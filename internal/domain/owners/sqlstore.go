package owners

import (
	"context"
	"database/sql"
	"errors"

	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/observability/metrics"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

const storeName = "owners_store"

// The statements. Hand-written for one table with two queries; the domains
// that arrive in phase 1 bring the sqlc toolchain DDB uses, and this table
// joins it then.
const (
	getPreferences = `SELECT user_id, scope, time_zone, created_at, last_updated_at
FROM user_preferences
WHERE user_id = $1 AND scope = $2`

	upsertTimeZone = `INSERT INTO user_preferences (user_id, scope, time_zone)
VALUES ($1, $2, $3)
ON CONFLICT (user_id) DO UPDATE
	SET time_zone = EXCLUDED.time_zone, last_updated_at = NOW()
	WHERE user_preferences.scope = EXCLUDED.scope
RETURNING user_id, scope, time_zone, created_at, last_updated_at`
)

// SQLStore is the Postgres implementation of Store.
//
// It takes no database.Client: its writes are handed a Tx and its reads an
// executor, it emits no dialect-dependent SQL, and the database stamps the
// rows. That is the shape the platform's convention says a store with none of
// those obligations has.
type SQLStore struct {
	o11y        observability.Observer
	instruments *metrics.OperationSet
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
	}

	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}

	return s, nil
}

// GetPreferences implements Store.
func (s *SQLStore) GetPreferences(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, userID string) (prefs *Preferences, err error) {
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

	prefs, err = scan(q.QueryRowContext(ctx, getPreferences, userID, scope))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, op.Error(ErrPreferencesNotFound, "reading preferences")
		}

		return nil, op.Error(err, "reading preferences")
	}

	return prefs, nil
}

// SetTimeZone implements Store.
func (s *SQLStore) SetTimeZone(ctx context.Context, tx database.Tx, scope tenancy.Scope, userID, timeZone string) (prefs *Preferences, err error) {
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

	prefs, err = scan(tx.QueryRowContext(ctx, upsertTimeZone, userID, scope, timeZone))
	if err != nil {
		// The upsert's WHERE refuses to move a row to another scope, and the
		// database reports that as no row returned.
		if errors.Is(err, sql.ErrNoRows) {
			return nil, op.Error(platformerrors.Wrap(ErrPreferencesNotFound, "the row belongs to another scope"), "setting a time zone")
		}

		return nil, op.Error(err, "setting a time zone")
	}

	return prefs, nil
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

func scan(row *sql.Row) (*Preferences, error) {
	var (
		p       Preferences
		scope   string
		updated sql.NullTime
	)

	if err := row.Scan(&p.UserID, &scope, &p.TimeZone, &p.CreatedAt, &updated); err != nil {
		return nil, err
	}

	p.Scope = tenancy.Of(scope)

	if updated.Valid {
		t := updated.Time
		p.LastUpdatedAt = &t
	}

	return &p, nil
}
