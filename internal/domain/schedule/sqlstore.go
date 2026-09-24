package schedule

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/primandproper/fijo/internal/domain/schedule/generated"

	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/identifiers"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/observability/metrics"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

const storeName = "schedule_store"

// The span and log keys this store attaches.
const (
	keyStudio     = "fijo.studio_id"
	keyOffering   = "fijo.offering_id"
	keySeries     = "fijo.series_id"
	keyOccurrence = "fijo.occurrence_id"
)

// ErrNotStudioScoped is a read or a write handed the global scope. Everything
// in this package belongs to a studio, and the global scope names none.
var ErrNotStudioScoped = platformerrors.New("the schedule is studio-scoped and the scope names no studio")

// SQLStore is the Postgres implementation of Store.
//
// Like the owners store it takes no database.Client: its writes are handed a
// Tx and its reads an executor, and the database stamps the rows.
type SQLStore struct {
	o11y        observability.Observer
	instruments *metrics.OperationSet
	querier     generated.Querier

	// newID mints an identifier for a row whose caller did not name one. It is
	// a seam so a test can have the identifiers it expects.
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

// WithIDs replaces the identifier minter.
func WithIDs(mint func() string) Option {
	return func(s *SQLStore) {
		if mint != nil {
			s.newID = mint
		}
	}
}

// NewSQLStore builds the store.
func NewSQLStore(opts ...Option) (*SQLStore, error) {
	instruments, err := metrics.NewOperationSet(nil, storeName)
	if err != nil {
		return nil, platformerrors.Wrap(err, "creating schedule store instruments")
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

// observe is the ceremony every method here repeats: begin the operation, count
// the attempt, time it, and count a failure if there was one. The returned
// function is deferred with the address of the method's named error.
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

// studioOf validates the scope and returns the studio it names.
func studioOf(scope tenancy.Scope) (string, error) {
	if err := scope.Validate(); err != nil {
		return "", err
	}

	if scope.IsGlobal() {
		return "", ErrNotStudioScoped
	}

	return scope.Owner(), nil
}

// readable is the guard every read shares.
func readable(q database.SQLQueryExecutor, scope tenancy.Scope) (string, error) {
	if q == nil {
		return "", ErrNilExecutor
	}

	return studioOf(scope)
}

// writable is the guard every write shares.
func writable(tx database.Tx, scope tenancy.Scope) (string, error) {
	if tx == nil {
		return "", ErrNilExecutor
	}

	return studioOf(scope)
}

// notFound maps the driver's empty result onto this package's refusal.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	return err
}

// GetSettings implements Store.
func (s *SQLStore) GetSettings(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope) (_ *Settings, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := readable(q, scope)
	if err != nil {
		return nil, op.Error(err, "reading studio settings")
	}

	op.Set(keyStudio, studio)

	row, err := s.querier.GetStudioSettings(ctx, q, studio)
	if err != nil {
		// A studio that has set nothing has the defaults rather than an error:
		// the settings row is a customization, not a prerequisite.
		if errors.Is(err, sql.ErrNoRows) {
			return DefaultSettings(studio), nil
		}

		return nil, op.Error(err, "reading studio settings")
	}

	return settingsFromRow(row), nil
}

// SetSettings implements Store.
func (s *SQLStore) SetSettings(ctx context.Context, tx database.Tx, scope tenancy.Scope, settings *Settings) (_ *Settings, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return nil, op.Error(err, "writing studio settings")
	}

	op.Set(keyStudio, studio)

	if settings == nil {
		return nil, op.Error(ErrNilInput, "writing studio settings")
	}

	if settings.NoticeWindow < 0 {
		return nil, op.Error(platformerrors.Wrap(platformerrors.ErrUnrecognizedInputValue, "notice window may not be negative"), "writing studio settings")
	}

	if settings.HorizonWeeks < 1 || settings.HorizonWeeks > 52 {
		return nil, op.Error(platformerrors.Wrapf(platformerrors.ErrUnrecognizedInputValue, "horizon of %d weeks is outside one to fifty-two", settings.HorizonWeeks), "writing studio settings")
	}

	row, err := s.querier.UpsertStudioSettings(ctx, tx, &generated.UpsertStudioSettingsParams{
		StudioID:            studio,
		NoticeWindowMinutes: int32(settings.NoticeWindow / time.Minute),
		HorizonWeeks:        int32(settings.HorizonWeeks),
	})
	if err != nil {
		return nil, op.Error(err, "writing studio settings")
	}

	return settingsFromRow(row), nil
}

// CreateOffering implements Store.
func (s *SQLStore) CreateOffering(ctx context.Context, tx database.Tx, scope tenancy.Scope, offering *Offering) (_ *Offering, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return nil, op.Error(err, "creating an offering")
	}

	op.Set(keyStudio, studio)

	if offering == nil {
		return nil, op.Error(ErrNilInput, "creating an offering")
	}

	staged := *offering
	staged.StudioID = studio

	if err = staged.Validate(); err != nil {
		return nil, op.Error(err, "creating an offering")
	}

	id := staged.ID
	if id == "" {
		id = s.newID()
	}

	row, err := s.querier.CreateOffering(ctx, tx, &generated.CreateOfferingParams{
		ID:               id,
		StudioID:         studio,
		NameEn:           staged.NameEN,
		NameEs:           staged.NameES,
		Kind:             string(staged.Kind),
		DurationMinutes:  int32(staged.Duration / time.Minute),
		ListPrice:        staged.ListPrice,
		LessonCount:      nullInt32(staged.LessonCount),
		ExpiresAfterDays: nullInt32(int(staged.ExpiresAfter / (24 * time.Hour))),
		Shareable:        staged.Shareable,
	})
	if err != nil {
		return nil, op.Error(err, "creating an offering")
	}

	op.Set(keyOffering, row.ID)

	return offeringFromRow(row), nil
}

// GetOffering implements Store.
func (s *SQLStore) GetOffering(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, id string) (_ *Offering, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := readable(q, scope)
	if err != nil {
		return nil, op.Error(err, "reading an offering")
	}

	op.Set(keyStudio, studio).Set(keyOffering, id)

	if id == "" {
		return nil, op.Error(ErrEmptyID, "reading an offering")
	}

	row, err := s.querier.GetOffering(ctx, q, &generated.GetOfferingParams{ID: id, StudioID: studio})
	if err != nil {
		return nil, op.Error(notFound(err), "reading an offering")
	}

	return offeringFromRow(row), nil
}

// ListOfferings implements Store.
func (s *SQLStore) ListOfferings(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, includeArchived bool) (_ []*Offering, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := readable(q, scope)
	if err != nil {
		return nil, op.Error(err, "listing offerings")
	}

	op.Set(keyStudio, studio)

	rows, err := s.querier.ListOfferings(ctx, q, &generated.ListOfferingsParams{StudioID: studio, IncludeArchived: includeArchived})
	if err != nil {
		return nil, op.Error(err, "listing offerings")
	}

	offerings := make([]*Offering, 0, len(rows))
	for _, row := range rows {
		offerings = append(offerings, offeringFromRow(row))
	}

	return offerings, nil
}

// UpdateOffering implements Store.
func (s *SQLStore) UpdateOffering(ctx context.Context, tx database.Tx, scope tenancy.Scope, offering *Offering) (_ *Offering, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return nil, op.Error(err, "updating an offering")
	}

	op.Set(keyStudio, studio)

	if offering == nil {
		return nil, op.Error(ErrNilInput, "updating an offering")
	}

	if offering.ID == "" {
		return nil, op.Error(ErrEmptyID, "updating an offering")
	}

	op.Set(keyOffering, offering.ID)

	staged := *offering
	staged.StudioID = studio

	if err = staged.Validate(); err != nil {
		return nil, op.Error(err, "updating an offering")
	}

	row, err := s.querier.UpdateOffering(ctx, tx, &generated.UpdateOfferingParams{
		NameEn:           staged.NameEN,
		NameEs:           staged.NameES,
		DurationMinutes:  int32(staged.Duration / time.Minute),
		ListPrice:        staged.ListPrice,
		LessonCount:      nullInt32(staged.LessonCount),
		ExpiresAfterDays: nullInt32(int(staged.ExpiresAfter / (24 * time.Hour))),
		Shareable:        staged.Shareable,
		ID:               staged.ID,
		StudioID:         studio,
	})
	if err != nil {
		return nil, op.Error(notFound(err), "updating an offering")
	}

	return offeringFromRow(row), nil
}

// ArchiveOffering implements Store.
func (s *SQLStore) ArchiveOffering(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string) (err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return op.Error(err, "archiving an offering")
	}

	op.Set(keyStudio, studio).Set(keyOffering, id)

	if id == "" {
		return op.Error(ErrEmptyID, "archiving an offering")
	}

	affected, err := s.querier.ArchiveOffering(ctx, tx, &generated.ArchiveOfferingParams{ID: id, StudioID: studio})
	if err != nil {
		return op.Error(err, "archiving an offering")
	}

	if affected == 0 {
		return op.Error(ErrNotFound, "archiving an offering")
	}

	return nil
}

// CreateSeries implements Store.
func (s *SQLStore) CreateSeries(ctx context.Context, tx database.Tx, scope tenancy.Scope, series *Series) (_ *Series, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return nil, op.Error(err, "creating a series")
	}

	op.Set(keyStudio, studio)

	if series == nil {
		return nil, op.Error(ErrNilInput, "creating a series")
	}

	staged := *series
	staged.StudioID = studio

	if err = staged.Validate(); err != nil {
		return nil, op.Error(err, "creating a series")
	}

	id := staged.ID
	if id == "" {
		id = s.newID()
	}

	row, err := s.querier.CreateSeries(ctx, tx, &generated.CreateSeriesParams{
		ID:            id,
		StudioID:      studio,
		OfferingID:    staged.OfferingID,
		Weekday:       int16(staged.Weekday),
		StartMinute:   int16(staged.StartTime.Minutes()),
		IntervalWeeks: int16(staged.IntervalWeeks),
		StartsOn:      staged.StartsOn.Time(),
		EndsOn:        nullDate(staged.EndsOn),
		PriceOverride: nullInt64(staged.PriceOverride),
	})
	if err != nil {
		return nil, op.Error(err, "creating a series")
	}

	op.Set(keySeries, row.ID)

	if err = s.replaceSeriesStaff(ctx, tx, row.ID, staged.Staff); err != nil {
		return nil, op.Error(err, "creating a series")
	}

	built := seriesFromRow(row)
	built.Staff = dedupe(staged.Staff)

	return built, nil
}

// GetSeries implements Store.
func (s *SQLStore) GetSeries(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, id string) (_ *Series, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := readable(q, scope)
	if err != nil {
		return nil, op.Error(err, "reading a series")
	}

	op.Set(keyStudio, studio).Set(keySeries, id)

	if id == "" {
		return nil, op.Error(ErrEmptyID, "reading a series")
	}

	row, err := s.querier.GetSeries(ctx, q, &generated.GetSeriesParams{ID: id, StudioID: studio})
	if err != nil {
		return nil, op.Error(notFound(err), "reading a series")
	}

	series := seriesFromRow(row)

	staff, err := s.staffForSeries(ctx, q, []string{row.ID})
	if err != nil {
		return nil, op.Error(err, "reading a series")
	}

	series.Staff = staff[row.ID]

	return series, nil
}

// ListSeries implements Store.
func (s *SQLStore) ListSeries(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, includeArchived bool) (_ []*Series, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := readable(q, scope)
	if err != nil {
		return nil, op.Error(err, "listing series")
	}

	op.Set(keyStudio, studio)

	rows, err := s.querier.ListSeries(ctx, q, &generated.ListSeriesParams{StudioID: studio, IncludeArchived: includeArchived})
	if err != nil {
		return nil, op.Error(err, "listing series")
	}

	series, err := s.attachSeriesStaff(ctx, q, rows)
	if err != nil {
		return nil, op.Error(err, "listing series")
	}

	return series, nil
}

// ListLiveSeries implements Store.
func (s *SQLStore) ListLiveSeries(ctx context.Context, q database.SQLQueryExecutor, today, horizon Date) (_ []*Series, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	if q == nil {
		return nil, op.Error(ErrNilExecutor, "listing live series")
	}

	if today.IsZero() || horizon.IsZero() {
		return nil, op.Error(platformerrors.Wrap(platformerrors.ErrUnrecognizedInputValue, "both ends of the window are required"), "listing live series")
	}

	rows, err := s.querier.ListLiveSeries(ctx, q, &generated.ListLiveSeriesParams{
		Horizon: horizon.Time(),
		Today:   today.Time(),
	})
	if err != nil {
		return nil, op.Error(err, "listing live series")
	}

	series, err := s.attachSeriesStaff(ctx, q, rows)
	if err != nil {
		return nil, op.Error(err, "listing live series")
	}

	return series, nil
}

// UpdateSeries implements Store.
func (s *SQLStore) UpdateSeries(ctx context.Context, tx database.Tx, scope tenancy.Scope, series *Series) (_ *Series, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return nil, op.Error(err, "updating a series")
	}

	op.Set(keyStudio, studio)

	if series == nil {
		return nil, op.Error(ErrNilInput, "updating a series")
	}

	if series.ID == "" {
		return nil, op.Error(ErrEmptyID, "updating a series")
	}

	op.Set(keySeries, series.ID)

	staged := *series
	staged.StudioID = studio

	if err = staged.Validate(); err != nil {
		return nil, op.Error(err, "updating a series")
	}

	row, err := s.querier.UpdateSeries(ctx, tx, &generated.UpdateSeriesParams{
		OfferingID:    staged.OfferingID,
		Weekday:       int16(staged.Weekday),
		StartMinute:   int16(staged.StartTime.Minutes()),
		IntervalWeeks: int16(staged.IntervalWeeks),
		StartsOn:      staged.StartsOn.Time(),
		EndsOn:        nullDate(staged.EndsOn),
		PriceOverride: nullInt64(staged.PriceOverride),
		ID:            staged.ID,
		StudioID:      studio,
	})
	if err != nil {
		return nil, op.Error(notFound(err), "updating a series")
	}

	if err = s.replaceSeriesStaff(ctx, tx, row.ID, staged.Staff); err != nil {
		return nil, op.Error(err, "updating a series")
	}

	built := seriesFromRow(row)
	built.Staff = dedupe(staged.Staff)

	return built, nil
}

// EndSeries implements Store.
func (s *SQLStore) EndSeries(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string, endsOn Date) (_ *Series, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return nil, op.Error(err, "ending a series")
	}

	op.Set(keyStudio, studio).Set(keySeries, id)

	switch {
	case id == "":
		return nil, op.Error(ErrEmptyID, "ending a series")
	case endsOn.IsZero():
		return nil, op.Error(platformerrors.Wrap(ErrInvalidSeries, "a series ends on a date"), "ending a series")
	}

	row, err := s.querier.EndSeries(ctx, tx, &generated.EndSeriesParams{
		EndsOn:   nullDate(endsOn),
		ID:       id,
		StudioID: studio,
	})
	if err != nil {
		return nil, op.Error(notFound(err), "ending a series")
	}

	series := seriesFromRow(row)

	staff, err := s.staffForSeries(ctx, tx, []string{row.ID})
	if err != nil {
		return nil, op.Error(err, "ending a series")
	}

	series.Staff = staff[row.ID]

	return series, nil
}

// ArchiveSeries implements Store.
func (s *SQLStore) ArchiveSeries(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string) (err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return op.Error(err, "archiving a series")
	}

	op.Set(keyStudio, studio).Set(keySeries, id)

	if id == "" {
		return op.Error(ErrEmptyID, "archiving a series")
	}

	affected, err := s.querier.ArchiveSeries(ctx, tx, &generated.ArchiveSeriesParams{ID: id, StudioID: studio})
	if err != nil {
		return op.Error(err, "archiving a series")
	}

	if affected == 0 {
		return op.Error(ErrNotFound, "archiving a series")
	}

	return nil
}

// MaterializeOccurrence implements Store.
func (s *SQLStore) MaterializeOccurrence(ctx context.Context, tx database.Tx, scope tenancy.Scope, occurrence *Occurrence) (_ *Occurrence, _ bool, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return nil, false, op.Error(err, "materializing an occurrence")
	}

	op.Set(keyStudio, studio)

	switch {
	case occurrence == nil:
		return nil, false, op.Error(ErrNilInput, "materializing an occurrence")
	case occurrence.SeriesID == "":
		return nil, false, op.Error(platformerrors.Wrap(ErrInvalidOccurrence, "a materialized occurrence belongs to a series"), "materializing an occurrence")
	case occurrence.OfferingID == "":
		return nil, false, op.Error(platformerrors.Wrap(ErrInvalidOccurrence, "no offering"), "materializing an occurrence")
	case !occurrence.EndsAt.After(occurrence.StartsAt):
		return nil, false, op.Error(platformerrors.Wrap(ErrInvalidOccurrence, "ends before it starts"), "materializing an occurrence")
	}

	op.Set(keySeries, occurrence.SeriesID)

	id := occurrence.ID
	if id == "" {
		id = s.newID()
	}

	row, err := s.querier.MaterializeOccurrence(ctx, tx, &generated.MaterializeOccurrenceParams{
		ID:         id,
		StudioID:   studio,
		SeriesID:   nullString(occurrence.SeriesID),
		OfferingID: occurrence.OfferingID,
		StartsAt:   occurrence.StartsAt,
		EndsAt:     occurrence.EndsAt,
	})
	if err != nil {
		// ON CONFLICT DO NOTHING returns no row, which is the worker running
		// again over a horizon it has already written. It is the answer, not a
		// failure.
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}

		return nil, false, op.Error(err, "materializing an occurrence")
	}

	op.Set(keyOccurrence, row.ID)

	if err = s.replaceOccurrenceStaff(ctx, tx, row.ID, occurrence.Staff); err != nil {
		return nil, false, op.Error(err, "materializing an occurrence")
	}

	built := occurrenceFromRow(row)
	built.Staff = dedupe(occurrence.Staff)

	return built, true, nil
}

// CreateOccurrence implements Store.
func (s *SQLStore) CreateOccurrence(ctx context.Context, tx database.Tx, scope tenancy.Scope, occurrence *Occurrence) (_ *Occurrence, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return nil, op.Error(err, "creating an occurrence")
	}

	op.Set(keyStudio, studio)

	switch {
	case occurrence == nil:
		return nil, op.Error(ErrNilInput, "creating an occurrence")
	case occurrence.OfferingID == "":
		return nil, op.Error(platformerrors.Wrap(ErrInvalidOccurrence, "no offering"), "creating an occurrence")
	case !occurrence.EndsAt.After(occurrence.StartsAt):
		return nil, op.Error(platformerrors.Wrap(ErrInvalidOccurrence, "ends before it starts"), "creating an occurrence")
	}

	id := occurrence.ID
	if id == "" {
		id = s.newID()
	}

	row, err := s.querier.CreateOccurrence(ctx, tx, &generated.CreateOccurrenceParams{
		ID:                   id,
		StudioID:             studio,
		SeriesID:             nullString(occurrence.SeriesID),
		OfferingID:           occurrence.OfferingID,
		StartsAt:             occurrence.StartsAt,
		EndsAt:               occurrence.EndsAt,
		Reason:               occurrence.Reason,
		ReplacesOccurrenceID: nullString(occurrence.Replaces),
	})
	if err != nil {
		return nil, op.Error(err, "creating an occurrence")
	}

	op.Set(keyOccurrence, row.ID)

	if err = s.replaceOccurrenceStaff(ctx, tx, row.ID, occurrence.Staff); err != nil {
		return nil, op.Error(err, "creating an occurrence")
	}

	built := occurrenceFromRow(row)
	built.Staff = dedupe(occurrence.Staff)

	return built, nil
}

// GetOccurrence implements Store.
func (s *SQLStore) GetOccurrence(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, id string) (_ *Occurrence, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := readable(q, scope)
	if err != nil {
		return nil, op.Error(err, "reading an occurrence")
	}

	op.Set(keyStudio, studio).Set(keyOccurrence, id)

	if id == "" {
		return nil, op.Error(ErrEmptyID, "reading an occurrence")
	}

	row, err := s.querier.GetOccurrence(ctx, q, &generated.GetOccurrenceParams{ID: id, StudioID: studio})
	if err != nil {
		return nil, op.Error(notFound(err), "reading an occurrence")
	}

	occurrence := occurrenceFromRow(row)

	staff, err := s.staffForOccurrences(ctx, q, []string{row.ID})
	if err != nil {
		return nil, op.Error(err, "reading an occurrence")
	}

	occurrence.Staff = staff[row.ID]

	return occurrence, nil
}

// ListOccurrencesInWindow implements Store.
func (s *SQLStore) ListOccurrencesInWindow(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, from, until time.Time) (_ []*Occurrence, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := readable(q, scope)
	if err != nil {
		return nil, op.Error(err, "listing occurrences")
	}

	op.Set(keyStudio, studio)

	if !until.After(from) {
		return nil, op.Error(platformerrors.Wrap(platformerrors.ErrUnrecognizedInputValue, "the window ends before it starts"), "listing occurrences")
	}

	rows, err := s.querier.ListOccurrencesInWindow(ctx, q, &generated.ListOccurrencesInWindowParams{
		StudioID:    studio,
		WindowStart: from,
		WindowEnd:   until,
	})
	if err != nil {
		return nil, op.Error(err, "listing occurrences")
	}

	occurrences, err := s.attachOccurrenceStaff(ctx, q, rows)
	if err != nil {
		return nil, op.Error(err, "listing occurrences")
	}

	return occurrences, nil
}

// ListOccurrencesForSeries implements Store.
func (s *SQLStore) ListOccurrencesForSeries(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, seriesID string) (_ []*Occurrence, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := readable(q, scope)
	if err != nil {
		return nil, op.Error(err, "listing a series' occurrences")
	}

	op.Set(keyStudio, studio).Set(keySeries, seriesID)

	if seriesID == "" {
		return nil, op.Error(ErrEmptyID, "listing a series' occurrences")
	}

	rows, err := s.querier.ListOccurrencesForSeries(ctx, q, &generated.ListOccurrencesForSeriesParams{
		StudioID: studio,
		SeriesID: nullString(seriesID),
	})
	if err != nil {
		return nil, op.Error(err, "listing a series' occurrences")
	}

	occurrences, err := s.attachOccurrenceStaff(ctx, q, rows)
	if err != nil {
		return nil, op.Error(err, "listing a series' occurrences")
	}

	return occurrences, nil
}

// ListScheduledForSeriesFrom implements Store.
func (s *SQLStore) ListScheduledForSeriesFrom(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, seriesID string, from time.Time) (_ []*Occurrence, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := readable(q, scope)
	if err != nil {
		return nil, op.Error(err, "listing standing occurrences for a series")
	}

	op.Set(keyStudio, studio).Set(keySeries, seriesID)

	if seriesID == "" {
		return nil, op.Error(ErrEmptyID, "listing standing occurrences for a series")
	}

	rows, err := s.querier.ListScheduledOccurrencesForSeriesFrom(ctx, q, &generated.ListScheduledOccurrencesForSeriesFromParams{
		StudioID:    studio,
		SeriesID:    nullString(seriesID),
		FromInstant: from,
	})
	if err != nil {
		return nil, op.Error(err, "listing standing occurrences for a series")
	}

	occurrences, err := s.attachOccurrenceStaff(ctx, q, rows)
	if err != nil {
		return nil, op.Error(err, "listing standing occurrences for a series")
	}

	return occurrences, nil
}

// ListScheduledInWindow implements Store.
func (s *SQLStore) ListScheduledInWindow(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, from, until time.Time) (_ []*Occurrence, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := readable(q, scope)
	if err != nil {
		return nil, op.Error(err, "listing standing occurrences")
	}

	op.Set(keyStudio, studio)

	if !until.After(from) {
		return nil, op.Error(platformerrors.Wrap(platformerrors.ErrUnrecognizedInputValue, "the window ends before it starts"), "listing standing occurrences")
	}

	rows, err := s.querier.ListScheduledOccurrencesInWindow(ctx, q, &generated.ListScheduledOccurrencesInWindowParams{
		StudioID:    studio,
		WindowStart: from,
		WindowEnd:   until,
	})
	if err != nil {
		return nil, op.Error(err, "listing standing occurrences")
	}

	occurrences, err := s.attachOccurrenceStaff(ctx, q, rows)
	if err != nil {
		return nil, op.Error(err, "listing standing occurrences")
	}

	return occurrences, nil
}

// SetOccurrenceState implements Store.
func (s *SQLStore) SetOccurrenceState(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string, state OccurrenceState, reason string) (_ *Occurrence, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return nil, op.Error(err, "setting an occurrence's state")
	}

	op.Set(keyStudio, studio).Set(keyOccurrence, id)

	switch {
	case id == "":
		return nil, op.Error(ErrEmptyID, "setting an occurrence's state")
	case !state.Valid():
		return nil, op.Error(platformerrors.Wrapf(ErrInvalidOccurrence, "unknown state %q", state), "setting an occurrence's state")
	case state == StateCancelled:
		return nil, op.Error(platformerrors.Wrap(ErrInvalidOccurrence, "cancelling records who cancelled; use CancelOccurrence"), "setting an occurrence's state")
	case state == StateMoved:
		return nil, op.Error(platformerrors.Wrap(ErrInvalidOccurrence, "moving names a replacement; use MarkOccurrenceMoved"), "setting an occurrence's state")
	}

	row, err := s.querier.SetOccurrenceState(ctx, tx, &generated.SetOccurrenceStateParams{
		State:    string(state),
		Reason:   reason,
		ID:       id,
		StudioID: studio,
	})
	if err != nil {
		return nil, op.Error(s.refused(ctx, tx, studio, id, err), "setting an occurrence's state")
	}

	return s.withStaff(ctx, tx, row)
}

// CancelOccurrence implements Store.
func (s *SQLStore) CancelOccurrence(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string, by Canceller, insideNotice bool, reason string) (_ *Occurrence, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return nil, op.Error(err, "cancelling an occurrence")
	}

	op.Set(keyStudio, studio).Set(keyOccurrence, id)

	switch {
	case id == "":
		return nil, op.Error(ErrEmptyID, "cancelling an occurrence")
	case !by.Valid():
		return nil, op.Error(platformerrors.Wrapf(ErrInvalidOccurrence, "unknown canceller %q", by), "cancelling an occurrence")
	}

	row, err := s.querier.CancelOccurrence(ctx, tx, &generated.CancelOccurrenceParams{
		CancelledBy:           nullString(string(by)),
		CancelledInsideNotice: sql.NullBool{Bool: insideNotice, Valid: true},
		Reason:                reason,
		ID:                    id,
		StudioID:              studio,
	})
	if err != nil {
		return nil, op.Error(s.refused(ctx, tx, studio, id, err), "cancelling an occurrence")
	}

	return s.withStaff(ctx, tx, row)
}

// MarkOccurrenceMoved implements Store.
func (s *SQLStore) MarkOccurrenceMoved(ctx context.Context, tx database.Tx, scope tenancy.Scope, id, movedToID, reason string) (_ *Occurrence, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return nil, op.Error(err, "moving an occurrence")
	}

	op.Set(keyStudio, studio).Set(keyOccurrence, id)

	switch {
	case id == "":
		return nil, op.Error(ErrEmptyID, "moving an occurrence")
	case movedToID == "":
		return nil, op.Error(platformerrors.Wrap(ErrInvalidOccurrence, "a move names what it became"), "moving an occurrence")
	case movedToID == id:
		return nil, op.Error(platformerrors.Wrap(ErrInvalidOccurrence, "an occurrence cannot become itself"), "moving an occurrence")
	}

	row, err := s.querier.MarkOccurrenceMoved(ctx, tx, &generated.MarkOccurrenceMovedParams{
		MovedToOccurrenceID: nullString(movedToID),
		Reason:              reason,
		ID:                  id,
		StudioID:            studio,
	})
	if err != nil {
		return nil, op.Error(s.refused(ctx, tx, studio, id, err), "moving an occurrence")
	}

	return s.withStaff(ctx, tx, row)
}

// SetOccurrenceStaff implements Store.
func (s *SQLStore) SetOccurrenceStaff(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string, staff []string) (_ *Occurrence, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return nil, op.Error(err, "reassigning an occurrence")
	}

	op.Set(keyStudio, studio).Set(keyOccurrence, id)

	if id == "" {
		return nil, op.Error(ErrEmptyID, "reassigning an occurrence")
	}

	// The read is what scopes the write: the staff table has no studio of its
	// own, so a caller who may not read the occurrence may not reassign it.
	row, err := s.querier.GetOccurrence(ctx, tx, &generated.GetOccurrenceParams{ID: id, StudioID: studio})
	if err != nil {
		return nil, op.Error(notFound(err), "reassigning an occurrence")
	}

	if err = s.replaceOccurrenceStaff(ctx, tx, row.ID, staff); err != nil {
		return nil, op.Error(err, "reassigning an occurrence")
	}

	occurrence := occurrenceFromRow(row)
	occurrence.Staff = dedupe(staff)

	return occurrence, nil
}

// CreateClosure implements Store.
func (s *SQLStore) CreateClosure(ctx context.Context, tx database.Tx, scope tenancy.Scope, closure *Closure) (_ *Closure, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return nil, op.Error(err, "closing the studio")
	}

	op.Set(keyStudio, studio)

	if closure == nil {
		return nil, op.Error(ErrNilInput, "closing the studio")
	}

	staged := *closure
	staged.StudioID = studio

	if err = staged.Validate(); err != nil {
		return nil, op.Error(err, "closing the studio")
	}

	id := staged.ID
	if id == "" {
		id = s.newID()
	}

	row, err := s.querier.CreateClosure(ctx, tx, &generated.CreateClosureParams{
		ID:       id,
		StudioID: studio,
		StartsOn: staged.StartsOn.Time(),
		EndsOn:   staged.EndsOn.Time(),
		Reason:   staged.Reason,
	})
	if err != nil {
		return nil, op.Error(err, "closing the studio")
	}

	return closureFromRow(row), nil
}

// ListClosures implements Store.
func (s *SQLStore) ListClosures(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope) (_ []*Closure, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := readable(q, scope)
	if err != nil {
		return nil, op.Error(err, "listing closures")
	}

	op.Set(keyStudio, studio)

	rows, err := s.querier.ListClosures(ctx, q, studio)
	if err != nil {
		return nil, op.Error(err, "listing closures")
	}

	closures := make([]*Closure, 0, len(rows))
	for _, row := range rows {
		closures = append(closures, closureFromRow(row))
	}

	return closures, nil
}

// ListClosuresForStudios implements Store.
func (s *SQLStore) ListClosuresForStudios(ctx context.Context, q database.SQLQueryExecutor, studioIDs []string) (_ map[string][]*Closure, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	if q == nil {
		return nil, op.Error(ErrNilExecutor, "listing closures for studios")
	}

	ids := dedupe(studioIDs)
	if len(ids) == 0 {
		return map[string][]*Closure{}, nil
	}

	rows, err := s.querier.ListClosuresForStudios(ctx, q, ids)
	if err != nil {
		return nil, op.Error(err, "listing closures for studios")
	}

	byStudio := make(map[string][]*Closure, len(ids))
	for _, row := range rows {
		byStudio[row.StudioID] = append(byStudio[row.StudioID], closureFromRow(row))
	}

	return byStudio, nil
}

// ArchiveClosure implements Store.
func (s *SQLStore) ArchiveClosure(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string) (err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return op.Error(err, "reopening the studio")
	}

	op.Set(keyStudio, studio)

	if id == "" {
		return op.Error(ErrEmptyID, "reopening the studio")
	}

	affected, err := s.querier.ArchiveClosure(ctx, tx, &generated.ArchiveClosureParams{ID: id, StudioID: studio})
	if err != nil {
		return op.Error(err, "reopening the studio")
	}

	if affected == 0 {
		return op.Error(ErrNotFound, "reopening the studio")
	}

	return nil
}

// RetractOccurrence implements Store.
func (s *SQLStore) RetractOccurrence(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string) (err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	studio, err := writable(tx, scope)
	if err != nil {
		return op.Error(err, "retracting an occurrence")
	}

	op.Set(keyStudio, studio).Set(keyOccurrence, id)

	if id == "" {
		return op.Error(ErrEmptyID, "retracting an occurrence")
	}

	affected, err := s.querier.RetractOccurrence(ctx, tx, &generated.RetractOccurrenceParams{ID: id, StudioID: studio})
	if err != nil {
		return op.Error(err, "retracting an occurrence")
	}

	if affected == 0 {
		return op.Error(ErrStateTransitionRefused, "retracting an occurrence")
	}

	return nil
}

// refused distinguishes the two reasons a guarded update wrote nothing: the
// occurrence is not there, or it is there in a state the command does not
// apply to. Both arrive as no rows, and a caller that cannot tell them apart
// cannot say anything useful to the person who pressed the button.
func (s *SQLStore) refused(ctx context.Context, tx database.Tx, studio, id string, err error) error {
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	if _, getErr := s.querier.GetOccurrence(ctx, tx, &generated.GetOccurrenceParams{ID: id, StudioID: studio}); getErr != nil {
		return ErrNotFound
	}

	return ErrStateTransitionRefused
}

// withStaff reads an occurrence's staff back onto the row a command returned.
func (s *SQLStore) withStaff(ctx context.Context, tx database.Tx, row *generated.ScheduleOccurrences) (*Occurrence, error) {
	occurrence := occurrenceFromRow(row)

	staff, err := s.staffForOccurrences(ctx, tx, []string{row.ID})
	if err != nil {
		return nil, err
	}

	occurrence.Staff = staff[row.ID]

	return occurrence, nil
}

func (s *SQLStore) replaceSeriesStaff(ctx context.Context, tx database.Tx, seriesID string, staff []string) error {
	if err := s.querier.ClearSeriesStaff(ctx, tx, seriesID); err != nil {
		return err
	}

	for _, member := range dedupe(staff) {
		if err := s.querier.AddSeriesStaff(ctx, tx, &generated.AddSeriesStaffParams{SeriesID: seriesID, StaffUserID: member}); err != nil {
			return err
		}
	}

	return nil
}

func (s *SQLStore) replaceOccurrenceStaff(ctx context.Context, tx database.Tx, occurrenceID string, staff []string) error {
	if err := s.querier.ClearOccurrenceStaff(ctx, tx, occurrenceID); err != nil {
		return err
	}

	for _, member := range dedupe(staff) {
		if err := s.querier.AddOccurrenceStaff(ctx, tx, &generated.AddOccurrenceStaffParams{OccurrenceID: occurrenceID, StaffUserID: member}); err != nil {
			return err
		}
	}

	return nil
}

func (s *SQLStore) staffForSeries(ctx context.Context, q database.SQLQueryExecutor, ids []string) (map[string][]string, error) {
	byID := make(map[string][]string, len(ids))

	if len(ids) == 0 {
		return byID, nil
	}

	rows, err := s.querier.ListStaffForSeries(ctx, q, ids)
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		byID[row.SeriesID] = append(byID[row.SeriesID], row.StaffUserID)
	}

	return byID, nil
}

func (s *SQLStore) staffForOccurrences(ctx context.Context, q database.SQLQueryExecutor, ids []string) (map[string][]string, error) {
	byID := make(map[string][]string, len(ids))

	if len(ids) == 0 {
		return byID, nil
	}

	rows, err := s.querier.ListStaffForOccurrences(ctx, q, ids)
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		byID[row.OccurrenceID] = append(byID[row.OccurrenceID], row.StaffUserID)
	}

	return byID, nil
}

// attachSeriesStaff reads every listed series' staff in one query rather than
// one per row.
func (s *SQLStore) attachSeriesStaff(ctx context.Context, q database.SQLQueryExecutor, rows []*generated.ScheduleSeries) ([]*Series, error) {
	series := make([]*Series, 0, len(rows))
	ids := make([]string, 0, len(rows))

	for _, row := range rows {
		series = append(series, seriesFromRow(row))
		ids = append(ids, row.ID)
	}

	staff, err := s.staffForSeries(ctx, q, ids)
	if err != nil {
		return nil, err
	}

	for _, one := range series {
		one.Staff = staff[one.ID]
	}

	return series, nil
}

// attachOccurrenceStaff is attachSeriesStaff for occurrences.
func (s *SQLStore) attachOccurrenceStaff(ctx context.Context, q database.SQLQueryExecutor, rows []*generated.ScheduleOccurrences) ([]*Occurrence, error) {
	occurrences := make([]*Occurrence, 0, len(rows))
	ids := make([]string, 0, len(rows))

	for _, row := range rows {
		occurrences = append(occurrences, occurrenceFromRow(row))
		ids = append(ids, row.ID)
	}

	staff, err := s.staffForOccurrences(ctx, q, ids)
	if err != nil {
		return nil, err
	}

	for _, one := range occurrences {
		one.Staff = staff[one.ID]
	}

	return occurrences, nil
}

func settingsFromRow(row *generated.ScheduleStudioSettings) *Settings {
	if row == nil {
		return nil
	}

	return &Settings{
		CreatedAt:     row.CreatedAt,
		LastUpdatedAt: database.TimePointerFromNullTime(row.LastUpdatedAt),
		StudioID:      row.StudioID,
		NoticeWindow:  time.Duration(row.NoticeWindowMinutes) * time.Minute,
		HorizonWeeks:  int(row.HorizonWeeks),
	}
}

func offeringFromRow(row *generated.ScheduleOfferings) *Offering {
	if row == nil {
		return nil
	}

	offering := &Offering{
		CreatedAt:     row.CreatedAt,
		LastUpdatedAt: database.TimePointerFromNullTime(row.LastUpdatedAt),
		ArchivedAt:    database.TimePointerFromNullTime(row.ArchivedAt),
		ID:            row.ID,
		StudioID:      row.StudioID,
		NameEN:        row.NameEn,
		NameES:        row.NameEs,
		Kind:          OfferingKind(row.Kind),
		Duration:      time.Duration(row.DurationMinutes) * time.Minute,
		ListPrice:     row.ListPrice,
		Shareable:     row.Shareable,
	}

	if row.LessonCount.Valid {
		offering.LessonCount = int(row.LessonCount.Int32)
	}

	if row.ExpiresAfterDays.Valid {
		offering.ExpiresAfter = time.Duration(row.ExpiresAfterDays.Int32) * 24 * time.Hour
	}

	return offering
}

func seriesFromRow(row *generated.ScheduleSeries) *Series {
	if row == nil {
		return nil
	}

	series := &Series{
		CreatedAt:     row.CreatedAt,
		LastUpdatedAt: database.TimePointerFromNullTime(row.LastUpdatedAt),
		ArchivedAt:    database.TimePointerFromNullTime(row.ArchivedAt),
		ID:            row.ID,
		StudioID:      row.StudioID,
		OfferingID:    row.OfferingID,
		Weekday:       time.Weekday(row.Weekday),
		StartTime:     ClockTimeOfMinutes(int(row.StartMinute)),
		IntervalWeeks: int(row.IntervalWeeks),
		StartsOn:      DateOf(row.StartsOn),
		Staff:         []string{},
	}

	if row.EndsOn.Valid {
		series.EndsOn = DateOf(row.EndsOn.Time)
	}

	if row.PriceOverride.Valid {
		override := row.PriceOverride.Int64
		series.PriceOverride = &override
	}

	return series
}

func occurrenceFromRow(row *generated.ScheduleOccurrences) *Occurrence {
	if row == nil {
		return nil
	}

	occurrence := &Occurrence{
		CreatedAt:     row.CreatedAt,
		LastUpdatedAt: database.TimePointerFromNullTime(row.LastUpdatedAt),
		ArchivedAt:    database.TimePointerFromNullTime(row.ArchivedAt),
		StartsAt:      row.StartsAt,
		EndsAt:        row.EndsAt,
		ID:            row.ID,
		StudioID:      row.StudioID,
		SeriesID:      row.SeriesID.String,
		OfferingID:    row.OfferingID,
		State:         OccurrenceState(row.State),
		Reason:        row.Reason,
		MovedTo:       row.MovedToOccurrenceID.String,
		Replaces:      row.ReplacesOccurrenceID.String,
		Staff:         []string{},
	}

	if row.CancelledBy.Valid {
		occurrence.CancelledBy = Canceller(row.CancelledBy.String)
	}

	if row.CancelledInsideNotice.Valid {
		inside := row.CancelledInsideNotice.Bool
		occurrence.CancelledInsideNotice = &inside
	}

	return occurrence
}

func closureFromRow(row *generated.ScheduleClosures) *Closure {
	if row == nil {
		return nil
	}

	return &Closure{
		CreatedAt:  row.CreatedAt,
		ArchivedAt: database.TimePointerFromNullTime(row.ArchivedAt),
		ID:         row.ID,
		StudioID:   row.StudioID,
		Reason:     row.Reason,
		StartsOn:   DateOf(row.StartsOn),
		EndsOn:     DateOf(row.EndsOn),
	}
}

func nullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func nullInt32(value int) sql.NullInt32 {
	return sql.NullInt32{Int32: int32(value), Valid: value != 0}
}

func nullInt64(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}

	return sql.NullInt64{Int64: *value, Valid: true}
}

func nullDate(value Date) sql.NullTime {
	if value.IsZero() {
		return sql.NullTime{}
	}

	return sql.NullTime{Time: value.Time(), Valid: true}
}

// dedupe returns the non-empty members in the order they were given, once each.
func dedupe(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}

	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))

	for _, value := range values {
		if value == "" {
			continue
		}

		if _, already := seen[value]; already {
			continue
		}

		seen[value] = struct{}{}

		out = append(out, value)
	}

	return out
}
