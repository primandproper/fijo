package interceptor

import (
	"context"
	"testing"
	"time"

	"github.com/primandproper/fijo/internal/auth/principal"
	"github.com/primandproper/fijo/internal/auth/session"

	"github.com/primandproper/platform-go/v14/authentication/signin"
	"github.com/primandproper/platform-go/v14/identity"
	identitymock "github.com/primandproper/platform-go/v14/identity/mock"
	"github.com/primandproper/platform-go/v14/sessions"
	sessionsmock "github.com/primandproper/platform-go/v14/sessions/mock"
	"github.com/primandproper/primitives-go/v2/authentication/tokens"
	tokensmock "github.com/primandproper/primitives-go/v2/authentication/tokens/mock"
	"github.com/primandproper/primitives-go/v2/authorization"
	authorizationmock "github.com/primandproper/primitives-go/v2/authorization/mock"
	"github.com/primandproper/primitives-go/v2/database"
	databasemock "github.com/primandproper/primitives-go/v2/database/mock"
	"github.com/primandproper/primitives-go/v2/tenancy"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	userID    = "user-1"
	studioID  = "studio-1"
	familyID  = "family-1"
	sessionID = "session-1"
	goodToken = "good"
)

// claims is a parsed token with the four claims the interceptor reads.
func claims(values map[string]string) tokens.Claims {
	return &tokensmock.ClaimsMock{
		SubjectFunc: func() string { return values["sub"] },
		JTIFunc:     func() string { return "jti" },
		ExpiresAtFunc: func() time.Time {
			return time.Now().Add(time.Hour)
		},
		GetStringFunc: func(key string) (string, bool) {
			v, ok := values[key]

			return v, ok
		},
		GetFunc: func(key string) (any, bool) {
			v, ok := values[key]

			return v, ok
		},
	}
}

type fixture struct {
	issuer    *tokensmock.IssuerMock
	sessions  *sessionsmock.StoreMock[session.Data]
	directory *identitymock.StoreMock
	policy    *authorizationmock.PolicyResolverMock
}

func newFixture(t *testing.T) (*Authenticator, *fixture) {
	t.Helper()

	f := &fixture{
		issuer: &tokensmock.IssuerMock{
			ParseTokenFunc: func(_ context.Context, token string) (tokens.Claims, error) {
				if token != goodToken {
					return nil, tokens.ErrTokenExpired
				}

				return claims(map[string]string{
					"sub":                  userID,
					session.ClaimSessionID: sessionID,
					signin.ClaimFamilyID:   familyID,
					signin.ClaimAccountID:  studioID,
				}), nil
			},
		},
		sessions: &sessionsmock.StoreMock[session.Data]{
			GetFunc: func(_ context.Context, id string) (*sessions.Session[session.Data], error) {
				if id != sessionID {
					return nil, sessions.ErrNotFound
				}

				return &sessions.Session[session.Data]{ID: id, Data: &session.Data{UserID: userID, FamilyID: familyID}}, nil
			},
		},
		directory: &identitymock.StoreMock{
			GetPrincipalFunc: func(_ context.Context, _ database.SQLQueryExecutor, scope tenancy.Scope, id, account string) (*identity.Principal, error) {
				test.Eq(t, tenancy.Global(), scope)
				test.Eq(t, studioID, account)

				return &identity.Principal{
					User:            &identity.User{ID: id, Scope: scope, AccountStatus: identity.StatusGood},
					ActiveAccountID: account,
					Memberships:     []*identity.Membership{{BelongsToAccount: account, Roles: []string{"owner"}}},
				}, nil
			},
		},
		policy: &authorizationmock.PolicyResolverMock{
			PermissionsForRolesFunc: func(_ context.Context, roles ...string) (*authorization.PermissionSet, error) {
				test.Eq(t, []string{"owner"}, roles)

				return authorization.NewPermissionSet("identity.accounts.read"), nil
			},
		},
	}

	client := &databasemock.ClientMock{ReaderFunc: func() database.SQLQueryExecutor { return nil }}

	auth, err := New(f.issuer, f.sessions, f.directory, client, f.policy)
	must.NoError(t, err)

	return auth, f
}

func ctxWithHeader(value string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", value))
}

func invoke(auth *Authenticator, ctx context.Context) (*principal.Caller, error) {
	var seen *principal.Caller

	_, err := auth.UnaryServerInterceptor()(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/x/Y"}, func(ctx context.Context, _ any) (any, error) {
		seen, _ = principal.FromContext(ctx)

		return nil, nil
	})

	return seen, err
}

func TestAuthenticator(t *testing.T) {
	t.Parallel()

	t.Run("a good token becomes a caller with grants", func(t *testing.T) {
		t.Parallel()

		auth, _ := newFixture(t)

		caller, err := invoke(auth, ctxWithHeader("Bearer "+goodToken))
		must.NoError(t, err)
		must.NotNil(t, caller)

		test.Eq(t, userID, caller.UserID())
		test.Eq(t, studioID, caller.ActiveAccountID())
		test.Eq(t, sessionID, caller.SessionID)
		test.True(t, caller.Grants.Has("identity.accounts.read"))
	})

	t.Run("no header passes through with nobody on the context", func(t *testing.T) {
		t.Parallel()

		auth, f := newFixture(t)

		caller, err := invoke(auth, context.Background())
		must.NoError(t, err)
		test.Nil(t, caller)
		test.SliceLen(t, 0, f.issuer.ParseTokenCalls())
	})

	t.Run("a malformed header is refused", func(t *testing.T) {
		t.Parallel()

		auth, _ := newFixture(t)

		_, err := invoke(auth, ctxWithHeader("Basic abc"))
		must.Error(t, err)
		test.Eq(t, codes.Unauthenticated, status.Code(err))
	})

	t.Run("a token the issuer rejects is refused", func(t *testing.T) {
		t.Parallel()

		auth, _ := newFixture(t)

		_, err := invoke(auth, ctxWithHeader("Bearer forged"))
		must.Error(t, err)
		test.Eq(t, codes.Unauthenticated, status.Code(err))
		test.ErrorIs(t, err, ErrTokenRejected)
	})

	t.Run("a revoked session is refused", func(t *testing.T) {
		t.Parallel()

		auth, f := newFixture(t)
		f.sessions.GetFunc = func(context.Context, string) (*sessions.Session[session.Data], error) {
			return nil, sessions.ErrNotFound
		}

		_, err := invoke(auth, ctxWithHeader("Bearer "+goodToken))
		must.Error(t, err)
		test.Eq(t, codes.Unauthenticated, status.Code(err))
		test.ErrorIs(t, err, sessions.ErrNotFound)
	})

	t.Run("a session for another user or family is refused", func(t *testing.T) {
		t.Parallel()

		auth, f := newFixture(t)
		f.sessions.GetFunc = func(_ context.Context, id string) (*sessions.Session[session.Data], error) {
			return &sessions.Session[session.Data]{ID: id, Data: &session.Data{UserID: "somebody-else", FamilyID: familyID}}, nil
		}

		_, err := invoke(auth, ctxWithHeader("Bearer "+goodToken))
		must.Error(t, err)
		test.ErrorIs(t, err, ErrSessionMismatch)
	})

	t.Run("a suspended user is refused on the next request", func(t *testing.T) {
		t.Parallel()

		auth, f := newFixture(t)
		f.directory.GetPrincipalFunc = func(context.Context, database.SQLQueryExecutor, tenancy.Scope, string, string) (*identity.Principal, error) {
			return nil, identity.ErrSignInNotAdmitted
		}

		_, err := invoke(auth, ctxWithHeader("Bearer "+goodToken))
		must.Error(t, err)
		test.Eq(t, codes.Unauthenticated, status.Code(err))
		test.ErrorIs(t, err, identity.ErrSignInNotAdmitted)
	})

	t.Run("constructor refuses a missing dependency", func(t *testing.T) {
		t.Parallel()

		_, f := newFixture(t)
		client := &databasemock.ClientMock{}

		_, err := New(nil, f.sessions, f.directory, client, f.policy)
		test.ErrorIs(t, err, ErrNilTokenIssuer)

		_, err = New(f.issuer, nil, f.directory, client, f.policy)
		test.ErrorIs(t, err, ErrNilSessionStore)

		_, err = New(f.issuer, f.sessions, nil, client, f.policy)
		test.ErrorIs(t, err, ErrNilDirectory)

		_, err = New(f.issuer, f.sessions, f.directory, nil, f.policy)
		test.ErrorIs(t, err, ErrNilDatabaseClient)

		_, err = New(f.issuer, f.sessions, f.directory, client, nil)
		test.ErrorIs(t, err, ErrNilPolicy)
	})
}
