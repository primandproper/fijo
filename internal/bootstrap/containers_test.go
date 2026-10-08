package bootstrap_test

import (
	"context"
	"testing"

	"github.com/primandproper/fijo/internal/authz/roles"
	"github.com/primandproper/fijo/internal/bootstrap"
	"github.com/primandproper/fijo/internal/testutils/pgclient"
	"github.com/primandproper/fijo/internal/testutils/seed"

	"github.com/primandproper/platform-go/v15/identity"
	"github.com/primandproper/primitives-go/v2/tenancy"
	"github.com/primandproper/primitives-go/v2/testutils/containers/pgtest"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestSeederAgainstPostgres(t *testing.T) {
	t.Parallel()

	pgtest.Run(t, func(ctx context.Context, pg *pgtest.Instance) {
		client := pgclient.Open(t, ctx, pg)
		seeder := seed.New(t, client)

		directory, err := identity.NewSQLStore(client)
		must.NoError(t, err)

		scope := tenancy.Global()

		t.Run("provisions a studio and its owner", func(t *testing.T) {
			registered := seed.Studio(t, ctx, seeder, &bootstrap.Studio{
				Username:     "maria",
				EmailAddress: "Maria@Example.com",
				Password:     "a good long password",
				FirstName:    "Maria",
				Name:         "Casa de Musica",
				TimeZone:     "US/Central",
			})

			must.NotNil(t, registered.User)
			must.NotNil(t, registered.Account)
			test.Eq(t, "maria", registered.User.Username)
			test.Eq(t, "maria@example.com", registered.User.EmailAddress)
			test.Eq(t, "Casa de Musica", registered.Account.Name)
			test.Eq(t, "US/Central", registered.Account.TimeZone)

			// The owner holds the studio through a membership, and holds no
			// service role: running a studio is not operating the deployment.
			read, readErr := directory.GetUserByUsername(ctx, client.Reader(), scope, "maria")
			must.NoError(t, readErr)
			test.Eq(t, registered.User.ID, read.ID)
			test.SliceEmpty(t, read.ServiceRoles)

			membership, membershipErr := directory.GetMembership(ctx, client.Reader(), scope, registered.User.ID, registered.Account.ID)
			must.NoError(t, membershipErr)
			test.SliceContains(t, membership.Roles, roles.Owner)
		})

		t.Run("refuses a second studio for a username already taken", func(t *testing.T) {
			_, refusal := seeder.ProvisionStudio(ctx, &bootstrap.Studio{
				Username:     "maria",
				EmailAddress: "someone@example.com",
				Password:     "a good long password",
				Name:         "Another Studio",
			})

			test.Error(t, refusal)
		})

		t.Run("makes the operator and is safe to run again", func(t *testing.T) {
			admin := &bootstrap.Admin{
				Username:     "jeffrey",
				EmailAddress: "jeffrey@example.com",
				Password:     "a good long password",
			}

			first, firstErr := seeder.EnsureServiceAdmin(ctx, admin)
			must.NoError(t, firstErr)
			test.SliceContains(t, first.ServiceRoles, roles.ServiceAdmin)

			second, secondErr := seeder.EnsureServiceAdmin(ctx, admin)
			must.NoError(t, secondErr)
			test.Eq(t, first.ID, second.ID)
			test.SliceContains(t, second.ServiceRoles, roles.ServiceAdmin)

			read, readErr := directory.GetUserByUsername(ctx, client.Reader(), scope, "jeffrey")
			must.NoError(t, readErr)
			test.SliceContains(t, read.ServiceRoles, roles.ServiceAdmin)
		})

		t.Run("promotes an owner who was registered as one", func(t *testing.T) {
			registered := seed.Studio(t, ctx, seeder, &bootstrap.Studio{
				Username:     "second",
				EmailAddress: "second@example.com",
				Password:     "a good long password",
				Name:         "Second Studio",
			})

			test.SliceEmpty(t, registered.User.ServiceRoles)

			promoted, promoteErr := seeder.EnsureServiceAdmin(ctx, &bootstrap.Admin{Username: "second"})
			must.NoError(t, promoteErr)
			test.Eq(t, registered.User.ID, promoted.ID)
			test.SliceContains(t, promoted.ServiceRoles, roles.ServiceAdmin)
		})
	})
}
