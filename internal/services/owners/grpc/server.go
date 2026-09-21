// Package ownersgrpc serves fijo.v1.OwnersService: who the caller is, as the
// frontend reads it, and the one preference Fijo keeps about them.
//
// Both RPCs are self-service and declared public to the enforcer; each refuses
// a request with no principal inside the method.
package ownersgrpc

import (
	"context"
	"errors"
	"strings"

	"github.com/primandproper/fijo/internal/auth/principal"
	"github.com/primandproper/fijo/internal/domain/owners"
	"github.com/primandproper/fijo/internal/grpc/fijopb"

	"github.com/primandproper/platform-go/v14/identity"
	identitygrpc "github.com/primandproper/platform-go/v14/identity/grpc"
	authzgrpc "github.com/primandproper/primitives-go/v2/authorization/grpc"
	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	grpcerrors "github.com/primandproper/primitives-go/v2/errors/grpc"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/tenancy"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

const serverName = "owners_grpc"

// The refusals.
var (
	ErrNilStore          = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil owners store")
	ErrNilDirectory      = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil directory")
	ErrNilDatabaseClient = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil database client")
	ErrNoPrincipal       = platformerrors.New("no principal on the request")
)

// Directory is the slice of identity.Store the server reads.
type Directory interface {
	GetAccount(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, accountID string) (*identity.Account, error)
}

// Server implements fijopb.OwnersServiceServer.
type Server struct {
	fijopb.UnimplementedOwnersServiceServer

	store     owners.Store
	directory Directory
	client    database.Client
	o11y      observability.Observer
}

var _ fijopb.OwnersServiceServer = (*Server)(nil)

// NewServer builds the server.
func NewServer(store owners.Store, directory Directory, client database.Client, pillars *observability.Pillars) (*Server, error) {
	switch {
	case store == nil:
		return nil, ErrNilStore
	case directory == nil:
		return nil, ErrNilDirectory
	case client == nil:
		return nil, ErrNilDatabaseClient
	}

	s := &Server{
		store:     store,
		directory: directory,
		client:    client,
		o11y:      observability.NewObserver(serverName, nil, nil),
	}

	if pillars != nil {
		s.o11y = observability.NewObserver(serverName, pillars.Logger, pillars.TracerProvider)
	}

	return s, nil
}

// RegisterOn puts the service on a gRPC server.
func (s *Server) RegisterOn(srv *grpc.Server) {
	fijopb.RegisterOwnersServiceServer(srv, s)
}

// Require declares both methods public to the enforcer.
func Require(b *authzgrpc.RequirementsBuilder) *authzgrpc.RequirementsBuilder {
	if b == nil {
		return nil
	}

	b.Public(fijopb.OwnersService_GetSelf_FullMethodName)
	b.Public(fijopb.OwnersService_SetTimeZone_FullMethodName)

	return b
}

func (s *Server) caller(ctx context.Context, op observability.Operation, method string) (*principal.Caller, error) {
	c, ok := principal.FromContext(ctx)
	if !ok {
		return nil, grpcerrors.PrepareAndLogGRPCStatus(ErrNoPrincipal, op.Logger(), op.Span(), codes.Unauthenticated, "resolving the caller of %s", method)
	}

	op.Set("identity.user_id", c.UserID())

	return c, nil
}

// GetSelf implements fijopb.OwnersServiceServer.
func (s *Server) GetSelf(ctx context.Context, _ *fijopb.GetSelfRequest) (*fijopb.GetSelfResponse, error) {
	ctx, op := s.o11y.Begin(ctx)
	defer op.End()

	c, err := s.caller(ctx, op, "GetSelf")
	if err != nil {
		return nil, err
	}

	out := &fijopb.GetSelfResponse{
		User:  identitygrpc.UserToProto(c.Principal.User),
		Roles: c.Principal.Roles(),
	}

	if c.ActiveAccountID() != "" {
		studio, readErr := s.directory.GetAccount(ctx, s.client.Reader(), c.Scope(), c.ActiveAccountID())
		if readErr != nil {
			return nil, grpcerrors.PrepareAndLogGRPCStatus(readErr, op.Logger(), op.Span(), codes.Internal, "reading the caller's studio")
		}

		out.Studio = identitygrpc.AccountToProto(studio)
	}

	prefs, err := s.store.GetPreferences(ctx, s.client.Reader(), c.Scope(), c.UserID())
	if err != nil && !errors.Is(err, owners.ErrPreferencesNotFound) {
		return nil, grpcerrors.PrepareAndLogGRPCStatus(err, op.Logger(), op.Span(), codes.Internal, "reading the caller's preferences")
	}

	if prefs != nil {
		out.TimeZone = prefs.TimeZone
	}

	return out, nil
}

// SetTimeZone implements fijopb.OwnersServiceServer.
func (s *Server) SetTimeZone(ctx context.Context, req *fijopb.SetTimeZoneRequest) (*fijopb.SetTimeZoneResponse, error) {
	ctx, op := s.o11y.Begin(ctx)
	defer op.End()

	c, err := s.caller(ctx, op, "SetTimeZone")
	if err != nil {
		return nil, err
	}

	zone := strings.TrimSpace(req.GetTimeZone())

	if _, err = owners.LoadTimeZone(zone); err != nil {
		return nil, grpcerrors.PrepareAndLogGRPCStatus(err, op.Logger(), op.Span(), codes.InvalidArgument, "checking the time zone")
	}

	var written *owners.Preferences

	if err = s.client.WithTransaction(ctx, func(tx database.Tx) error {
		var setErr error

		written, setErr = s.store.SetTimeZone(ctx, tx, c.Scope(), c.UserID(), zone)

		return setErr
	}); err != nil {
		return nil, grpcerrors.PrepareAndLogGRPCStatus(err, op.Logger(), op.Span(), codes.Internal, "setting the time zone")
	}

	return &fijopb.SetTimeZoneResponse{TimeZone: written.TimeZone}, nil
}
