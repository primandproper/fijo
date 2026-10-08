// Package passkeyflow is the WebAuthn ceremony over Fijo's directory: enrolling
// a passkey for a signed-in owner, and proving one at the door.
//
// The three pieces of state a passkey login has each have a platform home — the
// protocol in primitives-go's webauthn, the ceremony in flight in
// webauthnsessions, the registered credential in passkeys — and what none of
// them can do is turn a WebAuthn user handle into one of Fijo's users, because
// only the directory that holds them can. This package is that translation and
// the orchestration around it, and nothing else: it stores no state of its own.
//
// The user handle is the identity user ID. It is what the authenticator will
// hand back on a discoverable login, so it has to be a value the directory can
// look up directly and a value that never changes; a username fails the second
// test. It is not a secret and it is not shown to anybody.
package passkeyflow

import (
	"bytes"
	"context"

	"github.com/primandproper/platform-go/v15/authentication/passkeys"
	"github.com/primandproper/platform-go/v15/identity"
	"github.com/primandproper/primitives-go/v2/authentication/webauthn"
	"github.com/primandproper/primitives-go/v2/clock"
	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/identifiers"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/observability/metrics"
	"github.com/primandproper/primitives-go/v2/tenancy"

	"github.com/go-webauthn/webauthn/protocol"
)

const serviceName = "passkey_flow"

// The refusals.
var (
	ErrNilRelyingParty   = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil webauthn relying party")
	ErrNilCredentials    = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil passkey store")
	ErrNilDirectory      = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil directory")
	ErrNilDatabaseClient = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil database client")
	ErrEmptyUserID       = platformerrors.Wrap(platformerrors.ErrEmptyInputParameter, "empty user ID")
	ErrEmptyResponse     = platformerrors.Wrap(platformerrors.ErrEmptyInputParameter, "empty authenticator response")

	// ErrUnknownCredential is an assertion by a credential the directory does
	// not hold, which is a login that did not happen.
	ErrUnknownCredential = platformerrors.New("passkey credential is not registered")
)

// Directory is the slice of identity.Store the flow reads.
type Directory interface {
	GetUser(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, userID string) (*identity.User, error)
	GetUserByUsername(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, username string) (*identity.User, error)
}

// Service runs the ceremonies.
type Service struct {
	rp          *webauthn.RelyingParty
	credentials passkeys.Store
	directory   Directory
	client      database.Client
	clk         clock.Clock
	o11y        observability.Observer
	instruments *metrics.OperationSet
}

// Option configures a Service.
type Option func(*Service)

// WithClock sets the clock.
func WithClock(c clock.Clock) Option {
	return func(s *Service) {
		if c != nil {
			s.clk = c
		}
	}
}

// WithPillars attaches the observability pillars.
func WithPillars(p *observability.Pillars) Option {
	return func(s *Service) {
		if p == nil {
			return
		}

		s.o11y = observability.NewObserver(serviceName, p.Logger, p.TracerProvider)

		if set, err := metrics.NewOperationSet(p.MetricsProvider, serviceName); err == nil {
			s.instruments = set
		}
	}
}

// New builds a Service.
func New(rp *webauthn.RelyingParty, credentials passkeys.Store, directory Directory, client database.Client, opts ...Option) (*Service, error) {
	switch {
	case rp == nil:
		return nil, ErrNilRelyingParty
	case credentials == nil:
		return nil, ErrNilCredentials
	case directory == nil:
		return nil, ErrNilDirectory
	case client == nil:
		return nil, ErrNilDatabaseClient
	}

	instruments, err := metrics.NewOperationSet(nil, serviceName)
	if err != nil {
		return nil, platformerrors.Wrap(err, "creating passkey flow instruments")
	}

	s := &Service{
		rp:          rp,
		credentials: credentials,
		directory:   directory,
		client:      client,
		clk:         clock.NewClock(),
		o11y:        observability.NewObserver(serviceName, nil, nil),
		instruments: instruments,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}

	return s, nil
}

// source builds the platform's user source over this directory: a handle is a
// user ID, and the identity behind it is read on the given executor.
func (s *Service) source(q database.SQLQueryExecutor, scope tenancy.Scope) (*passkeys.UserSource, error) {
	return passkeys.NewUserSource(s.credentials, func(ctx context.Context, handle []byte) (passkeys.UserIdentity, error) {
		user, err := s.directory.GetUser(ctx, q, scope, string(handle))
		if err != nil {
			return passkeys.UserIdentity{}, err
		}

		return identityOf(user), nil
	})
}

func identityOf(user *identity.User) passkeys.UserIdentity {
	display := user.DisplayName
	if display == "" {
		display = user.Username
	}

	return passkeys.UserIdentity{UserID: user.ID, Name: user.Username, DisplayName: display}
}

func (s *Service) begin(ctx context.Context, values ...observability.BeginOption) (context.Context, observability.Operation, func(error)) {
	ctx, op := s.o11y.Begin(ctx, values...)
	s.instruments.Attempt(ctx)
	stop := op.Time(ctx, s.clk, s.instruments.Latency)

	return ctx, op, func(err error) {
		if err != nil {
			s.instruments.Failed(ctx)
		}

		stop()
		op.End()
	}
}

// BeginRegistration starts enrolling a passkey for a signed-in user. The
// options it returns go to the browser as-is; the ceremony state is in the
// platform's session store, keyed by the challenge inside them.
func (s *Service) BeginRegistration(ctx context.Context, scope tenancy.Scope, userID string) (creation *protocol.CredentialCreation, err error) {
	ctx, op, done := s.begin(ctx, observability.WithValue("identity.user_id", userID))
	defer func() { done(err) }()

	if userID == "" {
		return nil, op.Error(ErrEmptyUserID, "beginning passkey registration")
	}

	source, err := s.source(s.client.Reader(), scope)
	if err != nil {
		return nil, op.Error(err, "building the webauthn user source")
	}

	user, err := source.User(ctx, s.client.Reader(), scope, []byte(userID), passkeys.AuthenticatorFlags{})
	if err != nil {
		return nil, op.Error(err, "assembling the webauthn user")
	}

	creation, err = s.rp.BeginRegistration(ctx, user)
	if err != nil {
		return nil, op.Error(err, "beginning passkey registration")
	}

	return creation, nil
}

// FinishRegistration verifies the attestation the browser produced and stores
// the credential it carries.
func (s *Service) FinishRegistration(ctx context.Context, scope tenancy.Scope, userID, friendlyName string, response []byte) (stored *passkeys.Credential, err error) {
	ctx, op, done := s.begin(ctx, observability.WithValue("identity.user_id", userID))
	defer func() { done(err) }()

	switch {
	case userID == "":
		return nil, op.Error(ErrEmptyUserID, "finishing passkey registration")
	case len(response) == 0:
		return nil, op.Error(ErrEmptyResponse, "finishing passkey registration")
	}

	source, err := s.source(s.client.Reader(), scope)
	if err != nil {
		return nil, op.Error(err, "building the webauthn user source")
	}

	user, err := source.User(ctx, s.client.Reader(), scope, []byte(userID), passkeys.AuthenticatorFlags{})
	if err != nil {
		return nil, op.Error(err, "assembling the webauthn user")
	}

	credential, err := s.rp.FinishRegistrationBody(ctx, user, bytes.NewReader(response))
	if err != nil {
		return nil, op.Error(err, "finishing passkey registration")
	}

	transports := make([]string, 0, len(credential.Transport))
	for _, t := range credential.Transport {
		transports = append(transports, string(t))
	}

	if err = s.client.WithTransaction(ctx, func(tx database.Tx) error {
		var writeErr error

		stored, writeErr = s.credentials.CreateCredential(ctx, tx, scope, &passkeys.Credential{
			ID:            identifiers.New(),
			Scope:         scope,
			BelongsToUser: userID,
			FriendlyName:  friendlyName,
			Transports:    transports,
			CredentialID:  credential.ID,
			PublicKey:     credential.PublicKey,
			SignCount:     credential.Authenticator.SignCount,
		})

		return writeErr
	}); err != nil {
		return nil, op.Error(err, "storing the passkey credential")
	}

	return stored, nil
}

// BeginLogin starts a passkey login. With a username it lists that user's
// credentials in the options; without one it begins a discoverable login and
// lets the authenticator say who is signing in.
func (s *Service) BeginLogin(ctx context.Context, scope tenancy.Scope, username string) (assertion *protocol.CredentialAssertion, err error) {
	ctx, op, done := s.begin(ctx)
	defer func() { done(err) }()

	if username == "" {
		assertion, err = s.rp.BeginDiscoverableLogin(ctx)
		if err != nil {
			return nil, op.Error(err, "beginning discoverable passkey login")
		}

		return assertion, nil
	}

	user, err := s.namedUser(ctx, scope, username, passkeys.AuthenticatorFlags{})
	if err != nil {
		return nil, op.Error(err, "assembling the webauthn user")
	}

	assertion, err = s.rp.BeginLogin(ctx, user)
	if err != nil {
		return nil, op.Error(err, "beginning passkey login")
	}

	return assertion, nil
}

// FinishLogin verifies the assertion, records the authenticator's sign count,
// and returns the user it proved. It proves and records; minting a token is
// the caller's, through the mint package.
func (s *Service) FinishLogin(ctx context.Context, scope tenancy.Scope, username string, response []byte) (userID string, err error) {
	ctx, op, done := s.begin(ctx)
	defer func() { done(err) }()

	if len(response) == 0 {
		return "", op.Error(ErrEmptyResponse, "finishing passkey login")
	}

	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return "", op.Error(err, "parsing the passkey assertion")
	}

	flags := passkeys.AuthenticatorFlags{
		BackupEligible: parsed.Response.AuthenticatorData.Flags.HasBackupEligible(),
		BackupState:    parsed.Response.AuthenticatorData.Flags.HasBackupState(),
	}

	var (
		proven     webauthn.User
		credential *webauthn.Credential
	)

	if username == "" {
		source, sourceErr := s.source(s.client.Reader(), scope)
		if sourceErr != nil {
			return "", op.Error(sourceErr, "building the webauthn user source")
		}

		proven, credential, err = s.rp.FinishDiscoverableLoginBody(ctx,
			source.DiscoverableUserHandler(ctx, s.client.Reader(), scope, flags), bytes.NewReader(response))
		if err != nil {
			return "", op.Error(err, "finishing discoverable passkey login")
		}
	} else {
		proven, err = s.namedUser(ctx, scope, username, flags)
		if err != nil {
			return "", op.Error(err, "assembling the webauthn user")
		}

		credential, err = s.rp.FinishLoginBody(ctx, proven, bytes.NewReader(response))
		if err != nil {
			return "", op.Error(err, "finishing passkey login")
		}
	}

	if proven == nil || credential == nil {
		return "", op.Error(ErrUnknownCredential, "resolving the passkey owner")
	}

	userID = string(proven.WebAuthnID())

	stored, err := s.credentials.GetCredentialByCredentialID(ctx, s.client.Reader(), scope, credential.ID)
	if err != nil {
		return "", op.Error(err, "looking up the proven credential")
	}

	if stored == nil || stored.BelongsToUser != userID {
		return "", op.Error(ErrUnknownCredential, "checking the proven credential's owner")
	}

	// The sign count is the only clone detection WebAuthn has, and the platform
	// answers its write-back with an error rather than a log line on purpose: a
	// login whose bookkeeping failed is a login that did not happen.
	if err = s.client.WithTransaction(ctx, func(tx database.Tx) error {
		_, useErr := s.credentials.RecordUse(ctx, tx, scope, stored.ID, credential.Authenticator.SignCount, s.clk.Now())

		return useErr
	}); err != nil {
		return "", op.Error(err, "recording the passkey's use")
	}

	return userID, nil
}

// List returns a user's live passkeys.
func (s *Service) List(ctx context.Context, scope tenancy.Scope, userID string) (credentials []*passkeys.Credential, err error) {
	ctx, op, done := s.begin(ctx, observability.WithValue("identity.user_id", userID))
	defer func() { done(err) }()

	if userID == "" {
		return nil, op.Error(ErrEmptyUserID, "listing passkeys")
	}

	credentials, err = s.credentials.GetCredentialsForUser(ctx, s.client.Reader(), scope, userID)
	if err != nil {
		return nil, op.Error(err, "listing passkeys")
	}

	return credentials, nil
}

// Archive takes one of a user's passkeys off their account. The row stays, so
// the record that it existed does.
func (s *Service) Archive(ctx context.Context, scope tenancy.Scope, userID, credentialRowID string) (archived *passkeys.Credential, err error) {
	ctx, op, done := s.begin(ctx, observability.WithValue("identity.user_id", userID))
	defer func() { done(err) }()

	if userID == "" {
		return nil, op.Error(ErrEmptyUserID, "archiving a passkey")
	}

	if err = s.client.WithTransaction(ctx, func(tx database.Tx) error {
		var archiveErr error

		archived, archiveErr = s.credentials.ArchiveCredentialForUser(ctx, tx, scope, credentialRowID, userID)

		return archiveErr
	}); err != nil {
		return nil, op.Error(err, "archiving a passkey")
	}

	return archived, nil
}

// namedUser assembles the webauthn user for a username, with the flags the
// assertion reported so a synced passkey's backup flags match what the
// registration saw.
func (s *Service) namedUser(ctx context.Context, scope tenancy.Scope, username string, flags passkeys.AuthenticatorFlags) (webauthn.User, error) {
	user, err := s.directory.GetUserByUsername(ctx, s.client.Reader(), scope, username)
	if err != nil {
		return nil, err
	}

	source, err := s.source(s.client.Reader(), scope)
	if err != nil {
		return nil, err
	}

	return source.User(ctx, s.client.Reader(), scope, []byte(user.ID), flags)
}
