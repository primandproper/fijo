package config

import (
	"context"
	"net/url"
	"time"

	sessionscfg "github.com/primandproper/platform-go/v14/sessions/config"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// The lifetimes Fijo's sign-in flow uses when the config names none. The access
// token is short because the session record behind it is what revocation acts
// on, and a token outliving its session by less than this is the most a
// revoked owner keeps. The refresh token is the length of a sign-in.
const (
	DefaultAccessTokenTTL     = 15 * time.Minute
	DefaultRefreshTokenTTL    = 30 * 24 * time.Hour
	DefaultPasswordResetTTL   = time.Hour
	DefaultCredentialSweep    = 10 * time.Minute
	DefaultPasswordResetRoute = "/reset-password"
)

// AuthConfig is the sign-in configuration that is Fijo's rather than any one
// platform package's. The engines it is built over — identity, the token
// issuer, WebAuthn, email — are configured in the platform half of Config;
// what lives here is the glue between them.
type AuthConfig struct {
	_                       struct{}            `json:"-"`
	Sessions                SessionsConfig      `envPrefix:"SESSIONS_"           json:"sessions"`
	Mail                    MailConfig          `envPrefix:"MAIL_"               json:"mail"`
	PasswordReset           PasswordResetConfig `envPrefix:"PASSWORD_RESET_"     json:"passwordReset"`
	AccessTokenTTL          time.Duration       `env:"ACCESS_TOKEN_TTL"          json:"accessTokenTTL,omitempty"`
	RefreshTokenTTL         time.Duration       `env:"REFRESH_TOKEN_TTL"         json:"refreshTokenTTL,omitempty"`
	CredentialSweepInterval time.Duration       `env:"CREDENTIAL_SWEEP_INTERVAL" json:"credentialSweepInterval,omitempty"`
}

// SessionsConfig is the slice of platform-go's sessions config Fijo exposes.
//
// It is Fijo's own struct rather than the platform one embedded, for one
// reason: the platform config's Provider carries an envDefault of "cache", and
// environment overlay applies an envDefault over whatever a file said, so a
// checked-in config naming the database provider would be read back as the
// cache provider on every boot. Fijo is Postgres-only by decision — sessions
// included, so that a revocation reaches every replica and survives a restart
// — and a provider that cannot be chosen has no business being configurable.
// Platform renders the platform struct with the provider fixed.
type SessionsConfig struct {
	_ struct{} `json:"-"`

	// IdleTimeout is how long an owner may walk away and come back. Unset takes
	// the platform default; zero disables the idle timeout.
	IdleTimeout *time.Duration `env:"IDLE_TIMEOUT" json:"idleTimeout,omitempty"`

	// TouchInterval bounds how often a read refreshes the idle deadline. Unset
	// takes the platform default.
	TouchInterval *time.Duration `env:"TOUCH_INTERVAL" json:"touchInterval,omitempty"`

	// SweepInterval is how often expired session rows are removed. Unset takes
	// the platform default; zero starts no sweeper.
	SweepInterval *time.Duration `env:"SWEEP_INTERVAL" json:"sweepInterval,omitempty"`

	// AbsoluteTimeout is how long a session may exist at all. Zero takes the
	// platform default.
	AbsoluteTimeout time.Duration `env:"ABSOLUTE_TIMEOUT" json:"absoluteTimeout,omitempty"`
}

// Platform renders the platform-go sessions config this block stands for, with
// the database provider fixed.
func (cfg *SessionsConfig) Platform() *sessionscfg.Config {
	out := &sessionscfg.Config{
		Provider:        sessionscfg.ProviderDatabase,
		IdleTimeout:     cfg.IdleTimeout,
		TouchInterval:   cfg.TouchInterval,
		SweepInterval:   cfg.SweepInterval,
		AbsoluteTimeout: cfg.AbsoluteTimeout,
	}

	out.EnsureDefaults()

	return out
}

// PasswordResetConfig describes the link a password-reset mail carries.
type PasswordResetConfig struct {
	_ struct{} `json:"-"`

	// LinkBaseURL is the frontend page that completes a reset; the token is
	// appended as a query parameter. It is required wherever mail is sent.
	LinkBaseURL string `env:"LINK_BASE_URL" json:"linkBaseURL,omitempty"`

	// TokenLifetime bounds a reset link. Zero takes DefaultPasswordResetTTL.
	TokenLifetime time.Duration `env:"TOKEN_LIFETIME" json:"tokenLifetime,omitempty"`
}

// MailConfig is the From address on outbound mail.
type MailConfig struct {
	_ struct{} `json:"-"`

	FromAddress string `env:"FROM_ADDRESS" json:"fromAddress,omitempty"`
	FromName    string `env:"FROM_NAME"    json:"fromName,omitempty"`
}

var _ validation.ValidatableWithContext = (*AuthConfig)(nil)

// EnsureDefaults fills in the lifetimes and the session provider.
func (cfg *AuthConfig) EnsureDefaults() {
	if cfg.AccessTokenTTL <= 0 {
		cfg.AccessTokenTTL = DefaultAccessTokenTTL
	}

	if cfg.RefreshTokenTTL <= 0 {
		cfg.RefreshTokenTTL = DefaultRefreshTokenTTL
	}

	if cfg.CredentialSweepInterval <= 0 {
		cfg.CredentialSweepInterval = DefaultCredentialSweep
	}

	if cfg.PasswordReset.TokenLifetime <= 0 {
		cfg.PasswordReset.TokenLifetime = DefaultPasswordResetTTL
	}
}

// ValidateWithContext applies defaults and validates what survives.
func (cfg *AuthConfig) ValidateWithContext(ctx context.Context) error {
	cfg.EnsureDefaults()

	return validation.ValidateStructWithContext(ctx, cfg,
		validation.Field(&cfg.Sessions, validation.By(func(any) error {
			return cfg.Sessions.Platform().ValidateWithContext(ctx)
		})),
		validation.Field(&cfg.PasswordReset, validation.By(func(any) error {
			return cfg.PasswordReset.validate()
		})),
		validation.Field(&cfg.AccessTokenTTL, validation.Min(time.Duration(0))),
		validation.Field(&cfg.RefreshTokenTTL, validation.Min(time.Duration(0))),
	)
}

// validate refuses a link base that is not an absolute URL. Empty is allowed
// here — a config that sends no mail names none — and refused where a mailer is
// built.
func (cfg *PasswordResetConfig) validate() error {
	if cfg.LinkBaseURL == "" {
		return nil
	}

	u, err := url.Parse(cfg.LinkBaseURL)
	if err != nil {
		return platformerrors.Wrap(err, "parsing the password reset link base URL")
	}

	if !u.IsAbs() || u.Host == "" {
		return platformerrors.Wrapf(platformerrors.ErrUnrecognizedInputValue, "password reset link base URL %q is not absolute", cfg.LinkBaseURL)
	}

	return nil
}

// ResetLink is the URL a reset mail points at for the given secret.
func (cfg *PasswordResetConfig) ResetLink(secret string) (string, error) {
	u, err := url.Parse(cfg.LinkBaseURL)
	if err != nil {
		return "", platformerrors.Wrap(err, "parsing the password reset link base URL")
	}

	q := u.Query()
	q.Set("token", secret)
	u.RawQuery = q.Encode()

	return u.String(), nil
}
