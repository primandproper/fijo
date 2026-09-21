package cli

import (
	"context"
	"fmt"

	"github.com/primandproper/fijo/internal/database/migrations"

	"github.com/primandproper/primitives-go/v2/database"
	"github.com/primandproper/primitives-go/v2/database/postgres"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"

	"github.com/spf13/cobra"
)

// The refusals a workload makes before it composes anything.
var (
	errNoConfig   = platformerrors.New("no configuration was loaded")
	errNoDatabase = platformerrors.New("the configuration names no database; set the database block or FIJO_DATABASE_* to migrate")
)

// newMigrateCommand returns the `migrate` subcommand, which brings the
// configured database to the current schema and seeds the authorization
// policy, then exits. It reads the same config as `serve` — the file named by
// --config or the FIJO_DATABASE_* variables — so a deployment migrates with the
// credentials it serves with rather than a second set typed into flags.
//
// `serve` runs the same migrator on boot when the database block sets
// runMigrations, which is how localdev works. Production leaves that off and
// runs this once per release, ahead of the rollout.
func (a *application) newMigrateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply pending database migrations and seed the authorization policy.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runMigrate(cmd.Context())
		},
	}
}

func (a *application) runMigrate(ctx context.Context) error {
	if a.cfg == nil || a.cfg.Database == nil {
		return errNoDatabase
	}

	logger := a.log()

	// The migrator is built before the client so a schema that does not render
	// is reported without a connection having been opened for it.
	migrator, err := migrations.NewMigrator(logger)
	if err != nil {
		return fmt.Errorf("building migrator: %w", err)
	}

	client, err := postgres.NewDatabaseClient(ctx, a.cfg.Database,
		postgres.WithLogger(logger),
		postgres.WithTracerProvider(a.pillars.TracerProvider),
	)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}

	defer func() {
		if closeErr := client.Close(); closeErr != nil {
			logger.Error("closing database client", closeErr)
		}
	}()

	// Migrations need the concrete *sql.DB, which is the RawAccess capability
	// rather than the Client surface. The provider returns its own concrete
	// type, so this is a static conversion the compiler checks.
	var raw database.RawAccess = client

	if err = migrator.Migrate(ctx, raw.WriteDB()); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	logger.Info("migrations applied and authorization policy seeded")

	return nil
}
