// Package mint issues a sign-in for a user another credential has already
// proven.
//
// platform-go's signin service has four doors and every one of them proves a
// password or a mailed link on its way to a token; there is no door for a
// subject proven by a passkey, so a WebAuthn login that wants the same token,
// the same refresh-token family and the same hooks as a password login has
// nowhere in the platform to get them. This package is that door, written to
// the same shape as signin's own mint — the same claims builder, the same
// refresh-token store, the same AfterAuthenticate and AfterIssueToken inside
// one transaction — so that a token minted here is indistinguishable to the
// interceptor and to the refresh exchange from one minted there.
//
// It is an extraction candidate, not a design: the right home for this is a
// signin.Service method that takes a proven principal, and the issue asking
// for one is filed against platform-go. When it lands, this package becomes a
// call into it and the duplication of the mint shape goes with it.
package mint

import (
	"context"
	"time"

	"github.com/primandproper/platform-go/v14/authentication/signin"
	"github.com/primandproper/platform-go/v14/identity"
	"github.com/primandproper/primitives-go/v2/authentication/tokens"
	"github.com/primandproper/primitives-go/v2/clock"
	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/identifiers"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/observability/logging"
	"github.com/primandproper/primitives-go/v2/observability/metrics"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

const serviceName = "signin_mint"

// The refusals.
var (
	ErrNilDatabaseClient = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil database client for the minter")
	ErrNilDirectory      = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil directory for the minter")
	ErrNilTokenIssuer    = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil token issuer for the minter")
	ErrNilRefreshTokens  = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil refresh token store for the minter")
	ErrEmptyUserID       = platformerrors.Wrap(platformerrors.ErrEmptyInputParameter, "empty user ID")
)

// Directory is the slice of identity.Store the minter reads.
type Directory interface {
	GetPrincipal(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, userID, activeAccountID string) (*identity.Principal, error)
}

// Minter issues sign-ins for proven users.
type Minter struct {
	client        database.Client
	directory     Directory
	issuer        tokens.Issuer
	refreshTokens signin.RefreshTokenStore
	claims        signin.ClaimsBuilder
	hooks         signin.Hooks
	clk           clock.Clock
	o11y          observability.Observer
	instruments   *metrics.OperationSet
	tokenTTL      time.Duration
	refreshTTL    time.Duration
}

// Option configures a Minter.
type Option func(*Minter)

// WithClaimsBuilder sets how a token's claims are built; the default is
// signin.DefaultClaims. Fijo passes the same builder its signin.Service uses
// so a passkey token carries the same session claim a password token does.
func WithClaimsBuilder(build signin.ClaimsBuilder) Option {
	return func(m *Minter) {
		if build != nil {
			m.claims = build
		}
	}
}

// WithHooks sets the hooks a mint runs inside its transaction.
func WithHooks(hooks signin.Hooks) Option {
	return func(m *Minter) {
		if hooks != nil {
			m.hooks = hooks
		}
	}
}

// WithTokenTTL bounds the access token.
func WithTokenTTL(ttl time.Duration) Option {
	return func(m *Minter) {
		if ttl > 0 {
			m.tokenTTL = ttl
		}
	}
}

// WithRefreshTokenTTL bounds the refresh token.
func WithRefreshTokenTTL(ttl time.Duration) Option {
	return func(m *Minter) {
		if ttl > 0 {
			m.refreshTTL = ttl
		}
	}
}

// WithClock sets the clock.
func WithClock(c clock.Clock) Option {
	return func(m *Minter) {
		if c != nil {
			m.clk = c
		}
	}
}

// WithLogger attaches a logger without the other pillars.
func WithLogger(logger logging.Logger) Option {
	return func(m *Minter) { m.o11y = observability.NewObserver(serviceName, logger, nil) }
}

// WithPillars attaches the observability pillars.
func WithPillars(p *observability.Pillars) Option {
	return func(m *Minter) {
		if p == nil {
			return
		}

		m.o11y = observability.NewObserver(serviceName, p.Logger, p.TracerProvider)

		if set, err := metrics.NewOperationSet(p.MetricsProvider, serviceName); err == nil {
			m.instruments = set
		}
	}
}

// New builds a Minter.
func New(
	client database.Client,
	directory Directory,
	issuer tokens.Issuer,
	refreshTokens signin.RefreshTokenStore,
	opts ...Option,
) (*Minter, error) {
	switch {
	case client == nil:
		return nil, ErrNilDatabaseClient
	case directory == nil:
		return nil, ErrNilDirectory
	case issuer == nil:
		return nil, ErrNilTokenIssuer
	case refreshTokens == nil:
		return nil, ErrNilRefreshTokens
	}

	instruments, err := metrics.NewOperationSet(nil, serviceName)
	if err != nil {
		return nil, platformerrors.Wrap(err, "creating minter instruments")
	}

	m := &Minter{
		client:        client,
		directory:     directory,
		issuer:        issuer,
		refreshTokens: refreshTokens,
		claims:        signin.DefaultClaims,
		hooks:         signin.NoopHooks{},
		clk:           clock.NewClock(),
		o11y:          observability.NewObserver(serviceName, nil, nil),
		instruments:   instruments,
		tokenTTL:      signin.DefaultTokenTTL,
		refreshTTL:    signin.DefaultRefreshTokenTTL,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}

	if m.refreshTTL < m.tokenTTL {
		return nil, signin.ErrRefreshTokenTTLTooShort
	}

	return m, nil
}

// IssueForUser mints a sign-in for a user somebody else has proven. It resolves
// the principal on the client's reader — which is where a suspended user is
// refused, with identity.ErrSignInNotAdmitted — mints the token outside any
// transaction, and then runs the hooks and the refresh-token issue inside one.
func (m *Minter) IssueForUser(ctx context.Context, scope tenancy.Scope, userID, activeAccountID string) (signIn *signin.SignIn, err error) {
	ctx, op := m.o11y.Begin(ctx, observability.WithValue("identity.user_id", userID))
	defer op.End()

	m.instruments.Attempt(ctx)
	defer op.Time(ctx, m.clk, m.instruments.Latency)()

	defer func() {
		if err != nil {
			m.instruments.Failed(ctx)
		}
	}()

	if userID == "" {
		return nil, op.Error(ErrEmptyUserID, "minting a sign-in")
	}

	if err = scope.Validate(); err != nil {
		return nil, op.Error(err, "checking the scope a sign-in is minted in")
	}

	principal, err := m.directory.GetPrincipal(ctx, m.client.Reader(), scope, userID, activeAccountID)
	if err != nil {
		return nil, op.Error(err, "resolving the principal signing in")
	}

	familyID := identifiers.New()

	claims, err := m.claims(ctx, &signin.ClaimsInput{Principal: principal, FamilyID: familyID})
	if err != nil {
		return nil, op.Error(err, "building token claims")
	}

	token, jti, err := m.issuer.IssueToken(ctx, principal.User.ID, m.tokenTTL, claims)
	if err != nil {
		return nil, op.Error(err, "issuing a token")
	}

	signIn = &signin.SignIn{
		Token:     token,
		TokenID:   jti,
		FamilyID:  familyID,
		ExpiresAt: m.clk.Now().UTC().Add(m.tokenTTL),
		Principal: principal,
	}

	if err = m.client.WithTransaction(ctx, func(tx database.Tx) error {
		if hookErr := m.hooks.AfterAuthenticate(ctx, tx, scope, &signin.Authentication{Principal: principal}); hookErr != nil {
			return platformerrors.Wrap(hookErr, "running the after-authenticate hook")
		}

		issuance, issueErr := m.refreshTokens.Issue(ctx, tx, scope, &signin.RefreshTokenRequest{
			TTL:             m.refreshTTL,
			FamilyID:        familyID,
			SubjectID:       principal.User.ID,
			ActiveAccountID: principal.ActiveAccountID,
		})
		if issueErr != nil {
			return platformerrors.Wrap(issueErr, "minting a refresh token")
		}

		signIn.RefreshToken = issuance.Secret
		signIn.RefreshTokenExpiresAt = issuance.Token.ExpiresAt

		if hookErr := m.hooks.AfterIssueToken(ctx, tx, scope, signIn); hookErr != nil {
			return platformerrors.Wrap(hookErr, "running the after-issue-token hook")
		}

		return nil
	}); err != nil {
		return nil, op.Error(err, "recording a sign-in")
	}

	return signIn, nil
}
