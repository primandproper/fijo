// Package seed opens an internal/bootstrap Seeder over a migrated test
// database, so a container test that needs a studio before it can begin gets
// one the same way a deployment does.
//
// A studio is an identity account and Fijo's tenant, so nearly everything
// phase 1 onward stores is scoped to one and cannot be written until one
// exists. A test cannot ask the API for it — registering is an authorized
// call and the test has no authorized caller — which is the same wall
// cmd/tools/bootstrap climbs, and this is the same ladder.
//
// The registrar is a real *signin.Service, so a seeded owner holds a real
// Argon2 digest and can sign in. Only the token issuer is a noop: registering
// mints no token, and a test that wants one builds its own issuer.
package seed

import (
	"context"
	"testing"

	"github.com/primandproper/fijo/internal/bootstrap"

	"github.com/primandproper/platform-go/v14/authentication/signin"
	"github.com/primandproper/platform-go/v14/identity"
	"github.com/primandproper/primitives-go/v2/authentication/argon2"
	"github.com/primandproper/primitives-go/v2/authentication/tokens"
	"github.com/primandproper/primitives-go/v2/database"

	"github.com/shoenig/test/must"
)

// New builds a Seeder over a migrated client, as internal/testutils/pgclient
// returns one.
func New(tb testing.TB, client database.Client) *bootstrap.Seeder {
	tb.Helper()

	store, err := identity.NewSQLStore(client)
	must.NoError(tb, err)

	directory, err := identity.NewService(client, store)
	must.NoError(tb, err)

	registrar, err := signin.NewService(client, store, argon2.NewArgon2Authenticator(), tokens.NewNoopTokenIssuer(),
		signin.WithRegistrar(directory),
	)
	must.NoError(tb, err)

	seeder, err := bootstrap.New(registrar, directory, store, client.Reader())
	must.NoError(tb, err)

	return seeder
}

// Studio provisions a studio and its owner and returns what was registered,
// failing the test rather than handing back an error.
func Studio(tb testing.TB, ctx context.Context, seeder *bootstrap.Seeder, studio *bootstrap.Studio) *signin.Registered {
	tb.Helper()

	registered, err := seeder.ProvisionStudio(ctx, studio)
	must.NoError(tb, err)

	return registered
}
