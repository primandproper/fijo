// Package worker is the composition root of `fijo worker`.
//
// A worker is the same platform registration as the API with the servers left
// out, plus the loops. Today the loops are the sweepers over the expiring
// credential tables — refresh tokens and reset tokens — which the platform
// stores expose but do not schedule, because one sweep for the fleet is a
// deployment's decision and not a store's. The session and WebAuthn ceremony
// stores sweep themselves from their own configs, and phase 1's materializer
// turns every studio's standing slots into rows out to its horizon. Phase 3's
// reminder sender joins the runners here.
package worker

import (
	"context"

	"github.com/primandproper/fijo/internal/config"
	"github.com/primandproper/fijo/internal/database/migrations"
	"github.com/primandproper/fijo/internal/domain/schedule"
	"github.com/primandproper/fijo/internal/workers/materialize"
	"github.com/primandproper/fijo/internal/workers/sweep"

	"github.com/primandproper/platform-go/v15/authentication/passwordreset"
	"github.com/primandproper/platform-go/v15/authentication/signin/refreshtokens"
	"github.com/primandproper/platform-go/v15/service"
	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/observability"

	"github.com/samber/do/v2"
)

// BuildInjector registers everything `fijo worker` is made of.
func BuildInjector(ctx context.Context, cfg *config.Config) (*do.RootScope, error) {
	if cfg == nil {
		return nil, platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil config")
	}

	if err := cfg.ValidateWithContext(ctx); err != nil {
		return nil, err
	}

	if cfg.Database == nil {
		return nil, platformerrors.New("the worker requires the database block")
	}

	// The worker serves nothing, so the servers are left out of the platform
	// registration. A copy rather than an edit, because the config is the
	// caller's and `serve` reads the same one.
	platform := cfg.Config
	platform.GRPCServer = nil
	platform.HTTPServer = nil

	i := do.New()

	do.ProvideValue(i, ctx)
	do.ProvideValue(i, cfg)

	do.Provide(i, func(i do.Injector) (database.Migrator, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		return migrations.NewMigrator(pillars.Logger)
	})

	service.Register(i, &platform)

	do.Provide(i, func(i do.Injector) (*refreshtokens.SQLStore, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		client, err := do.Invoke[database.Client](i)
		if err != nil {
			return nil, err
		}

		return refreshtokens.NewSQLStore(&refreshtokens.Config{}, client, refreshtokens.WithLogger(pillars.Logger), refreshtokens.WithTracerProvider(pillars.TracerProvider), refreshtokens.WithMetricsProvider(pillars.MetricsProvider))
	})

	do.Provide(i, func(i do.Injector) (*passwordreset.SQLStore, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		client, err := do.Invoke[database.Client](i)
		if err != nil {
			return nil, err
		}

		return passwordreset.NewSQLStore(&passwordreset.Config{}, client, passwordreset.WithLogger(pillars.Logger), passwordreset.WithTracerProvider(pillars.TracerProvider), passwordreset.WithMetricsProvider(pillars.MetricsProvider))
	})

	do.Provide(i, func(i do.Injector) (schedule.Store, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		return schedule.NewSQLStore(schedule.WithPillars(pillars))
	})

	materialize.Register(i)

	return i, nil
}

// NewService builds the injector and the service that runs the loops.
func NewService(ctx context.Context, cfg *config.Config) (*service.Service, error) {
	i, err := BuildInjector(ctx, cfg)
	if err != nil {
		return nil, err
	}

	pillars, err := observability.InvokePillars(i)
	if err != nil {
		return nil, err
	}

	refresh, err := do.Invoke[*refreshtokens.SQLStore](i)
	if err != nil {
		return nil, platformerrors.Wrap(err, "building the refresh token store")
	}

	reset, err := do.Invoke[*passwordreset.SQLStore](i)
	if err != nil {
		return nil, platformerrors.Wrap(err, "building the password reset store")
	}

	interval := cfg.Auth.CredentialSweepInterval

	refreshSweeper, err := sweep.New("refresh_tokens", interval, refresh.Sweep, pillars)
	if err != nil {
		return nil, err
	}

	resetSweeper, err := sweep.New("password_reset_tokens", interval, reset.Sweep, pillars)
	if err != nil {
		return nil, err
	}

	materializer, err := do.Invoke[*schedule.Materializer](i)
	if err != nil {
		return nil, platformerrors.Wrap(err, "building the schedule materializer")
	}

	grid, err := materialize.New(cfg.Schedule.MaterializeInterval, materializer.RunOnce, pillars)
	if err != nil {
		return nil, err
	}

	return service.New(i, service.WithRunners(refreshSweeper, resetSweeper, grid))
}
