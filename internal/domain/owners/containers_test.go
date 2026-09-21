package owners

import (
	"context"
	"testing"

	"github.com/primandproper/fijo/internal/testutils/pgclient"

	"github.com/primandproper/platform-go/v14/identity"
	"github.com/primandproper/primitives-go/v2/database"
	"github.com/primandproper/primitives-go/v2/identifiers"
	"github.com/primandproper/primitives-go/v2/tenancy"
	"github.com/primandproper/primitives-go/v2/testutils/containers/pgtest"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestSQLStoreAgainstPostgres(t *testing.T) {
	t.Parallel()

	pgtest.Run(t, func(ctx context.Context, pg *pgtest.Instance) {
		client := pgclient.Open(t, ctx, pg)

		directory, err := identity.NewSQLStore(client)
		must.NoError(t, err)

		store, err := NewSQLStore()
		must.NoError(t, err)

		scope := tenancy.Global()

		var user *identity.User

		must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
			var createErr error

			user, createErr = directory.CreateUser(ctx, tx, scope, &identity.User{
				ID:            identifiers.New(),
				Username:      "owner",
				EmailAddress:  "owner@example.com",
				AccountStatus: identity.StatusGood,
			})

			return createErr
		}))

		_, err = store.GetPreferences(ctx, client.Reader(), scope, user.ID)
		must.ErrorIs(t, err, ErrPreferencesNotFound)

		must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
			written, setErr := store.SetTimeZone(ctx, tx, scope, user.ID, "US/Central")
			if setErr != nil {
				return setErr
			}

			test.Eq(t, "US/Central", written.TimeZone)
			test.Nil(t, written.LastUpdatedAt)

			return nil
		}))

		must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
			written, setErr := store.SetTimeZone(ctx, tx, scope, user.ID, "America/New_York")
			if setErr != nil {
				return setErr
			}

			test.Eq(t, "America/New_York", written.TimeZone)
			test.NotNil(t, written.LastUpdatedAt)

			return nil
		}))

		read, err := store.GetPreferences(ctx, client.Reader(), scope, user.ID)
		must.NoError(t, err)
		test.Eq(t, "America/New_York", read.TimeZone)

		must.Error(t, client.WithTransaction(ctx, func(tx database.Tx) error {
			_, setErr := store.SetTimeZone(ctx, tx, scope, user.ID, "Local")

			return setErr
		}))

		// A row for a user the directory does not hold is refused by the key.
		must.Error(t, client.WithTransaction(ctx, func(tx database.Tx) error {
			_, setErr := store.SetTimeZone(ctx, tx, scope, "nobody", "UTC")

			return setErr
		}))
	})
}
