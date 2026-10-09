package roster

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/primandproper/fijo/internal/domain/roster/generated"

	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/identifiers"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/observability/metrics"
	"github.com/primandproper/primitives-go/v2/tenancy"

	"github.com/jackc/pgx/v5/pgconn"
)

const storeName = "roster_store"

// The span and log keys this store attaches.
const (
	keyStudio    = "fijo.studio_id"
	keyHousehold = "fijo.household_id"
	keyContact   = "fijo.contact_id"
	keyAttendee  = "fijo.attendee_id"
)

// phoneIndex is the unique index ErrPhoneTaken is read off.
const phoneIndex = "roster_contacts_studio_phone_idx"

// SQLStore is the Postgres implementation of Store.
type SQLStore struct {
	o11y        observability.Observer
	instruments *metrics.OperationSet
	querier     generated.Querier

	// newID mints an identifier for a row whose caller did not name one.
	newID func() string
}

var _ Store = (*SQLStore)(nil)

// Option configures a SQLStore.
type Option func(*SQLStore)

// WithPillars attaches the observability pillars.
func WithPillars(p *observability.Pillars) Option {
	return func(s *SQLStore) {
		if p == nil {
			return
		}

		s.o11y = observability.NewObserver(storeName, p.Logger, p.TracerProvider)

		if set, err := metrics.NewOperationSet(p.MetricsProvider, storeName); err == nil {
			s.instruments = set
		}
	}
}

// NewSQLStore builds the store.
func NewSQLStore(opts ...Option) (*SQLStore, error) {
	instruments, err := metrics.NewOperationSet(nil, storeName)
	if err != nil {
		return nil, platformerrors.Wrap(err, "creating roster store instruments")
	}

	s := &SQLStore{
		o11y:        observability.NewObserver(storeName, nil, nil),
		instruments: instruments,
		querier:     generated.New(),
		newID:       identifiers.New,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}

	return s, nil
}

// observe is the ceremony every method here repeats, as the schedule store's.
func (s *SQLStore) observe(ctx context.Context) (context.Context, observability.Operation, func(*error)) {
	ctx, op := s.o11y.Begin(ctx)

	s.instruments.Attempt(ctx)

	stop := op.Time(ctx, nil, s.instruments.Latency)

	return ctx, op, func(errp *error) {
		stop()

		if errp != nil && *errp != nil {
			s.instruments.Failed(ctx)
		}

		op.End()
	}
}

func studioOf(scope tenancy.Scope) (string, error) {
	if err := scope.Validate(); err != nil {
		return "", err
	}

	if scope.IsGlobal() {
		return "", ErrNotStudioScoped
	}

	return scope.Owner(), nil
}

func readable(q database.SQLQueryExecutor, scope tenancy.Scope) (string, error) {
	if q == nil {
		return "", ErrNilExecutor
	}

	return studioOf(scope)
}

func writable(tx database.Tx, scope tenancy.Scope) (string, error) {
	if tx == nil {
		return "", ErrNilExecutor
	}

	return studioOf(scope)
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	return err
}

// phoneTaken maps the unique index on a studio's live phones onto
// ErrPhoneTaken, and leaves every other failure alone.
func phoneTaken(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == phoneIndex {
		return ErrPhoneTaken
	}

	return err
}

// CreateHousehold implements Store.
func (s *SQLStore) CreateHousehold(ctx context.Context, tx database.Tx, scope tenancy.Scope, household *Household) (_ *Household, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return nil, op.Error(err, "creating a household")
	}

	op.Set(keyStudio, studio)

	if household == nil {
		return nil, op.Error(ErrNilInput, "creating a household")
	}

	staged := *household
	staged.StudioID = studio
	staged.Name = strings.TrimSpace(staged.Name)

	if err = staged.Validate(); err != nil {
		return nil, op.Error(err, "creating a household")
	}

	id := staged.ID
	if id == "" {
		id = s.newID()
	}

	row, err := s.querier.CreateHousehold(ctx, tx, &generated.CreateHouseholdParams{
		ID:       id,
		StudioID: studio,
		Name:     staged.Name,
		Language: string(staged.Language),
		Notes:    staged.Notes,
	})
	if err != nil {
		return nil, op.Error(err, "creating a household")
	}

	op.Set(keyHousehold, row.ID)

	return householdFromRow(row), nil
}

// GetHousehold implements Store.
func (s *SQLStore) GetHousehold(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, id string) (_ *Household, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := readable(q, scope)
	if err != nil {
		return nil, op.Error(err, "reading a household")
	}

	op.Set(keyStudio, studio).Set(keyHousehold, id)

	if id == "" {
		return nil, op.Error(ErrEmptyID, "reading a household")
	}

	row, err := s.querier.GetHousehold(ctx, q, &generated.GetHouseholdParams{ID: id, StudioID: studio})
	if err != nil {
		return nil, op.Error(notFound(err), "reading a household")
	}

	households, err := s.withPeople(ctx, q, studio, []*generated.RosterHouseholds{row})
	if err != nil {
		return nil, op.Error(err, "reading a household")
	}

	return households[0], nil
}

// ListHouseholds implements Store.
func (s *SQLStore) ListHouseholds(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, includeArchived bool) (_ []*Household, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := readable(q, scope)
	if err != nil {
		return nil, op.Error(err, "listing households")
	}

	op.Set(keyStudio, studio)

	rows, err := s.querier.ListHouseholds(ctx, q, &generated.ListHouseholdsParams{StudioID: studio, IncludeArchived: includeArchived})
	if err != nil {
		return nil, op.Error(err, "listing households")
	}

	households, err := s.withPeople(ctx, q, studio, rows)
	if err != nil {
		return nil, op.Error(err, "listing households")
	}

	return households, nil
}

// UpdateHousehold implements Store.
func (s *SQLStore) UpdateHousehold(ctx context.Context, tx database.Tx, scope tenancy.Scope, household *Household) (_ *Household, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return nil, op.Error(err, "updating a household")
	}

	op.Set(keyStudio, studio)

	switch {
	case household == nil:
		return nil, op.Error(ErrNilInput, "updating a household")
	case household.ID == "":
		return nil, op.Error(ErrEmptyID, "updating a household")
	}

	op.Set(keyHousehold, household.ID)

	staged := *household
	staged.StudioID = studio
	staged.Name = strings.TrimSpace(staged.Name)

	if err = staged.Validate(); err != nil {
		return nil, op.Error(err, "updating a household")
	}

	row, err := s.querier.UpdateHousehold(ctx, tx, &generated.UpdateHouseholdParams{
		Name:     staged.Name,
		Language: string(staged.Language),
		Notes:    staged.Notes,
		ID:       staged.ID,
		StudioID: studio,
	})
	if err != nil {
		return nil, op.Error(notFound(err), "updating a household")
	}

	households, err := s.withPeople(ctx, tx, studio, []*generated.RosterHouseholds{row})
	if err != nil {
		return nil, op.Error(err, "updating a household")
	}

	return households[0], nil
}

// ArchiveHousehold implements Store.
func (s *SQLStore) ArchiveHousehold(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string) (err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return op.Error(err, "archiving a household")
	}

	op.Set(keyStudio, studio).Set(keyHousehold, id)

	if id == "" {
		return op.Error(ErrEmptyID, "archiving a household")
	}

	if _, err = s.querier.ArchiveHousehold(ctx, tx, &generated.ArchiveHouseholdParams{ID: id, StudioID: studio}); err != nil {
		return op.Error(notFound(err), "archiving a household")
	}

	return nil
}

// AttendeesOfHousehold implements Store.
func (s *SQLStore) AttendeesOfHousehold(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, householdID string) (_ []string, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := readable(q, scope)
	if err != nil {
		return nil, op.Error(err, "listing a household's attendees")
	}

	op.Set(keyStudio, studio).Set(keyHousehold, householdID)

	if householdID == "" {
		return nil, op.Error(ErrEmptyID, "listing a household's attendees")
	}

	ids, err := s.querier.ListAttendeesForHousehold(ctx, q, &generated.ListAttendeesForHouseholdParams{StudioID: studio, HouseholdID: householdID})
	if err != nil {
		return nil, op.Error(err, "listing a household's attendees")
	}

	return ids, nil
}

// CreateContact implements Store.
func (s *SQLStore) CreateContact(ctx context.Context, tx database.Tx, scope tenancy.Scope, contact *Contact) (_ *Contact, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return nil, op.Error(err, "creating a contact")
	}

	op.Set(keyStudio, studio)

	staged, err := s.stageContact(ctx, tx, studio, contact)
	if err != nil {
		return nil, op.Error(err, "creating a contact")
	}

	id := staged.ID
	if id == "" {
		id = s.newID()
	}

	row, err := s.querier.CreateContact(ctx, tx, &generated.CreateContactParams{
		ID:           id,
		StudioID:     studio,
		HouseholdID:  staged.HouseholdID,
		Name:         staged.Name,
		Phone:        staged.Phone,
		Language:     string(staged.Language),
		Relationship: string(staged.Relationship),
	})
	if err != nil {
		return nil, op.Error(phoneTaken(err), "creating a contact")
	}

	op.Set(keyContact, row.ID)

	return contactFromRow(row), nil
}

// GetContact implements Store.
func (s *SQLStore) GetContact(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, id string) (_ *Contact, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := readable(q, scope)
	if err != nil {
		return nil, op.Error(err, "reading a contact")
	}

	op.Set(keyStudio, studio).Set(keyContact, id)

	if id == "" {
		return nil, op.Error(ErrEmptyID, "reading a contact")
	}

	row, err := s.querier.GetContact(ctx, q, &generated.GetContactParams{ID: id, StudioID: studio})
	if err != nil {
		return nil, op.Error(notFound(err), "reading a contact")
	}

	return contactFromRow(row), nil
}

// UpdateContact implements Store. The household is not changed: a contact who
// belongs somewhere else is archived here and added there.
func (s *SQLStore) UpdateContact(ctx context.Context, tx database.Tx, scope tenancy.Scope, contact *Contact) (_ *Contact, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return nil, op.Error(err, "updating a contact")
	}

	op.Set(keyStudio, studio)

	switch {
	case contact == nil:
		return nil, op.Error(ErrNilInput, "updating a contact")
	case contact.ID == "":
		return nil, op.Error(ErrEmptyID, "updating a contact")
	}

	op.Set(keyContact, contact.ID)

	existing, err := s.querier.GetContact(ctx, tx, &generated.GetContactParams{ID: contact.ID, StudioID: studio})
	if err != nil {
		return nil, op.Error(notFound(err), "updating a contact")
	}

	withHousehold := *contact
	withHousehold.HouseholdID = existing.HouseholdID

	staged, err := s.stageContact(ctx, tx, studio, &withHousehold)
	if err != nil {
		return nil, op.Error(err, "updating a contact")
	}

	row, err := s.querier.UpdateContact(ctx, tx, &generated.UpdateContactParams{
		Name:         staged.Name,
		Phone:        staged.Phone,
		Language:     string(staged.Language),
		Relationship: string(staged.Relationship),
		ID:           staged.ID,
		StudioID:     studio,
	})
	if err != nil {
		return nil, op.Error(phoneTaken(notFound(err)), "updating a contact")
	}

	return contactFromRow(row), nil
}

// ArchiveContact implements Store.
func (s *SQLStore) ArchiveContact(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string) (err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return op.Error(err, "archiving a contact")
	}

	op.Set(keyStudio, studio).Set(keyContact, id)

	if id == "" {
		return op.Error(ErrEmptyID, "archiving a contact")
	}

	affected, err := s.querier.ArchiveContact(ctx, tx, &generated.ArchiveContactParams{ID: id, StudioID: studio})
	if err != nil {
		return op.Error(err, "archiving a contact")
	}

	if affected == 0 {
		return op.Error(ErrNotFound, "archiving a contact")
	}

	if err = s.querier.UnlinkContact(ctx, tx, &generated.UnlinkContactParams{ContactID: nullString(id), StudioID: studio}); err != nil {
		return op.Error(err, "archiving a contact")
	}

	return nil
}

// CreateAttendee implements Store.
func (s *SQLStore) CreateAttendee(ctx context.Context, tx database.Tx, scope tenancy.Scope, attendee *Attendee) (_ *Attendee, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return nil, op.Error(err, "creating an attendee")
	}

	op.Set(keyStudio, studio)

	staged, err := s.stageAttendee(ctx, tx, studio, attendee)
	if err != nil {
		return nil, op.Error(err, "creating an attendee")
	}

	id := staged.ID
	if id == "" {
		id = s.newID()
	}

	row, err := s.querier.CreateAttendee(ctx, tx, &generated.CreateAttendeeParams{
		ID:          id,
		StudioID:    studio,
		HouseholdID: staged.HouseholdID,
		Name:        staged.Name,
		Language:    string(staged.Language),
		ContactID:   nullString(staged.ContactID),
	})
	if err != nil {
		return nil, op.Error(err, "creating an attendee")
	}

	op.Set(keyAttendee, row.ID)

	return attendeeFromRow(row), nil
}

// GetAttendee implements Store.
func (s *SQLStore) GetAttendee(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, id string) (_ *Attendee, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := readable(q, scope)
	if err != nil {
		return nil, op.Error(err, "reading an attendee")
	}

	op.Set(keyStudio, studio).Set(keyAttendee, id)

	if id == "" {
		return nil, op.Error(ErrEmptyID, "reading an attendee")
	}

	row, err := s.querier.GetAttendee(ctx, q, &generated.GetAttendeeParams{ID: id, StudioID: studio})
	if err != nil {
		return nil, op.Error(notFound(err), "reading an attendee")
	}

	return attendeeFromRow(row), nil
}

// UpdateAttendee implements Store. As with a contact, the household stays put.
func (s *SQLStore) UpdateAttendee(ctx context.Context, tx database.Tx, scope tenancy.Scope, attendee *Attendee) (_ *Attendee, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return nil, op.Error(err, "updating an attendee")
	}

	op.Set(keyStudio, studio)

	switch {
	case attendee == nil:
		return nil, op.Error(ErrNilInput, "updating an attendee")
	case attendee.ID == "":
		return nil, op.Error(ErrEmptyID, "updating an attendee")
	}

	op.Set(keyAttendee, attendee.ID)

	existing, err := s.querier.GetAttendee(ctx, tx, &generated.GetAttendeeParams{ID: attendee.ID, StudioID: studio})
	if err != nil {
		return nil, op.Error(notFound(err), "updating an attendee")
	}

	withHousehold := *attendee
	withHousehold.HouseholdID = existing.HouseholdID

	staged, err := s.stageAttendee(ctx, tx, studio, &withHousehold)
	if err != nil {
		return nil, op.Error(err, "updating an attendee")
	}

	row, err := s.querier.UpdateAttendee(ctx, tx, &generated.UpdateAttendeeParams{
		Name:      staged.Name,
		Language:  string(staged.Language),
		ContactID: nullString(staged.ContactID),
		ID:        staged.ID,
		StudioID:  studio,
	})
	if err != nil {
		return nil, op.Error(notFound(err), "updating an attendee")
	}

	return attendeeFromRow(row), nil
}

// ArchiveAttendee implements Store.
func (s *SQLStore) ArchiveAttendee(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string) (err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return op.Error(err, "archiving an attendee")
	}

	op.Set(keyStudio, studio).Set(keyAttendee, id)

	if id == "" {
		return op.Error(ErrEmptyID, "archiving an attendee")
	}

	affected, err := s.querier.ArchiveAttendee(ctx, tx, &generated.ArchiveAttendeeParams{ID: id, StudioID: studio})
	if err != nil {
		return op.Error(err, "archiving an attendee")
	}

	if affected == 0 {
		return op.Error(ErrNotFound, "archiving an attendee")
	}

	return nil
}

// stageContact validates a contact for a write: the studio stamped, the phone
// normalized, and the household a live one of this studio's.
func (s *SQLStore) stageContact(ctx context.Context, tx database.Tx, studio string, contact *Contact) (*Contact, error) {
	if contact == nil {
		return nil, ErrNilInput
	}

	staged := *contact
	staged.StudioID = studio
	staged.Name = strings.TrimSpace(staged.Name)

	if err := staged.Validate(); err != nil {
		return nil, err
	}

	phone, err := NormalizePhone(staged.Phone)
	if err != nil {
		return nil, err
	}

	staged.Phone = phone

	if _, err = s.querier.GetHousehold(ctx, tx, &generated.GetHouseholdParams{ID: staged.HouseholdID, StudioID: studio}); err != nil {
		return nil, notFound(err)
	}

	return &staged, nil
}

// stageAttendee is stageContact for an attendee, plus the contact link: it must
// name a live contact on the same household.
func (s *SQLStore) stageAttendee(ctx context.Context, tx database.Tx, studio string, attendee *Attendee) (*Attendee, error) {
	if attendee == nil {
		return nil, ErrNilInput
	}

	staged := *attendee
	staged.StudioID = studio
	staged.Name = strings.TrimSpace(staged.Name)

	if err := staged.Validate(); err != nil {
		return nil, err
	}

	if _, err := s.querier.GetHousehold(ctx, tx, &generated.GetHouseholdParams{ID: staged.HouseholdID, StudioID: studio}); err != nil {
		return nil, notFound(err)
	}

	if staged.ContactID != "" {
		contact, err := s.querier.GetContact(ctx, tx, &generated.GetContactParams{ID: staged.ContactID, StudioID: studio})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, platformerrors.Wrap(ErrInvalidAttendee, "the contact is not on the roster")
			}

			return nil, err
		}

		if contact.HouseholdID != staged.HouseholdID {
			return nil, platformerrors.Wrap(ErrInvalidAttendee, "the contact is on another household")
		}
	}

	return &staged, nil
}

// withPeople reads every listed household's live contacts and attendees in two
// queries rather than two per household.
func (s *SQLStore) withPeople(ctx context.Context, q database.SQLQueryExecutor, studio string, rows []*generated.RosterHouseholds) ([]*Household, error) {
	households := make([]*Household, 0, len(rows))
	byID := make(map[string]*Household, len(rows))
	ids := make([]string, 0, len(rows))

	for _, row := range rows {
		household := householdFromRow(row)
		households = append(households, household)
		byID[household.ID] = household
		ids = append(ids, household.ID)
	}

	if len(ids) == 0 {
		return households, nil
	}

	contacts, err := s.querier.ListContactsForHouseholds(ctx, q, &generated.ListContactsForHouseholdsParams{StudioID: studio, HouseholdIds: ids})
	if err != nil {
		return nil, err
	}

	for _, row := range contacts {
		if household, ok := byID[row.HouseholdID]; ok {
			household.Contacts = append(household.Contacts, contactFromRow(row))
		}
	}

	attendees, err := s.querier.ListAttendeesForHouseholds(ctx, q, &generated.ListAttendeesForHouseholdsParams{StudioID: studio, HouseholdIds: ids})
	if err != nil {
		return nil, err
	}

	for _, row := range attendees {
		if household, ok := byID[row.HouseholdID]; ok {
			household.Attendees = append(household.Attendees, attendeeFromRow(row))
		}
	}

	return households, nil
}

func householdFromRow(row *generated.RosterHouseholds) *Household {
	return &Household{
		CreatedAt:     row.CreatedAt,
		LastUpdatedAt: database.TimePointerFromNullTime(row.LastUpdatedAt),
		ArchivedAt:    database.TimePointerFromNullTime(row.ArchivedAt),
		ID:            row.ID,
		StudioID:      row.StudioID,
		Name:          row.Name,
		Language:      Language(row.Language),
		Notes:         row.Notes,
		Contacts:      []*Contact{},
		Attendees:     []*Attendee{},
	}
}

func contactFromRow(row *generated.RosterContacts) *Contact {
	return &Contact{
		CreatedAt:     row.CreatedAt,
		LastUpdatedAt: database.TimePointerFromNullTime(row.LastUpdatedAt),
		ArchivedAt:    database.TimePointerFromNullTime(row.ArchivedAt),
		VerifiedAt:    database.TimePointerFromNullTime(row.VerifiedAt),
		ID:            row.ID,
		StudioID:      row.StudioID,
		HouseholdID:   row.HouseholdID,
		Name:          row.Name,
		Phone:         row.Phone,
		Language:      Language(row.Language),
		Relationship:  Relationship(row.Relationship),
		OptedOut:      row.OptedOut,
	}
}

func attendeeFromRow(row *generated.RosterAttendees) *Attendee {
	return &Attendee{
		CreatedAt:     row.CreatedAt,
		LastUpdatedAt: database.TimePointerFromNullTime(row.LastUpdatedAt),
		ArchivedAt:    database.TimePointerFromNullTime(row.ArchivedAt),
		ID:            row.ID,
		StudioID:      row.StudioID,
		HouseholdID:   row.HouseholdID,
		Name:          row.Name,
		Language:      Language(row.Language),
		ContactID:     row.ContactID.String,
	}
}

func nullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}
