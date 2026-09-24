// Package api is the composition root of `fijo serve`.
//
// It follows DDB's shape — BuildInjector registers everything with a samber/do
// injector and NewServer resolves what the process runs — with one difference
// that is the point of building on platform-go v14: the platform half of the
// registration is one call. service.Register walks the platform config and
// registers every subsystem it names; service.RegisterTransports mounts the
// identity, sign-in and audit surfaces over the extractor Fijo hands it and
// puts Fijo's own services on the same server. What this file registers by
// hand is exactly what no environment variable can express: the hooks that
// record to the audit log, the session record behind a token, the sign-in
// service and the flows over it, the interceptors, and the policy each method
// requires.
package api

import (
	"context"

	"github.com/primandproper/fijo/internal/auth/auditing"
	"github.com/primandproper/fijo/internal/auth/interceptor"
	"github.com/primandproper/fijo/internal/auth/mint"
	"github.com/primandproper/fijo/internal/auth/passkeyflow"
	"github.com/primandproper/fijo/internal/auth/principal"
	"github.com/primandproper/fijo/internal/auth/resetmail"
	"github.com/primandproper/fijo/internal/auth/session"
	"github.com/primandproper/fijo/internal/authz/roles"
	"github.com/primandproper/fijo/internal/config"
	"github.com/primandproper/fijo/internal/database/migrations"
	"github.com/primandproper/fijo/internal/domain/owners"
	"github.com/primandproper/fijo/internal/domain/schedule"
	ownerauthgrpc "github.com/primandproper/fijo/internal/services/ownerauth/grpc"
	ownersgrpc "github.com/primandproper/fijo/internal/services/owners/grpc"
	schedulegrpc "github.com/primandproper/fijo/internal/services/schedule/grpc"
	"github.com/primandproper/fijo/internal/workers/materialize"

	"github.com/primandproper/platform-go/v14/audit"
	"github.com/primandproper/platform-go/v14/authentication/passkeys"
	"github.com/primandproper/platform-go/v14/authentication/passwordreset"
	"github.com/primandproper/platform-go/v14/authentication/signin"
	"github.com/primandproper/platform-go/v14/authentication/signin/refreshtokens"
	"github.com/primandproper/platform-go/v14/identity"
	identitycfg "github.com/primandproper/platform-go/v14/identity/config"
	"github.com/primandproper/platform-go/v14/service"
	"github.com/primandproper/platform-go/v14/sessions"
	sessionscfg "github.com/primandproper/platform-go/v14/sessions/config"
	"github.com/primandproper/primitives-go/v2/authentication"
	"github.com/primandproper/primitives-go/v2/authentication/argon2"
	"github.com/primandproper/primitives-go/v2/authentication/tokens"
	"github.com/primandproper/primitives-go/v2/authentication/webauthn"
	"github.com/primandproper/primitives-go/v2/authorization"
	"github.com/primandproper/primitives-go/v2/database"
	"github.com/primandproper/primitives-go/v2/email"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/observability"
	grpcserver "github.com/primandproper/primitives-go/v2/server/grpc"

	"github.com/samber/do/v2"
	"google.golang.org/grpc"
)

// BuildInjector registers everything `fijo serve` is made of.
func BuildInjector(ctx context.Context, cfg *config.Config) (*do.RootScope, error) {
	if cfg == nil {
		return nil, platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil config")
	}

	if err := cfg.ValidateForServing(ctx); err != nil {
		return nil, err
	}

	i := do.New()

	do.ProvideValue(i, ctx)
	do.ProvideValue(i, cfg)

	// The migrator the database client runs on boot when RunMigrations is set.
	// Registered before the platform so the client's provider finds it.
	registerMigrator(i)

	service.Register(i, &cfg.Config)

	registerAuth(i, cfg)
	registerDomains(i)
	registerServers(i)
	materialize.Register(i)
	registerScheduleServer(i)
	registerInterceptors(i)

	service.RegisterTransports(i, &service.Transports{
		Extractor: principal.Extract,
		Registrations: []grpcserver.RegistrationFunc{
			func(srv *grpc.Server) { do.MustInvoke[*ownerauthgrpc.Server](i).RegisterOn(srv) },
			func(srv *grpc.Server) { do.MustInvoke[*ownersgrpc.Server](i).RegisterOn(srv) },
			func(srv *grpc.Server) { do.MustInvoke[*schedulegrpc.Server](i).RegisterOn(srv) },
		},
	})

	return i, nil
}

// NewServer builds the injector and the service over it. Fijo's own servers
// are resolved here, ahead of service.New, so a misconfiguration in one of
// them is an error with a name rather than a panic inside a registration
// function.
func NewServer(ctx context.Context, cfg *config.Config) (*service.Service, error) {
	i, err := BuildInjector(ctx, cfg)
	if err != nil {
		return nil, err
	}

	if _, err = do.Invoke[*ownerauthgrpc.Server](i); err != nil {
		return nil, platformerrors.Wrap(err, "building the owner auth server")
	}

	if _, err = do.Invoke[*ownersgrpc.Server](i); err != nil {
		return nil, platformerrors.Wrap(err, "building the owners server")
	}

	return service.New(i)
}

func registerMigrator(i do.Injector) {
	do.Provide(i, func(i do.Injector) (database.Migrator, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		return migrations.NewMigrator(pillars.Logger)
	})
}

// registerAuth registers the sign-in layer: the recorder's hooks, the session
// store, the platform sign-in service over Fijo's directory, and the passkey,
// mint and reset flows beside it.
func registerAuth(i do.Injector, cfg *config.Config) {
	do.Provide(i, func(i do.Injector) (*auditing.Hooks, error) {
		recorder, err := do.Invoke[audit.Recorder](i)
		if err != nil {
			return nil, err
		}

		return auditing.New(recorder)
	})

	// The same hooks under both interfaces, so identity's writes and signin's
	// land in one log through one recorder.
	do.Provide(i, func(i do.Injector) (identity.Hooks, error) {
		return do.Invoke[*auditing.Hooks](i)
	})
	do.Provide(i, func(i do.Injector) (signin.Hooks, error) {
		return do.Invoke[*auditing.Hooks](i)
	})

	// The directory service over the store service.Register registered, with
	// the hooks above. It is what the identity surface mounts over and what
	// signin registers through.
	identitycfg.RegisterService(i)

	do.ProvideValue(i, cfg.Auth.Sessions.Platform())
	sessionscfg.RegisterStore[session.Data](i)

	do.Provide(i, func(i do.Injector) (signin.ClaimsBuilder, error) {
		store, err := do.Invoke[sessions.Store[session.Data]](i)
		if err != nil {
			return nil, err
		}

		return session.ClaimsBuilder(store)
	})

	do.Provide(i, func(_ do.Injector) (authentication.Authenticator, error) {
		return argon2.NewArgon2Authenticator(), nil
	})

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

	do.Provide(i, func(i do.Injector) (*signin.Service, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		client, err := do.Invoke[database.Client](i)
		if err != nil {
			return nil, err
		}

		directory, err := do.Invoke[identity.Store](i)
		if err != nil {
			return nil, err
		}

		authenticator, err := do.Invoke[authentication.Authenticator](i)
		if err != nil {
			return nil, err
		}

		issuer, err := do.Invoke[tokens.Issuer](i)
		if err != nil {
			return nil, err
		}

		refresh, err := do.Invoke[*refreshtokens.SQLStore](i)
		if err != nil {
			return nil, err
		}

		claims, err := do.Invoke[signin.ClaimsBuilder](i)
		if err != nil {
			return nil, err
		}

		hooks, err := do.Invoke[signin.Hooks](i)
		if err != nil {
			return nil, err
		}

		registrar, err := do.Invoke[*identity.Service](i)
		if err != nil {
			return nil, err
		}

		return signin.NewService(client, directory, authenticator, issuer,
			signin.WithPillars(pillars),
			signin.WithRefreshTokenStore(refresh),
			signin.WithClaimsBuilder(claims),
			signin.WithHooks(hooks),
			signin.WithRegistrar(registrar),
			signin.WithAdminServiceRoles(roles.ServiceAdmin),
			signin.WithTokenTTL(cfg.Auth.AccessTokenTTL),
			signin.WithRefreshTokenTTL(cfg.Auth.RefreshTokenTTL),
		)
	})

	do.Provide(i, func(i do.Injector) (passkeys.Store, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		client, err := do.Invoke[database.Client](i)
		if err != nil {
			return nil, err
		}

		store, err := passkeys.NewSQLStore(client, passkeys.WithPillars(pillars))
		if err != nil {
			return nil, err
		}

		return store, nil
	})

	do.Provide(i, func(i do.Injector) (*passkeyflow.Service, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		rp, err := do.Invoke[*webauthn.RelyingParty](i)
		if err != nil {
			return nil, err
		}

		credentials, err := do.Invoke[passkeys.Store](i)
		if err != nil {
			return nil, err
		}

		directory, err := do.Invoke[identity.Store](i)
		if err != nil {
			return nil, err
		}

		client, err := do.Invoke[database.Client](i)
		if err != nil {
			return nil, err
		}

		return passkeyflow.New(rp, credentials, directory, client, passkeyflow.WithPillars(pillars))
	})

	do.Provide(i, func(i do.Injector) (*mint.Minter, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		client, err := do.Invoke[database.Client](i)
		if err != nil {
			return nil, err
		}

		directory, err := do.Invoke[identity.Store](i)
		if err != nil {
			return nil, err
		}

		issuer, err := do.Invoke[tokens.Issuer](i)
		if err != nil {
			return nil, err
		}

		refresh, err := do.Invoke[*refreshtokens.SQLStore](i)
		if err != nil {
			return nil, err
		}

		claims, err := do.Invoke[signin.ClaimsBuilder](i)
		if err != nil {
			return nil, err
		}

		hooks, err := do.Invoke[signin.Hooks](i)
		if err != nil {
			return nil, err
		}

		return mint.New(client, directory, issuer, refresh,
			mint.WithPillars(pillars),
			mint.WithClaimsBuilder(claims),
			mint.WithHooks(hooks),
			mint.WithTokenTTL(cfg.Auth.AccessTokenTTL),
			mint.WithRefreshTokenTTL(cfg.Auth.RefreshTokenTTL),
		)
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

	do.Provide(i, func(i do.Injector) (*resetmail.Mailer, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		emailer, err := do.Invoke[email.Emailer](i)
		if err != nil {
			return nil, err
		}

		return resetmail.New(emailer, cfg.Auth.PasswordReset, cfg.Auth.Mail, pillars)
	})

	do.Provide(i, func(i do.Injector) (*passwordreset.Service, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		client, err := do.Invoke[database.Client](i)
		if err != nil {
			return nil, err
		}

		tokenStore, err := do.Invoke[*passwordreset.SQLStore](i)
		if err != nil {
			return nil, err
		}

		directory, err := do.Invoke[identity.Store](i)
		if err != nil {
			return nil, err
		}

		authenticator, err := do.Invoke[authentication.Authenticator](i)
		if err != nil {
			return nil, err
		}

		mailer, err := do.Invoke[*resetmail.Mailer](i)
		if err != nil {
			return nil, err
		}

		return passwordreset.NewService(client, tokenStore, directory, authenticator, mailer,
			passwordreset.WithServicePillars(pillars),
			passwordreset.WithTokenLifetime(cfg.Auth.PasswordReset.TokenLifetime),
		)
	})

	do.Provide(i, func(i do.Injector) (*interceptor.Authenticator, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		issuer, err := do.Invoke[tokens.Issuer](i)
		if err != nil {
			return nil, err
		}

		store, err := do.Invoke[sessions.Store[session.Data]](i)
		if err != nil {
			return nil, err
		}

		directory, err := do.Invoke[identity.Store](i)
		if err != nil {
			return nil, err
		}

		client, err := do.Invoke[database.Client](i)
		if err != nil {
			return nil, err
		}

		policy, err := do.Invoke[authorization.PolicyResolver](i)
		if err != nil {
			return nil, err
		}

		return interceptor.New(issuer, store, directory, client, policy, interceptor.WithPillars(pillars))
	})
}

// registerDomains registers Fijo's stores. One today; the roster, the schedule
// and messaging join here.
func registerDomains(i do.Injector) {
	do.Provide(i, func(i do.Injector) (owners.Store, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		store, err := owners.NewSQLStore(owners.WithPillars(pillars))
		if err != nil {
			return nil, err
		}

		return store, nil
	})

	do.Provide(i, func(i do.Injector) (schedule.Store, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		return schedule.NewSQLStore(schedule.WithPillars(pillars))
	})

	// The command layer puts the rows and the audit entry on one transaction,
	// which is why it takes the recorder rather than the reader.
	do.Provide(i, func(i do.Injector) (*schedule.Commands, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		client, err := do.Invoke[database.Client](i)
		if err != nil {
			return nil, err
		}

		store, err := do.Invoke[schedule.Store](i)
		if err != nil {
			return nil, err
		}

		recorder, err := do.Invoke[audit.Recorder](i)
		if err != nil {
			return nil, err
		}

		return schedule.NewCommands(client, store, recorder, schedule.WithCommandsPillars(pillars))
	})
}

// registerServers registers Fijo's own gRPC services.
func registerServers(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*ownerauthgrpc.Server, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		flow, err := do.Invoke[*passkeyflow.Service](i)
		if err != nil {
			return nil, err
		}

		minter, err := do.Invoke[*mint.Minter](i)
		if err != nil {
			return nil, err
		}

		reset, err := do.Invoke[*passwordreset.Service](i)
		if err != nil {
			return nil, err
		}

		signIn, err := do.Invoke[*signin.Service](i)
		if err != nil {
			return nil, err
		}

		store, err := do.Invoke[sessions.Store[session.Data]](i)
		if err != nil {
			return nil, err
		}

		return ownerauthgrpc.NewServer(flow, minter, reset, signIn, store, pillars)
	})

	do.Provide(i, func(i do.Injector) (*ownersgrpc.Server, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		store, err := do.Invoke[owners.Store](i)
		if err != nil {
			return nil, err
		}

		directory, err := do.Invoke[identity.Store](i)
		if err != nil {
			return nil, err
		}

		client, err := do.Invoke[database.Client](i)
		if err != nil {
			return nil, err
		}

		return ownersgrpc.NewServer(store, directory, client, pillars)
	})
}

// registerScheduleServer registers the grid's gRPC surface.
//
// The history reader is optional: a deployment with no audit log configured
// still serves the grid, and ListHistory is the only RPC that stops working.
func registerScheduleServer(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*schedulegrpc.Server, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		store, err := do.Invoke[schedule.Store](i)
		if err != nil {
			return nil, err
		}

		commands, err := do.Invoke[*schedule.Commands](i)
		if err != nil {
			return nil, err
		}

		client, err := do.Invoke[database.Client](i)
		if err != nil {
			return nil, err
		}

		var opts []schedulegrpc.Option

		if reader, readerErr := do.Invoke[audit.Reader](i); readerErr == nil {
			opts = append(opts, schedulegrpc.WithHistory(reader))
		}

		grid, err := do.Invoke[*schedule.Materializer](i)
		if err != nil {
			return nil, err
		}

		opts = append(opts, schedulegrpc.WithMaterializer(grid))

		return schedulegrpc.NewServer(store, commands, client, pillars, opts...)
	})
}
