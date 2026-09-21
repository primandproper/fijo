// Package interceptor turns a bearer token back into a caller.
//
// It is the one authentication interceptor the deployment has. A request
// carrying an Authorization header is resolved or refused; a request carrying
// none passes through with no caller on it, and the authorization enforcer
// behind this interceptor decides whether the method it named admits nobody.
// That split is deliberate: sign-in, refresh and the passkey login are RPCs a
// caller reaches before there is anybody to authenticate, and refusing an
// absent header here would refuse the act of signing in.
//
// Resolution is four reads and each one is a door a revocation can close: the
// token parses and is unexpired; the session it names is live and belongs to
// its subject and family; the directory still admits the subject; and the
// policy resolves the roles the directory reports. The session read is also the
// touch that keeps an active owner signed in.
package interceptor

import (
	"context"
	"strings"

	"github.com/primandproper/fijo/internal/auth/principal"
	"github.com/primandproper/fijo/internal/auth/session"

	"github.com/primandproper/platform-go/v14/authentication/signin"
	"github.com/primandproper/platform-go/v14/identity"
	"github.com/primandproper/platform-go/v14/sessions"
	"github.com/primandproper/primitives-go/v2/authentication/tokens"
	"github.com/primandproper/primitives-go/v2/authorization"
	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/observability/logging"
	"github.com/primandproper/primitives-go/v2/observability/metrics"
	"github.com/primandproper/primitives-go/v2/observability/tracing"
	"github.com/primandproper/primitives-go/v2/tenancy"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

const (
	serviceName  = "auth_interceptor"
	headerName   = "authorization"
	bearerPrefix = "bearer "
)

// The refusals. Every one reaches a client as codes.Unauthenticated with the
// same wording, because telling a stolen token apart from an expired one is
// telling its holder which to try next; the distinction is kept for the log.
var (
	ErrMalformedHeader   = platformerrors.New("authorization header is not a bearer token")
	ErrTokenRejected     = platformerrors.New("token could not be parsed")
	ErrTokenNamesNoUser  = platformerrors.New("token names no subject")
	ErrNoSession         = platformerrors.New("token names no session")
	ErrSessionMismatch   = platformerrors.New("session does not belong to the token's subject or family")
	ErrNilTokenIssuer    = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil token issuer")
	ErrNilSessionStore   = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil session store")
	ErrNilDirectory      = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil identity store")
	ErrNilDatabaseClient = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil database client")
	ErrNilPolicy         = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil policy resolver")
)

// Directory is the slice of identity.Store the interceptor reads.
type Directory interface {
	GetPrincipal(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, userID, activeAccountID string) (*identity.Principal, error)
}

// Authenticator resolves tokens.
type Authenticator struct {
	issuer      tokens.Issuer
	sessions    sessions.Store[session.Data]
	directory   Directory
	client      database.Client
	policy      authorization.PolicyResolver
	o11y        observability.Observer
	instruments *metrics.OperationSet
}

// Option configures an Authenticator.
type Option func(*options)

type options struct {
	logger          logging.Logger
	tracerProvider  tracing.Provider
	metricsProvider metrics.Provider
}

// WithLogger attaches a logger.
func WithLogger(logger logging.Logger) Option {
	return func(o *options) { o.logger = logger }
}

// WithTracerProvider attaches a tracer provider.
func WithTracerProvider(tp tracing.Provider) Option {
	return func(o *options) { o.tracerProvider = tp }
}

// WithMetricsProvider attaches a metrics provider.
func WithMetricsProvider(mp metrics.Provider) Option {
	return func(o *options) { o.metricsProvider = mp }
}

// WithPillars supplies all three observability components at once.
func WithPillars(p *observability.Pillars) Option {
	return func(o *options) {
		if p == nil {
			return
		}

		o.logger, o.tracerProvider, o.metricsProvider = p.Logger, p.TracerProvider, p.MetricsProvider
	}
}

// New builds an Authenticator over the four things it reads.
func New(
	issuer tokens.Issuer,
	store sessions.Store[session.Data],
	directory Directory,
	client database.Client,
	policy authorization.PolicyResolver,
	opts ...Option,
) (*Authenticator, error) {
	switch {
	case issuer == nil:
		return nil, ErrNilTokenIssuer
	case store == nil:
		return nil, ErrNilSessionStore
	case directory == nil:
		return nil, ErrNilDirectory
	case client == nil:
		return nil, ErrNilDatabaseClient
	case policy == nil:
		return nil, ErrNilPolicy
	}

	o := &options{}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}

	instruments, err := metrics.NewOperationSet(o.metricsProvider, serviceName)
	if err != nil {
		return nil, platformerrors.Wrap(err, "creating authentication instruments")
	}

	return &Authenticator{
		issuer:      issuer,
		sessions:    store,
		directory:   directory,
		client:      client,
		policy:      policy,
		o11y:        observability.NewObserver(serviceName, o.logger, o.tracerProvider),
		instruments: instruments,
	}, nil
}

// Resolve turns a bearer token into a caller. It is exported for the handlers
// that receive a token in a request body rather than a header — none today —
// and for tests.
func (a *Authenticator) Resolve(ctx context.Context, token string) (caller *principal.Caller, err error) {
	ctx, op := a.o11y.Begin(ctx)
	defer op.End()

	a.instruments.Attempt(ctx)
	defer op.Time(ctx, nil, a.instruments.Latency)()

	defer func() {
		if err != nil {
			a.instruments.Failed(ctx)
		}
	}()

	claims, err := a.issuer.ParseToken(ctx, token)
	if err != nil {
		return nil, op.Error(platformerrors.Wrap(ErrTokenRejected, err.Error()), "parsing bearer token")
	}

	userID := claims.Subject()
	if userID == "" {
		return nil, op.Error(ErrTokenNamesNoUser, "reading the token subject")
	}

	op.Set("identity.user_id", userID)

	sessionID, _ := claims.GetString(session.ClaimSessionID)
	if sessionID == "" {
		return nil, op.Error(ErrNoSession, "reading the token's session")
	}

	familyID, _ := claims.GetString(signin.ClaimFamilyID)
	accountID, _ := claims.GetString(signin.ClaimAccountID)

	live, err := a.sessions.Get(ctx, sessionID)
	if err != nil {
		return nil, op.Error(err, "reading the session behind a token")
	}

	if live.Data == nil || live.Data.UserID != userID || live.Data.FamilyID != familyID {
		return nil, op.Error(ErrSessionMismatch, "checking the session against the token")
	}

	resolved, err := a.directory.GetPrincipal(ctx, a.client.Reader(), tenancy.Global(), userID, accountID)
	if err != nil {
		return nil, op.Error(err, "resolving the token's principal")
	}

	permissions, err := a.policy.PermissionsForRoles(ctx, resolved.Roles()...)
	if err != nil {
		return nil, op.Error(err, "resolving the principal's grants")
	}

	return &principal.Caller{
		Principal: resolved,
		Grants:    authorization.NewGrants(permissions),
		SessionID: sessionID,
	}, nil
}

// authenticate reads the header and, when one is present, resolves it onto the
// context. An absent header is not a failure here.
func (a *Authenticator) authenticate(ctx context.Context) (context.Context, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx, nil
	}

	values := md.Get(headerName)
	if len(values) == 0 || strings.TrimSpace(values[0]) == "" {
		return ctx, nil
	}

	raw := strings.TrimSpace(values[0])
	if len(raw) <= len(bearerPrefix) || !strings.EqualFold(raw[:len(bearerPrefix)], bearerPrefix) {
		return ctx, unauthenticated(ErrMalformedHeader)
	}

	caller, err := a.Resolve(ctx, strings.TrimSpace(raw[len(bearerPrefix):]))
	if err != nil {
		return ctx, unauthenticated(err)
	}

	return principal.With(ctx, caller), nil
}

// UnaryServerInterceptor authenticates unary calls.
func (a *Authenticator) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, err := a.authenticate(ctx)
		if err != nil {
			return nil, err
		}

		return handler(ctx, req)
	}
}

// StreamServerInterceptor authenticates streaming calls.
func (a *Authenticator) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, err := a.authenticate(ss.Context())
		if err != nil {
			return err
		}

		return handler(srv, &wrappedStream{ServerStream: ss, ctx: ctx})
	}
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }

// unauthenticated is the one answer a client gets. The cause stays on the
// error for the log and for tests, wrapped rather than replaced.
func unauthenticated(cause error) error {
	return observability.GRPCStatusError(cause, codes.Unauthenticated, "invalid or expired credentials")
}
