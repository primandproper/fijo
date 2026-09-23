package main

import (
	"encoding/base64"
	"time"

	"github.com/primandproper/fijo/internal/config"

	auditcfg "github.com/primandproper/platform-go/v14/audit/config"
	webauthnsessionscfg "github.com/primandproper/platform-go/v14/authentication/webauthnsessions/config"
	identitycfg "github.com/primandproper/platform-go/v14/identity/config"
	"github.com/primandproper/platform-go/v14/rbac"
	rbaccfg "github.com/primandproper/platform-go/v14/rbac/config"
	"github.com/primandproper/platform-go/v14/service"
	tokenscfg "github.com/primandproper/primitives-go/v2/authentication/tokens/config"
	"github.com/primandproper/primitives-go/v2/authentication/webauthn"
	databasecfg "github.com/primandproper/primitives-go/v2/database/config"
	"github.com/primandproper/primitives-go/v2/database/dialect"
	emailcfg "github.com/primandproper/primitives-go/v2/email/config"
	"github.com/primandproper/primitives-go/v2/email/resend"
	"github.com/primandproper/primitives-go/v2/httpclient"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/observability/logging"
	loggingcfg "github.com/primandproper/primitives-go/v2/observability/logging/config"
	grpcserver "github.com/primandproper/primitives-go/v2/server/grpc"
)

// Each builder returns a fully-formed *config.Config for one environment. The
// configs are constructed here as real, typed Go objects — the compiler and the
// config's own ValidateWithContext are the guardrails — and then Render projects
// them to the JSON files under config/.
//
// A platform subsystem is composed into the process by being present here and
// by nothing else, so this file is also the shortest statement of what a Fijo
// process is made of: a Postgres database, the identity directory, database-
// backed authorization policy, the audit log, a token issuer, a WebAuthn relying
// party with a database ceremony store, a mailer, and the gRPC server.

const (
	// replaceAtDeploy marks a secret the deployment supplies through the
	// environment. Every one of them has a FIJO_ variable in the envvars
	// package; the rendered production file carries the marker so a deployment
	// that forgot one fails loudly at the provider rather than quietly with a
	// placeholder.
	replaceAtDeploy = "REPLACE_AT_DEPLOY"

	grpcPort = 50051

	// The database Fijo's local docker-compose stands up.
	localDatabaseHost = "localhost"
	localDatabasePort = 5432
	localDatabaseName = "fijo"
	localDatabaseUser = "fijo"
	localDatabasePass = "fijo"

	// The SvelteKit dev server, which is also the WebAuthn origin locally.
	localFrontendOrigin = "http://localhost:5173"

	productionFrontendOrigin = "https://app.fijo.example"
	productionRPID           = "app.fijo.example"

	// brand is the display name the relying party and the mail sender carry.
	brand = "Fijo"

	passkeyCeremonyTimeout = 5 * time.Minute

	// A studio invites a second owner or a teacher; a week to answer is
	// generous and a month is the most an owner may ask for.
	invitationTTL     = 7 * 24 * time.Hour
	maxInvitationTTL  = 30 * 24 * time.Hour
	httpClientTimeout = 15 * time.Second
	tokenIssuer       = "fijo"
)

// localDevSigningKey is a fixed 32-byte key so a local token survives a restart.
// It signs nothing outside a laptop.
const localDevSigningKey = "fijo-localdev-signing-key-000000"

// productionSigningKeyMarker is a 32-byte marker that decodes cleanly so the
// rendered file validates; FIJO_TOKENS_SIGNING_KEY replaces it at deploy.
const productionSigningKeyMarker = "REPLACE_AT_DEPLOY_signing_key_00"

func base64Key(raw string) string {
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

func connection(host string, port uint16, name, user, password string, disableSSL bool) databasecfg.ConnectionDetails {
	return databasecfg.ConnectionDetails{
		Host:       host,
		Port:       port,
		Database:   name,
		Username:   user,
		Password:   password,
		DisableSSL: disableSSL,
	}
}

// platform is what both environments share: every subsystem present, the
// dialect Postgres, the table prefixes left at their defaults because Fijo has
// the database to itself.
func platform(name string, level logging.Level, db databasecfg.ConnectionDetails, runMigrations bool, rp *webauthn.Config, key string, email *emailcfg.Config) service.Config {
	return service.Config{
		Name: name,
		Observability: observability.Config{
			Logging: loggingcfg.Config{
				Provider:    loggingcfg.ProviderSlog,
				ServiceName: name,
				Level:       level,
			},
		},
		Database: &databasecfg.Config{
			Provider:        databasecfg.ProviderPostgres,
			ReadConnection:  db,
			WriteConnection: db,
			RunMigrations:   runMigrations,
		},
		// Spelled out rather than left at its defaults: a block holding nothing
		// but the library's defaults is released as unconfigured, and a process
		// with no identity block has no directory.
		Identity: &identitycfg.Config{
			InvitationTTL:    invitationTTL,
			MaxInvitationTTL: maxInvitationTTL,
		},
		Authorization: &rbaccfg.Config{
			Provider: rbaccfg.ProviderDatabase,
			Database: &rbac.Config{Dialect: dialect.Postgres},
		},
		Audit: &auditcfg.Config{Dialect: dialect.Postgres},
		Tokens: &tokenscfg.Config{
			Provider:                tokenscfg.ProviderJWT,
			Issuer:                  tokenIssuer,
			Audience:                tokenIssuer,
			Base64EncodedSigningKey: key,
		},
		WebAuthn: &webauthnsessionscfg.Config{
			Provider:     webauthnsessionscfg.ProviderDatabase,
			RelyingParty: *rp,
		},
		Email:      email,
		GRPCServer: &grpcserver.Config{Port: grpcPort},
		// The mailer sends through this client. Every field is set explicitly
		// because a block holding nothing but defaults is released as
		// unconfigured, and an unconfigured client is a mailer with no transport.
		HTTPClient: &httpclient.Config{Timeout: httpClientTimeout, MaxIdleConns: 16, MaxIdleConnsPerHost: 8, EnableTracing: true},
	}
}

// buildLocalDevConfig is the config a developer runs against locally: debug
// logging, the docker-compose Postgres, migrations on boot, a passkey relying
// party for the Vite dev server, and a mailer that drops every message.
func buildLocalDevConfig() *config.Config {
	return &config.Config{
		Config: platform(
			config.DefaultServiceName,
			logging.DebugLevel,
			connection(localDatabaseHost, localDatabasePort, localDatabaseName, localDatabaseUser, localDatabasePass, true),
			true,
			&webauthn.Config{
				RPID:            "localhost",
				RPDisplayName:   brand + " (local)",
				RPOrigins:       []string{localFrontendOrigin},
				CeremonyTimeout: passkeyCeremonyTimeout,
			},
			base64Key(localDevSigningKey),
			&emailcfg.Config{Provider: emailcfg.ProviderNoop},
		),
		Auth: config.AuthConfig{
			PasswordReset: config.PasswordResetConfig{
				LinkBaseURL: localFrontendOrigin + config.DefaultPasswordResetRoute,
			},
			Mail: config.MailConfig{
				FromAddress: "fijo@localhost",
				FromName:    brand,
			},
		},
		// A developer watching the grid fill in does not want to wait a
		// quarter of an hour for it, and a short horizon keeps the local
		// database small.
		Schedule: config.ScheduleConfig{
			MaterializeInterval: time.Minute,
			DefaultHorizonWeeks: 4,
		},
	}
}

// buildProductionConfig is the config for a production deployment: info
// logging, a database whose password arrives from the environment, migrations
// run by `fijo migrate` rather than on boot, and a real mailer.
func buildProductionConfig() *config.Config {
	return &config.Config{
		Config: platform(
			config.DefaultServiceName,
			logging.InfoLevel,
			connection("postgres.fijo.internal", localDatabasePort, localDatabaseName, localDatabaseUser, replaceAtDeploy, false),
			false,
			&webauthn.Config{
				RPID:            productionRPID,
				RPDisplayName:   brand,
				RPOrigins:       []string{productionFrontendOrigin},
				CeremonyTimeout: passkeyCeremonyTimeout,
			},
			base64Key(productionSigningKeyMarker),
			&emailcfg.Config{
				Provider: emailcfg.ProviderResend,
				Resend:   &resend.Config{APIToken: replaceAtDeploy},
			},
		),
		Auth: config.AuthConfig{
			PasswordReset: config.PasswordResetConfig{
				LinkBaseURL: productionFrontendOrigin + config.DefaultPasswordResetRoute,
			},
			Mail: config.MailConfig{
				FromAddress: "no-reply@fijo.example",
				FromName:    brand,
			},
		},
		Schedule: config.ScheduleConfig{
			MaterializeInterval: config.DefaultMaterializeInterval,
			DefaultHorizonWeeks: config.DefaultHorizonWeeks,
		},
	}
}
