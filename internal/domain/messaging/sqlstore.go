package messaging

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/primandproper/fijo/internal/domain/messaging/generated"
	"github.com/primandproper/fijo/internal/domain/roster"

	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/observability/metrics"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

const storeName = "messaging_store"

// The span and log keys this store attaches.
const (
	keyStudio    = "fijo.studio_id"
	keyReminder  = "fijo.reminder_id"
	keyHousehold = "fijo.household_id"
)

// SQLStore is the Postgres implementation of Store.
type SQLStore struct {
	o11y        observability.Observer
	instruments *metrics.OperationSet
	querier     generated.Querier
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
		return nil, platformerrors.Wrap(err, "creating messaging store instruments")
	}

	s := &SQLStore{
		o11y:        observability.NewObserver(storeName, nil, nil),
		instruments: instruments,
		querier:     generated.New(),
	}

	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}

	return s, nil
}

// observe opens a span and counts the attempt; the returned func, deferred
// with the method's named error, counts a failure.
func (s *SQLStore) observe(ctx context.Context) (context.Context, observability.Operation, func(*error)) {
	ctx, op := s.o11y.Begin(ctx)
	s.instruments.Attempt(ctx)
	stop := op.Time(ctx, nil, s.instruments.Latency)

	return ctx, op, func(err *error) {
		stop()

		if err != nil && *err != nil {
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

	if q == nil {
		return nil, op.Error(ErrNilExecutor, "reading reminder settings")
	}

	studio, err := studioOf(scope)
	if err != nil {
		return nil, op.Error(err, "reading reminder settings")
	}

	op.Set(keyStudio, studio)

	row, err := s.querier.GetStudioSettings(ctx, q, studio)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &Settings{StudioID: studio}, nil
		}

		return nil, op.Error(err, "reading reminder settings")
	}

	return settingsFromRow(row), nil
}

// SaveSettings implements Store.
func (s *SQLStore) SaveSettings(ctx context.Context, tx database.Tx, scope tenancy.Scope, settings *Settings) (_ *Settings, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	if tx == nil {
		return nil, op.Error(ErrNilExecutor, "saving reminder settings")
	}

	studio, err := studioOf(scope)
	if err != nil {
		return nil, op.Error(err, "saving reminder settings")
	}

	op.Set(keyStudio, studio)

	if settings == nil {
		return nil, op.Error(ErrNilInput, "saving reminder settings")
	}

	staged := *settings
	staged.StudioID = studio
	staged.ReplyTo = strings.TrimSpace(staged.ReplyTo)

	if err = staged.Validate(); err != nil {
		return nil, op.Error(err, "saving reminder settings")
	}

	if staged.ReplyTo != "" {
		if staged.ReplyTo, err = roster.NormalizeEmail(staged.ReplyTo); err != nil {
			return nil, op.Error(err, "saving reminder settings")
		}
	}

	row, err := s.querier.SaveStudioSettings(ctx, tx, &generated.SaveStudioSettingsParams{
		StudioID:    studio,
		RemindersOn: staged.RemindersOn,
		ReplyTo:     staged.ReplyTo,
	})
	if err != nil {
		return nil, op.Error(err, "saving reminder settings")
	}

	return settingsFromRow(row), nil
}

// Candidates implements Store.
func (s *SQLStore) Candidates(ctx context.Context, q database.SQLQueryExecutor, now, horizon time.Time) (_ []*Candidate, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	if q == nil {
		return nil, op.Error(ErrNilExecutor, "reading reminder candidates")
	}

	rows, err := s.querier.ReminderCandidates(ctx, q, &generated.ReminderCandidatesParams{Now: now, Horizon: horizon})
	if err != nil {
		return nil, op.Error(err, "reading reminder candidates")
	}

	out := make([]*Candidate, 0, len(rows))

	for _, row := range rows {
		var others []string
		if row.OtherAttendees != "" {
			others = strings.Split(row.OtherAttendees, "\n")
		}

		out = append(out, &Candidate{
			StartsAt:            row.StartsAt,
			OccurrenceCreatedAt: row.OccurrenceCreatedAt,
			RemindersOnSince:    row.RemindersOnSince.Time,
			OccurrenceID:        row.OccurrenceID,
			StudioID:            row.StudioID,
			OfferingNameEN:      row.OfferingNameEn,
			OfferingNameES:      row.OfferingNameEs,
			HouseholdID:         row.HouseholdID,
			ContactID:           row.ContactID,
			ContactName:         row.ContactName,
			ContactEmail:        row.ContactEmail.String,
			ContactLang:         roster.Language(row.ContactLanguage),
			ReplyTo:             row.ReplyTo,
			OtherAttendees:      others,
			FirstReminder:       minutes(row.FirstReminderMinutes),
			SecondReminder:      minutes(row.SecondReminderMinutes),
			SmallestWritten:     time.Duration(row.SmallestWritten) * time.Minute,
			ContactAttends:      row.ContactAttends,
		})
	}

	return out, nil
}

// WriteReminder implements Store.
func (s *SQLStore) WriteReminder(ctx context.Context, tx database.Tx, reminder *Reminder) (_ bool, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	switch {
	case tx == nil:
		return false, op.Error(ErrNilExecutor, "writing a reminder")
	case reminder == nil:
		return false, op.Error(ErrNilInput, "writing a reminder")
	case reminder.ID == "" || reminder.StudioID == "" || reminder.OccurrenceID == "" || reminder.ContactID == "":
		return false, op.Error(ErrEmptyID, "writing a reminder")
	}

	op.Set(keyStudio, reminder.StudioID).Set(keyReminder, reminder.ID)

	affected, err := s.querier.WriteReminder(ctx, tx, &generated.WriteReminderParams{
		ID:            reminder.ID,
		StudioID:      reminder.StudioID,
		OccurrenceID:  reminder.OccurrenceID,
		HouseholdID:   reminder.HouseholdID,
		ContactID:     reminder.ContactID,
		OffsetMinutes: int32(reminder.Offset / time.Minute),
		ToAddress:     reminder.ToAddress,
		ToName:        reminder.ToName,
		Language:      string(reminder.Language),
		Subject:       reminder.Subject,
		BodyHtml:      reminder.BodyHTML,
	})
	if err != nil {
		return false, op.Error(err, "writing a reminder")
	}

	return affected > 0, nil
}

// LockForDelivery implements Store.
func (s *SQLStore) LockForDelivery(ctx context.Context, tx database.Tx, id string) (_ *Delivery, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	switch {
	case tx == nil:
		return nil, op.Error(ErrNilExecutor, "locking a reminder")
	case id == "":
		return nil, op.Error(ErrEmptyID, "locking a reminder")
	}

	op.Set(keyReminder, id)

	row, err := s.querier.LockReminderForDelivery(ctx, tx, id)
	if err != nil {
		return nil, op.Error(notFound(err), "locking a reminder")
	}

	return &Delivery{
		CreatedAt:          row.CreatedAt,
		SentAt:             database.TimePointerFromNullTime(row.SentAt),
		ID:                 row.ID,
		StudioID:           row.StudioID,
		OccurrenceID:       row.OccurrenceID,
		HouseholdID:        row.HouseholdID,
		ContactID:          row.ContactID,
		ToAddress:          row.ToAddress,
		ToName:             row.ToName,
		Language:           roster.Language(row.Language),
		Subject:            row.Subject,
		BodyHTML:           row.BodyHtml,
		Status:             Status(row.Status),
		Reason:             row.Reason,
		Offset:             time.Duration(row.OffsetMinutes) * time.Minute,
		Attempts:           int(row.Attempts),
		OccurrenceStartsAt: row.OccurrenceStartsAt,
		OccurrenceState:    row.OccurrenceState,
		ReplyTo:            row.ReplyTo,
		ContactWantsIt:     row.ContactWantsIt,
		RemindersOn:        row.RemindersOn,
	}, nil
}

// MarkSent implements Store.
func (s *SQLStore) MarkSent(ctx context.Context, tx database.Tx, id string) (err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	switch {
	case tx == nil:
		return op.Error(ErrNilExecutor, "marking a reminder sent")
	case id == "":
		return op.Error(ErrEmptyID, "marking a reminder sent")
	}

	op.Set(keyReminder, id)

	if err = s.querier.MarkReminderSent(ctx, tx, id); err != nil {
		return op.Error(err, "marking a reminder sent")
	}

	return nil
}

// MarkUnsent implements Store.
func (s *SQLStore) MarkUnsent(ctx context.Context, tx database.Tx, id string, status Status, reason string) (err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	switch {
	case tx == nil:
		return op.Error(ErrNilExecutor, "retiring a reminder")
	case id == "":
		return op.Error(ErrEmptyID, "retiring a reminder")
	case status != Skipped && status != Failed:
		return op.Error(platformerrors.Wrapf(platformerrors.ErrUnrecognizedInputValue, "a reminder is retired as skipped or failed, not %q", status), "retiring a reminder")
	}

	op.Set(keyReminder, id)

	if err = s.querier.MarkReminderUnsent(ctx, tx, &generated.MarkReminderUnsentParams{ID: id, Status: string(status), Reason: reason}); err != nil {
		return op.Error(err, "retiring a reminder")
	}

	return nil
}

// RecordAttempt implements Store.
func (s *SQLStore) RecordAttempt(ctx context.Context, tx database.Tx, id, reason string) (_ int, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	switch {
	case tx == nil:
		return 0, op.Error(ErrNilExecutor, "recording a reminder attempt")
	case id == "":
		return 0, op.Error(ErrEmptyID, "recording a reminder attempt")
	}

	op.Set(keyReminder, id)

	attempts, err := s.querier.RecordReminderAttempt(ctx, tx, &generated.RecordReminderAttemptParams{ID: id, Reason: reason})
	if err != nil {
		return 0, op.Error(notFound(err), "recording a reminder attempt")
	}

	return int(attempts), nil
}

// ListReminders implements Store.
func (s *SQLStore) ListReminders(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, householdID string, limit int) (_ []*Reminder, err error) {
	ctx, op, done := s.observe(ctx)
	defer done(&err)

	if q == nil {
		return nil, op.Error(ErrNilExecutor, "listing reminders")
	}

	studio, err := studioOf(scope)
	if err != nil {
		return nil, op.Error(err, "listing reminders")
	}

	op.Set(keyStudio, studio).Set(keyHousehold, householdID)

	if limit <= 0 || limit > 500 {
		limit = 100
	}

	rows, err := s.querier.ListReminders(ctx, q, &generated.ListRemindersParams{
		StudioID:    studio,
		HouseholdID: householdID,
		RowLimit:    int32(limit),
	})
	if err != nil {
		return nil, op.Error(err, "listing reminders")
	}

	out := make([]*Reminder, 0, len(rows))

	for _, row := range rows {
		out = append(out, &Reminder{
			CreatedAt:      row.CreatedAt,
			SentAt:         database.TimePointerFromNullTime(row.SentAt),
			LessonStartsAt: row.OccurrenceStartsAt,
			ID:             row.ID,
			StudioID:       row.StudioID,
			OccurrenceID:   row.OccurrenceID,
			HouseholdID:    row.HouseholdID,
			ContactID:      row.ContactID,
			ToAddress:      row.ToAddress,
			ToName:         row.ToName,
			Language:       roster.Language(row.Language),
			Subject:        row.Subject,
			BodyHTML:       row.BodyHtml,
			Status:         Status(row.Status),
			Reason:         row.Reason,
			ContactName:    row.ContactName,
			HouseholdName:  row.HouseholdName,
			Offset:         time.Duration(row.OffsetMinutes) * time.Minute,
			Attempts:       int(row.Attempts),
		})
	}

	return out, nil
}

func settingsFromRow(row *generated.MessagingStudios) *Settings {
	return &Settings{
		RemindersOnSince: database.TimePointerFromNullTime(row.RemindersOnSince),
		StudioID:         row.StudioID,
		ReplyTo:          row.ReplyTo,
		RemindersOn:      row.RemindersOn,
	}
}

// minutes is a stored reminder time, NULL being none.
func minutes(value sql.NullInt32) time.Duration {
	if !value.Valid {
		return 0
	}

	return time.Duration(value.Int32) * time.Minute
}
