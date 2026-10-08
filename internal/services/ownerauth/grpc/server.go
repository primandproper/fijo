// Package ownerauthgrpc serves fijo.v1.OwnerAuthService: the passkey
// ceremonies, the password reset and signing out. Password sign-in and the
// refresh exchange are platform-go's signin surface, mounted beside this one.
//
// Every RPC here is declared public to the authorization enforcer, because
// each is either a door — a login, a reset — or self-service on the caller's
// own rows. The self-service ones refuse a request with no principal inside
// the method, which is the identity convention for RPCs whose whole
// authorization is "this is your own".
package ownerauthgrpc

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/primandproper/fijo/internal/auth/mint"
	"github.com/primandproper/fijo/internal/auth/passkeyflow"
	"github.com/primandproper/fijo/internal/auth/principal"
	"github.com/primandproper/fijo/internal/auth/session"
	"github.com/primandproper/fijo/internal/grpc/fijopb"

	"github.com/primandproper/platform-go/v15/authentication/passkeys"
	"github.com/primandproper/platform-go/v15/authentication/passwordreset"
	"github.com/primandproper/platform-go/v15/authentication/signin"
	"github.com/primandproper/platform-go/v15/authentication/signin/signinpb"
	"github.com/primandproper/platform-go/v15/sessions"
	authzgrpc "github.com/primandproper/primitives-go/v2/authorization/grpc"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	grpcerrors "github.com/primandproper/primitives-go/v2/errors/grpc"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/tenancy"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const serverName = "owner_auth_grpc"

// The refusals.
var (
	ErrNilPasskeys      = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil passkey flow")
	ErrNilMinter        = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil minter")
	ErrNilPasswordReset = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil password reset service")
	ErrNilSignIn        = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil sign-in service")
	ErrNilSessions      = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil session store")
	ErrNoPrincipal      = platformerrors.New("no principal on the request")
)

// Server implements fijopb.OwnerAuthServiceServer.
type Server struct {
	fijopb.UnimplementedOwnerAuthServiceServer

	passkeys      *passkeyflow.Service
	minter        *mint.Minter
	passwordReset *passwordreset.Service
	signIn        *signin.Service
	sessions      sessions.Store[session.Data]
	o11y          observability.Observer
}

var _ fijopb.OwnerAuthServiceServer = (*Server)(nil)

// NewServer builds the server.
func NewServer(
	flow *passkeyflow.Service,
	minter *mint.Minter,
	reset *passwordreset.Service,
	signIn *signin.Service,
	store sessions.Store[session.Data],
	pillars *observability.Pillars,
) (*Server, error) {
	switch {
	case flow == nil:
		return nil, ErrNilPasskeys
	case minter == nil:
		return nil, ErrNilMinter
	case reset == nil:
		return nil, ErrNilPasswordReset
	case signIn == nil:
		return nil, ErrNilSignIn
	case store == nil:
		return nil, ErrNilSessions
	}

	s := &Server{
		passkeys:      flow,
		minter:        minter,
		passwordReset: reset,
		signIn:        signIn,
		sessions:      store,
		o11y:          observability.NewObserver(serverName, nil, nil),
	}

	if pillars != nil {
		s.o11y = observability.NewObserver(serverName, pillars.Logger, pillars.TracerProvider)
	}

	return s, nil
}

// RegisterOn puts the service on a gRPC server.
func (s *Server) RegisterOn(srv *grpc.Server) {
	fijopb.RegisterOwnerAuthServiceServer(srv, s)
}

// Require declares every method public to the enforcer; see the package
// documentation for why.
func Require(b *authzgrpc.RequirementsBuilder) *authzgrpc.RequirementsBuilder {
	if b == nil {
		return nil
	}

	for _, method := range []string{
		fijopb.OwnerAuthService_BeginPasskeyRegistration_FullMethodName,
		fijopb.OwnerAuthService_FinishPasskeyRegistration_FullMethodName,
		fijopb.OwnerAuthService_ListPasskeys_FullMethodName,
		fijopb.OwnerAuthService_ArchivePasskey_FullMethodName,
		fijopb.OwnerAuthService_BeginPasskeyLogin_FullMethodName,
		fijopb.OwnerAuthService_FinishPasskeyLogin_FullMethodName,
		fijopb.OwnerAuthService_RequestPasswordReset_FullMethodName,
		fijopb.OwnerAuthService_CompletePasswordReset_FullMethodName,
		fijopb.OwnerAuthService_SignOut_FullMethodName,
	} {
		b.Public(method)
	}

	return b
}

// scope is the identity directory every sign-in reads. Fijo has one.
func scope() tenancy.Scope { return tenancy.Global() }

// caller is the self-service door: a principal or Unauthenticated.
func (s *Server) caller(ctx context.Context, op observability.Operation, method string) (*principal.Caller, error) {
	c, ok := principal.FromContext(ctx)
	if !ok {
		return nil, grpcerrors.PrepareAndLogGRPCStatus(ErrNoPrincipal, op.Logger(), op.Span(), codes.Unauthenticated, "resolving the caller of %s", method)
	}

	op.Set("identity.user_id", c.UserID())

	return c, nil
}

func (s *Server) refuse(op observability.Operation, err error, code codes.Code, what string) error {
	return grpcerrors.PrepareAndLogGRPCStatus(err, op.Logger(), op.Span(), code, "%s", what)
}

// BeginPasskeyRegistration implements fijopb.OwnerAuthServiceServer.
func (s *Server) BeginPasskeyRegistration(ctx context.Context, _ *fijopb.BeginPasskeyRegistrationRequest) (*fijopb.BeginPasskeyRegistrationResponse, error) {
	ctx, op := s.o11y.Begin(ctx)
	defer op.End()

	c, err := s.caller(ctx, op, "BeginPasskeyRegistration")
	if err != nil {
		return nil, err
	}

	creation, err := s.passkeys.BeginRegistration(ctx, scope(), c.UserID())
	if err != nil {
		return nil, s.refuse(op, err, codes.Internal, "beginning passkey registration")
	}

	options, err := json.Marshal(creation)
	if err != nil {
		return nil, s.refuse(op, err, codes.Internal, "encoding passkey registration options")
	}

	return &fijopb.BeginPasskeyRegistrationResponse{OptionsJson: options}, nil
}

// FinishPasskeyRegistration implements fijopb.OwnerAuthServiceServer.
func (s *Server) FinishPasskeyRegistration(ctx context.Context, req *fijopb.FinishPasskeyRegistrationRequest) (*fijopb.FinishPasskeyRegistrationResponse, error) {
	ctx, op := s.o11y.Begin(ctx)
	defer op.End()

	c, err := s.caller(ctx, op, "FinishPasskeyRegistration")
	if err != nil {
		return nil, err
	}

	if len(req.GetAttestationJson()) == 0 {
		return nil, s.refuse(op, passkeyflow.ErrEmptyResponse, codes.InvalidArgument, "finishing passkey registration")
	}

	stored, err := s.passkeys.FinishRegistration(ctx, scope(), c.UserID(), strings.TrimSpace(req.GetFriendlyName()), req.GetAttestationJson())
	if err != nil {
		return nil, s.refuse(op, err, codes.InvalidArgument, "finishing passkey registration")
	}

	return &fijopb.FinishPasskeyRegistrationResponse{Passkey: passkeyToProto(stored)}, nil
}

// ListPasskeys implements fijopb.OwnerAuthServiceServer.
func (s *Server) ListPasskeys(ctx context.Context, _ *fijopb.ListPasskeysRequest) (*fijopb.ListPasskeysResponse, error) {
	ctx, op := s.o11y.Begin(ctx)
	defer op.End()

	c, err := s.caller(ctx, op, "ListPasskeys")
	if err != nil {
		return nil, err
	}

	credentials, err := s.passkeys.List(ctx, scope(), c.UserID())
	if err != nil {
		return nil, s.refuse(op, err, codes.Internal, "listing passkeys")
	}

	out := make([]*fijopb.Passkey, 0, len(credentials))
	for _, credential := range credentials {
		out = append(out, passkeyToProto(credential))
	}

	return &fijopb.ListPasskeysResponse{Passkeys: out}, nil
}

// ArchivePasskey implements fijopb.OwnerAuthServiceServer.
func (s *Server) ArchivePasskey(ctx context.Context, req *fijopb.ArchivePasskeyRequest) (*fijopb.ArchivePasskeyResponse, error) {
	ctx, op := s.o11y.Begin(ctx)
	defer op.End()

	c, err := s.caller(ctx, op, "ArchivePasskey")
	if err != nil {
		return nil, err
	}

	if req.GetId() == "" {
		return nil, s.refuse(op, platformerrors.ErrInvalidIDProvided, codes.InvalidArgument, "archiving a passkey")
	}

	archived, err := s.passkeys.Archive(ctx, scope(), c.UserID(), req.GetId())
	if err != nil {
		return nil, s.refuse(op, err, codes.NotFound, "archiving a passkey")
	}

	return &fijopb.ArchivePasskeyResponse{Passkey: passkeyToProto(archived)}, nil
}

// BeginPasskeyLogin implements fijopb.OwnerAuthServiceServer.
func (s *Server) BeginPasskeyLogin(ctx context.Context, req *fijopb.BeginPasskeyLoginRequest) (*fijopb.BeginPasskeyLoginResponse, error) {
	ctx, op := s.o11y.Begin(ctx)
	defer op.End()

	assertion, err := s.passkeys.BeginLogin(ctx, scope(), strings.TrimSpace(req.GetUsername()))
	if err != nil {
		// One answer for an unknown username and a known one with no passkeys,
		// so the door does not say who is behind it.
		return nil, s.refuse(op, err, codes.Unauthenticated, "beginning passkey login")
	}

	options, err := json.Marshal(assertion)
	if err != nil {
		return nil, s.refuse(op, err, codes.Internal, "encoding passkey login options")
	}

	return &fijopb.BeginPasskeyLoginResponse{OptionsJson: options}, nil
}

// FinishPasskeyLogin implements fijopb.OwnerAuthServiceServer.
func (s *Server) FinishPasskeyLogin(ctx context.Context, req *fijopb.FinishPasskeyLoginRequest) (*fijopb.FinishPasskeyLoginResponse, error) {
	ctx, op := s.o11y.Begin(ctx)
	defer op.End()

	if len(req.GetAssertionJson()) == 0 {
		return nil, s.refuse(op, passkeyflow.ErrEmptyResponse, codes.InvalidArgument, "finishing passkey login")
	}

	userID, err := s.passkeys.FinishLogin(ctx, scope(), strings.TrimSpace(req.GetUsername()), req.GetAssertionJson())
	if err != nil {
		return nil, s.refuse(op, err, codes.Unauthenticated, "finishing passkey login")
	}

	signedIn, err := s.minter.IssueForUser(ctx, scope(), userID, "")
	if err != nil {
		return nil, s.refuse(op, err, codes.Unauthenticated, "issuing a token for a passkey login")
	}

	return &fijopb.FinishPasskeyLoginResponse{Token: issuedTokenToProto(signedIn)}, nil
}

// RequestPasswordReset implements fijopb.OwnerAuthServiceServer. It answers
// the same way whether or not the address is known; the platform pads the
// unknown case to the known one's duration.
func (s *Server) RequestPasswordReset(ctx context.Context, req *fijopb.RequestPasswordResetRequest) (*fijopb.RequestPasswordResetResponse, error) {
	ctx, op := s.o11y.Begin(ctx)
	defer op.End()

	address := strings.TrimSpace(req.GetEmailAddress())
	if address == "" {
		return nil, s.refuse(op, passwordreset.ErrEmptyEmailAddress, codes.InvalidArgument, "requesting a password reset")
	}

	if err := s.passwordReset.Request(ctx, scope(), address); err != nil {
		return nil, s.refuse(op, err, codes.Internal, "requesting a password reset")
	}

	return &fijopb.RequestPasswordResetResponse{}, nil
}

// CompletePasswordReset implements fijopb.OwnerAuthServiceServer.
func (s *Server) CompletePasswordReset(ctx context.Context, req *fijopb.CompletePasswordResetRequest) (*fijopb.CompletePasswordResetResponse, error) {
	ctx, op := s.o11y.Begin(ctx)
	defer op.End()

	if _, err := s.passwordReset.Complete(ctx, scope(), strings.TrimSpace(req.GetToken()), req.GetNewPassword()); err != nil {
		return nil, s.refuse(op, err, codes.InvalidArgument, "completing a password reset")
	}

	return &fijopb.CompletePasswordResetResponse{}, nil
}

// SignOut implements fijopb.OwnerAuthServiceServer. It ends the session the
// token named and revokes its family, so neither this token nor a refresh of
// it works again; the platform's sign-in surface has no sign-out because it
// keeps no session.
func (s *Server) SignOut(ctx context.Context, _ *fijopb.SignOutRequest) (*fijopb.SignOutResponse, error) {
	ctx, op := s.o11y.Begin(ctx)
	defer op.End()

	c, err := s.caller(ctx, op, "SignOut")
	if err != nil {
		return nil, err
	}

	live, err := s.sessions.Get(ctx, c.SessionID)
	if err != nil {
		return nil, s.refuse(op, err, codes.Unauthenticated, "reading the session being ended")
	}

	if live.Data != nil && live.Data.FamilyID != "" {
		if _, err = s.signIn.RevokeRefreshTokenFamily(ctx, scope(), live.Data.FamilyID); err != nil {
			return nil, s.refuse(op, err, codes.Internal, "revoking the refresh-token family")
		}
	}

	if err = s.sessions.Delete(ctx, c.SessionID); err != nil {
		return nil, s.refuse(op, err, codes.Internal, "ending the session")
	}

	return &fijopb.SignOutResponse{}, nil
}

func passkeyToProto(c *passkeys.Credential) *fijopb.Passkey {
	if c == nil {
		return nil
	}

	out := &fijopb.Passkey{
		Id:           c.ID,
		FriendlyName: c.FriendlyName,
		Transports:   c.Transports,
		CreatedAt:    timestamppb.New(c.CreatedAt),
	}

	if c.LastUsedAt != nil {
		out.LastUsedAt = timestamppb.New(*c.LastUsedAt)
	}

	return out
}

// issuedTokenToProto renders a sign-in as the platform's IssuedToken, so the
// frontend handles one shape whichever door minted it.
func issuedTokenToProto(s *signin.SignIn) *signinpb.IssuedToken {
	if s == nil {
		return nil
	}

	out := &signinpb.IssuedToken{
		Token:          s.Token,
		TokenId:        s.TokenID,
		ExpiresAt:      timestamppb.New(s.ExpiresAt),
		Administrative: s.Administrative,
		RefreshToken:   s.RefreshToken,
		FamilyId:       s.FamilyID,
	}

	if s.Principal != nil {
		out.ActiveAccountId = s.Principal.ActiveAccountID
	}

	if !s.RefreshTokenExpiresAt.IsZero() {
		out.RefreshTokenExpiresAt = timestamppb.New(s.RefreshTokenExpiresAt)
	}

	return out
}
