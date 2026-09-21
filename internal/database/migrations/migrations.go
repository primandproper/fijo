// Package migrations is the one sequence every Fijo database is brought up by.
//
// Most of the schema is not Fijo's. Every platform package Fijo composes owns
// its tables and renders their DDL for a dialect and a table prefix, and
// primitives-go's migrate package splices that text into a numbered sequence
// as if it were a file — so the identity, authorization, audit, session,
// WebAuthn, passkey, reset-token and refresh-token tables are created by the
// same run that creates Fijo's own, and nothing platform-owned is copied into
// this repository. The numbers are Fijo's to pick, because the sequence is
// Fijo's; they start at one and leave room after the platform block for Fijo's
// own files, which begin at twenty.
//
// Two things ride along with the platform DDL, both of which DDB also does. The
// audit tables get the append-only triggers the platform renders separately,
// fenced so goose executes each trigger function whole. And four platform
// tables get a foreign key onto identity's tables that the platform cannot
// declare itself — a package that does not know identity exists cannot
// reference identity_users — so that archiving a user cascades to their
// passkeys and reset tokens, and a role a membership names has to be a role
// the policy seeded.
//
// The seed is the second half of Migrate. rbac stores roles as data and Fijo
// declares them in code (internal/authz/roles), so every run reconciles the
// tables with the declaration: a role renamed in Go is renamed in the
// database on the next deploy, under an advisory lock so replicas booting
// together converge rather than collide.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	stderrors "errors"
	"io/fs"
	"strings"

	"github.com/primandproper/fijo/internal/authz/roles"

	"github.com/primandproper/platform-go/v14/audit"
	auditmigrations "github.com/primandproper/platform-go/v14/audit/migrations"
	"github.com/primandproper/platform-go/v14/authentication/passkeys"
	passkeysmigrations "github.com/primandproper/platform-go/v14/authentication/passkeys/migrations"
	"github.com/primandproper/platform-go/v14/authentication/passwordreset"
	passwordresetmigrations "github.com/primandproper/platform-go/v14/authentication/passwordreset/migrations"
	"github.com/primandproper/platform-go/v14/authentication/signin/refreshtokens"
	refreshtokensmigrations "github.com/primandproper/platform-go/v14/authentication/signin/refreshtokens/migrations"
	"github.com/primandproper/platform-go/v14/authentication/webauthnsessions"
	webauthnsessionsmigrations "github.com/primandproper/platform-go/v14/authentication/webauthnsessions/migrations"
	"github.com/primandproper/platform-go/v14/identity"
	identitymigrations "github.com/primandproper/platform-go/v14/identity/migrations"
	"github.com/primandproper/platform-go/v14/rbac"
	rbacmigrations "github.com/primandproper/platform-go/v14/rbac/migrations"
	sessionsdatabase "github.com/primandproper/platform-go/v14/sessions/database"
	sessionsmigrations "github.com/primandproper/platform-go/v14/sessions/database/migrations"
	"github.com/primandproper/primitives-go/v2/database"
	"github.com/primandproper/primitives-go/v2/database/ddl"
	"github.com/primandproper/primitives-go/v2/database/dialect"
	"github.com/primandproper/primitives-go/v2/database/migrate"
	"github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/observability/logging"
)

//go:embed migration_files/*.sql
var rawMigrations embed.FS

// The advisory lock keys. Deployments sharing a database serialize on the
// schema key; the seed has its own because the migrator's lock covers the
// schema and not what runs after it.
const (
	lockKey     = "fijo"
	seedLockKey = "fijo.authorization.seed"
)

// The platform block of the sequence. A version, once shipped, is never
// reassigned; a platform package that grows a second schema version lands as
// a new number after the last one here.
const (
	identityVersion         uint64 = 1
	authorizationVersion    uint64 = 2
	auditVersion            uint64 = 3
	sessionsVersion         uint64 = 4
	webauthnSessionsVersion uint64 = 5
	passkeysVersion         uint64 = 6
	passwordResetVersion    uint64 = 7
	refreshTokensVersion    uint64 = 8
)

// Fijo has the database to itself, so every platform table lives at its
// default prefix. These are spelled once so a store built elsewhere and the DDL
// rendered here cannot name different tables.
var (
	identityUsers           = ddl.Qualify(identity.DefaultTablePrefix) + "identity_users"
	identityUserRoles       = ddl.Qualify(identity.DefaultTablePrefix) + "identity_user_roles"
	identityMembershipRoles = ddl.Qualify(identity.DefaultTablePrefix) + "identity_membership_roles"
	authzRoles              = ddl.Qualify(rbac.DefaultTablePrefix) + "authz_roles"
	passkeyCredentials      = ddl.Qualify(passkeys.DefaultTablePrefix) + "webauthn_credentials"
	passwordResetTokens     = ddl.Qualify(passwordreset.DefaultTablePrefix) + "password_reset_tokens"
	refreshTokens           = ddl.Qualify(refreshtokens.DefaultTablePrefix) + "signin_refresh_tokens"
)

// Migrator brings a Fijo database to the current schema and seeds the
// authorization policy. It is what the database client runs on boot when
// RunMigrations is set and what `fijo migrate` runs by hand.
type Migrator struct {
	schema *migrate.Migrator
	logger logging.Logger
}

var _ database.Migrator = (*Migrator)(nil)

// NewMigrator renders every platform schema and assembles the sequence. It
// fails here, not at Migrate, for a migration file that does not parse or a
// generated version that collides with one on disk.
func NewMigrator(logger logging.Logger) (*Migrator, error) {
	files, err := fs.Sub(rawMigrations, "migration_files")
	if err != nil {
		return nil, errors.Wrap(err, "opening migration files")
	}

	generated := []struct {
		render  func() (string, error)
		name    string
		version uint64
	}{
		{version: identityVersion, name: "create_identity_tables", render: renderIdentity},
		{version: authorizationVersion, name: "create_authorization_tables", render: renderAuthorization},
		{version: auditVersion, name: "create_audit_tables", render: renderAudit},
		{version: sessionsVersion, name: "create_sessions_table", render: renderSessions},
		{version: webauthnSessionsVersion, name: "create_webauthn_sessions_table", render: renderWebAuthnSessions},
		{version: passkeysVersion, name: "create_passkey_credentials_table", render: renderPasskeys},
		{version: passwordResetVersion, name: "create_password_reset_tokens_table", render: renderPasswordReset},
		{version: refreshTokensVersion, name: "create_refresh_tokens_table", render: renderRefreshTokens},
	}

	opts := []migrate.Option{
		migrate.WithLogger(logging.EnsureLogger(logger)),
		migrate.WithLockKey(lockKey),
	}

	for idx := range generated {
		g := &generated[idx]

		body, renderErr := g.render()
		if renderErr != nil {
			return nil, errors.Wrapf(renderErr, "rendering %s", g.name)
		}

		opts = append(opts, migrate.WithGeneratedMigration(g.version, g.name, body))
	}

	schema, err := migrate.New(dialect.Postgres, files, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "building migrator")
	}

	return &Migrator{schema: schema, logger: logging.EnsureLogger(logger)}, nil
}

// Migrate applies every pending migration and then seeds the policy.
func (m *Migrator) Migrate(ctx context.Context, db *sql.DB) error {
	if err := m.schema.Migrate(ctx, db); err != nil {
		return err
	}

	return m.seedPolicy(ctx, db)
}

// seedPolicy reconciles the authorization tables with internal/authz/roles,
// in one transaction under an advisory lock so that replicas booting together
// seed once each and converge.
func (m *Migrator) seedPolicy(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "beginning authorization policy transaction")
	}

	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !stderrors.Is(rollbackErr, sql.ErrTxDone) {
			m.logger.Error("rolling back authorization policy transaction", rollbackErr)
		}
	}()

	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", seedLockKey); err != nil {
		return errors.Wrap(err, "locking authorization policy")
	}

	resolver, err := rbac.NewResolver(
		&rbac.Config{Dialect: dialect.Postgres, TablePrefix: rbac.DefaultTablePrefix},
		tx,
		rbac.WithLogger(m.logger),
	)
	if err != nil {
		return errors.Wrap(err, "building authorization resolver")
	}

	if err = resolver.SeedPolicy(ctx, tx, roles.Policy()); err != nil {
		return errors.Wrap(err, "seeding authorization policy")
	}

	if err = tx.Commit(); err != nil {
		return errors.Wrap(err, "committing authorization policy")
	}

	return nil
}

func renderIdentity() (string, error) {
	return identitymigrations.SQL(dialect.Postgres, identity.DefaultTablePrefix)
}

// renderAuthorization renders the policy tables and then ties the roles a
// user or a membership names to the roles the policy seeded, so a typo in a
// role assignment is a refused write rather than a principal with no grants.
func renderAuthorization() (string, error) {
	schema, err := rbacmigrations.SQL(dialect.Postgres, rbac.DefaultTablePrefix)
	if err != nil {
		return "", err
	}

	body := &strings.Builder{}
	body.WriteString(schema)
	body.WriteString(roleReference(identityUserRoles))
	body.WriteString(roleReference(identityMembershipRoles))

	return body.String(), nil
}

// renderAudit renders the audit tables and the triggers that make them
// append-only. The platform hands the triggers back pre-split because goose
// splits a migration on semicolons and a trigger function has semicolons in
// its body, so each one is fenced to be executed whole.
func renderAudit() (string, error) {
	schema, err := auditmigrations.SQL(dialect.Postgres, audit.DefaultTablePrefix)
	if err != nil {
		return "", err
	}

	appendOnly, err := auditmigrations.AppendOnlyStatements(dialect.Postgres, audit.DefaultTablePrefix)
	if err != nil {
		return "", errors.Wrap(err, "rendering audit append-only triggers")
	}

	body := &strings.Builder{}
	body.WriteString(schema)

	for _, statement := range appendOnly {
		body.WriteString("\n-- +goose StatementBegin\n")
		body.WriteString(statement)
		body.WriteString(";\n-- +goose StatementEnd\n")
	}

	return body.String(), nil
}

func renderSessions() (string, error) {
	return sessionsmigrations.SQL(dialect.Postgres, sessionsdatabase.DefaultTablePrefix)
}

func renderWebAuthnSessions() (string, error) {
	return webauthnsessionsmigrations.SQL(dialect.Postgres, webauthnsessions.DefaultTablePrefix)
}

func renderPasskeys() (string, error) {
	schema, err := passkeysmigrations.SQL(dialect.Postgres, passkeys.DefaultTablePrefix)
	if err != nil {
		return "", err
	}

	return schema + userCascade(passkeyCredentials, "belongs_to_user"), nil
}

func renderPasswordReset() (string, error) {
	schema, err := passwordresetmigrations.SQL(dialect.Postgres, passwordreset.DefaultTablePrefix)
	if err != nil {
		return "", err
	}

	return schema + userCascade(passwordResetTokens, "belongs_to_user"), nil
}

func renderRefreshTokens() (string, error) {
	schema, err := refreshtokensmigrations.SQL(dialect.Postgres, refreshtokens.DefaultTablePrefix)
	if err != nil {
		return "", err
	}

	return schema + userCascade(refreshTokens, "subject_id"), nil
}

// userCascade ties a platform table's user column to identity_users so that
// erasing a person erases what they held.
func userCascade(table, column string) string {
	return "\n\nALTER TABLE " + table +
		"\n\tADD CONSTRAINT " + table + "_" + column + "_fk" +
		"\n\tFOREIGN KEY (" + column + ") REFERENCES " + identityUsers + " (id) ON DELETE CASCADE;\n"
}

// roleReference ties a role-name column to the seeded roles. RESTRICT rather
// than CASCADE: a role in use is a role the seed may not silently drop.
func roleReference(table string) string {
	return "\n\nALTER TABLE " + table +
		"\n\tADD CONSTRAINT " + table + "_role_fk" +
		"\n\tFOREIGN KEY (role) REFERENCES " + authzRoles + " (name) ON DELETE RESTRICT;\n"
}
