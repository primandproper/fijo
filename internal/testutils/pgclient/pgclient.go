// Package pgclient opens a primitives-go database client against a pgtest
// instance and brings it to Fijo's schema. It is the one place a container
// test in this repository turns a connection string into a migrated client,
// so every such test runs against the same sequence production does.
package pgclient

import (
	"context"
	"testing"

	"github.com/primandproper/fijo/internal/database/migrations"

	"github.com/primandproper/primitives-go/v2/database"
	databasecfg "github.com/primandproper/primitives-go/v2/database/config"
	"github.com/primandproper/primitives-go/v2/database/postgres"
	"github.com/primandproper/primitives-go/v2/testutils/containers/pgtest"

	"github.com/shoenig/test/must"
)

// Open migrates pg and returns a client on it. The client is closed when the
// test ends.
func Open(tb testing.TB, ctx context.Context, pg *pgtest.Instance) *postgres.Client {
	tb.Helper()

	migrator, err := migrations.NewMigrator(nil)
	must.NoError(tb, err)
	must.NoError(tb, migrator.Migrate(ctx, pg.DB))

	cfg := &databasecfg.Config{Provider: databasecfg.ProviderPostgres}
	must.NoError(tb, cfg.ReadConnection.LoadFromURL(pg.ConnectionString))
	must.NoError(tb, cfg.WriteConnection.LoadFromURL(pg.ConnectionString))
	cfg.EnsureDefaults()

	client, err := postgres.NewDatabaseClient(ctx, cfg)
	must.NoError(tb, err)

	tb.Cleanup(func() { must.NoError(tb, client.Close()) })

	var _ database.Client = client

	return client
}
