package migrations

import (
	"context"
	"testing"

	"github.com/primandproper/fijo/internal/authz/roles"

	"github.com/primandproper/platform-go/v15/rbac"
	"github.com/primandproper/primitives-go/v2/database/dialect"
	"github.com/primandproper/primitives-go/v2/testutils/containers/pgtest"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// TestMigrateAgainstPostgres runs the whole sequence against a real Postgres
// and then reads the policy back. It gates on RUN_CONTAINER_TESTS like every
// container test on this stack.
func TestMigrateAgainstPostgres(t *testing.T) {
	t.Parallel()

	pgtest.Run(t, func(ctx context.Context, pg *pgtest.Instance) {
		migrator, err := NewMigrator(nil)
		must.NoError(t, err)

		must.NoError(t, migrator.Migrate(ctx, pg.DB))

		// Idempotent: a second run is what every replica after the first does.
		must.NoError(t, migrator.Migrate(ctx, pg.DB))

		for _, table := range []string{
			"identity_users", "identity_accounts", "authz_roles", "audit_log_entries",
			"sessions", "webauthn_sessions", "webauthn_credentials",
			"password_reset_tokens", "signin_refresh_tokens", "user_preferences",
		} {
			var exists bool
			must.NoError(t, pg.DB.QueryRowContext(ctx,
				"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)", table,
			).Scan(&exists))
			test.True(t, exists, test.Sprintf("table %s", table))
		}

		resolver, err := rbac.NewResolver(&rbac.Config{Dialect: dialect.Postgres}, pg.DB)
		must.NoError(t, err)

		seeded, err := resolver.Roles(ctx)
		must.NoError(t, err)
		test.SliceLen(t, len(roles.Roles()), seeded)

		owner, err := resolver.PermissionsForRoles(ctx, roles.Owner)
		must.NoError(t, err)
		test.False(t, owner.IsEmpty())
	})
}
