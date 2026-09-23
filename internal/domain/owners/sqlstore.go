package owners

import (
	"context"
	"database/sql"
	"errors"

	"github.com/primandproper/fijo/internal/domain/owners/generated"

	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/observability/metrics"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

const storeName = "owners_store"

// SQLStore is the Postgres implementation of Store.
//
// It takes no database.Client: its writes are handed a Tx and its reads an
// executor, it emits no dialect-dependent SQL, and the database stamps the
// rows. That is the shape the platform's convention says a store with none of
// those obligations has.
type SQLStore struct {
	o11y        observability.Observer
	instruments *metrics.OperationSet

	// querier is sqlc's, and is stateless: every method takes the executor it
	// should run on, which is how a write is handed a Tx and a read a reader.
	querier generated.Querier
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
		return nil, platformerrors.Wrap(err, "creating owners store instruments")
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

// GetPreferences implements Store.
func (s *SQLStore) GetPreferences(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, userID string) (_ *Preferences, err error) {
	ctx, op := s.o11y.Begin(ctx, observability.WithValue("identity.user_id", userID))
	defer op.End()

	s.instruments.Attempt(ctx)
	defer op.Time(ctx, nil, s.instruments.Latency)()

	defer func() {
		if err != nil {
			s.instruments.Failed(ctx)
		}
	}()

	if err = checkRead(q, scope, userID); err != nil {
		return nil, op.Error(err, "reading preferences")
	}

	row, err := s.querier.GetUserPreferences(ctx, q, &generated.GetUserPreferencesParams{
		UserID: userID,
		Scope:  scope.String(),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, op.Error(ErrPreferencesNotFound, "reading preferences")
		}

		return nil, op.Error(err, "reading preferences")
	}

	return rowToPreferences(row), nil
}

// SetTimeZone implements Store.
func (s *SQLStore) SetTimeZone(ctx context.Context, tx database.Tx, scope tenancy.Scope, userID, timeZone string) (_ *Preferences, err error) {
	ctx, op := s.o11y.Begin(ctx, observability.WithValue("identity.user_id", userID))
	defer op.End()

	s.instruments.Attempt(ctx)
	defer op.Time(ctx, nil, s.instruments.Latency)()

	defer func() {
		if err != nil {
			s.instruments.Failed(ctx)
		}
	}()

	if tx == nil {
		return nil, op.Error(ErrNilExecutor, "setting a time zone")
	}

	if err = checkRead(tx, scope, userID); err != nil {
		return nil, op.Error(err, "setting a time zone")
	}

	if _, err = LoadTimeZone(timeZone); err != nil {
		return nil, op.Error(err, "setting a time zone")
	}

	row, err := s.querier.UpsertUserTimeZone(ctx, tx, &generated.UpsertUserTimeZoneParams{
		UserID:   userID,
		Scope:    scope.String(),
		TimeZone: timeZone,
	})
	if err != nil {
		// The upsert's WHERE refuses to move a row to another scope, and the
		// database reports that as no row returned.
		if errors.Is(err, sql.ErrNoRows) {
			return nil, op.Error(platformerrors.Wrap(ErrPreferencesNotFound, "the row belongs to another scope"), "setting a time zone")
		}

		return nil, op.Error(err, "setting a time zone")
	}

	return rowToPreferences(row), nil
}

func checkRead(q database.SQLQueryExecutor, scope tenancy.Scope, userID string) error {
	switch {
	case q == nil:
		return ErrNilExecutor
	case userID == "":
		return ErrEmptyUserID
	}

	return scope.Validate()
}

// rowToPreferences turns the generated row into the domain's value.
func rowToPreferences(row *generated.UserPreferences) *Preferences {
	if row == nil {
		return nil
	}

	return &Preferences{
		CreatedAt:     row.CreatedAt,
		LastUpdatedAt: database.TimePointerFromNullTime(row.LastUpdatedAt),
		UserID:        row.UserID,
		TimeZone:      row.TimeZone,
		Scope:         tenancy.Of(row.Scope),
	}
}
