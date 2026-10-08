// Package schedulegrpc serves fijo.v1.ScheduleService: the grid, the standing
// slots behind it, and the commands that change them.
//
// Reads go to the store and writes go through internal/domain/schedule's
// command layer, which puts the rows and the audit entry on one transaction.
// Nothing here decides what a command means; what it does is turn a request
// into the domain's vocabulary, resolve the caller, and turn a refusal into a
// status code a client can act on.
//
// Every method is declared public to the enforcer and refuses a request with
// no principal inside the method, which is the identity convention phase 0
// adopted. The scope every call runs under is the caller's studio, so a
// request can only ever reach the studio the caller is signed in to; there is
// no studio on the wire to get wrong.
package schedulegrpc

import (
	"context"
	"errors"

	"github.com/primandproper/fijo/internal/auth/principal"
	"github.com/primandproper/fijo/internal/domain/schedule"
	"github.com/primandproper/fijo/internal/grpc/fijopb"

	"github.com/primandproper/platform-go/v15/audit"
	authzgrpc "github.com/primandproper/primitives-go/v2/authorization/grpc"
	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	grpcerrors "github.com/primandproper/primitives-go/v2/errors/grpc"
	"github.com/primandproper/primitives-go/v2/filtering"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/tenancy"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

const serverName = "schedule_grpc"

// maxHistoryLimit is the most history one request may ask for. A request that
// asks for none gets the filtering package's own default.
const maxHistoryLimit = 200

// The refusals.
var (
	ErrNilStore          = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil schedule store")
	ErrNilCommands       = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil schedule commands")
	ErrNilDatabaseClient = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil database client")
	ErrNoPrincipal       = platformerrors.New("no principal on the request")
	ErrNoStudio          = platformerrors.New("the caller belongs to no studio")
	ErrNilRequest        = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil request body")

	// ErrNoHistory is ListHistory on a deployment with no audit log
	// configured. Everything else on this surface still works.
	ErrNoHistory = platformerrors.New("this deployment records no audit log")
)

// History is the slice of audit.Reader this server reads.
//
// It is an interface so the server can be built without one: a deployment that
// has not configured audit still serves the grid, and ListHistory is the only
// thing that stops working.
type History interface {
	List(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, query *audit.Query, filter *filtering.QueryFilter) (*filtering.QueryFilteredResult[audit.Entry], error)
}

// Server implements fijopb.ScheduleServiceServer.
type Server struct {
	fijopb.UnimplementedScheduleServiceServer

	store    schedule.Store
	commands *schedule.Commands
	client   database.Client
	history  History
	grid     Materializer
	o11y     observability.Observer
}

var _ fijopb.ScheduleServiceServer = (*Server)(nil)

// Materializer is the slice of *schedule.Materializer this server runs on a
// write, so an owner who edits a standing slot sees the grid change without
// waiting for the worker.
type Materializer interface {
	SyncSeries(ctx context.Context, tx database.Tx, scope tenancy.Scope, seriesID string) (written, retracted int64, err error)
	SyncStudio(ctx context.Context, tx database.Tx, scope tenancy.Scope) (int64, error)
}

// Option configures a Server.
type Option func(*Server)

// WithHistory attaches the audit reader ListHistory needs.
func WithHistory(reader History) Option {
	return func(s *Server) {
		if reader != nil {
			s.history = reader
		}
	}
}

// WithMaterializer attaches the materializer the series and settings writes
// run on their own transaction. Without one the grid waits for the worker.
func WithMaterializer(grid Materializer) Option {
	return func(s *Server) {
		if grid != nil {
			s.grid = grid
		}
	}
}

// syncSeries brings one series' standing rows into line with its rule.
func (s *Server) syncSeries(ctx context.Context, tx database.Tx, scope tenancy.Scope, seriesID string) error {
	if s.grid == nil {
		return nil
	}

	_, _, err := s.grid.SyncSeries(ctx, tx, scope, seriesID)

	return err
}

// syncStudio writes whatever the studio's rules produce that is not there yet.
func (s *Server) syncStudio(ctx context.Context, tx database.Tx, scope tenancy.Scope) error {
	if s.grid == nil {
		return nil
	}

	_, err := s.grid.SyncStudio(ctx, tx, scope)

	return err
}

// NewServer builds the server.
func NewServer(store schedule.Store, commands *schedule.Commands, client database.Client, pillars *observability.Pillars, opts ...Option) (*Server, error) {
	switch {
	case store == nil:
		return nil, ErrNilStore
	case commands == nil:
		return nil, ErrNilCommands
	case client == nil:
		return nil, ErrNilDatabaseClient
	}

	s := &Server{
		store:    store,
		commands: commands,
		client:   client,
		o11y:     observability.NewObserver(serverName, nil, nil),
	}

	if pillars != nil {
		s.o11y = observability.NewObserver(serverName, pillars.Logger, pillars.TracerProvider)
	}

	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}

	return s, nil
}

// RegisterOn mounts the service.
func (s *Server) RegisterOn(srv *grpc.Server) {
	fijopb.RegisterScheduleServiceServer(srv, s)
}

// Require declares every method public to the enforcer.
//
// Fijo's RPCs act on the caller's own studio and nobody else's — the scope
// comes off the principal and never off the wire — so there is no row here
// belonging to somebody the caller would need a permission to act on. The
// first Fijo permission arrives with the first RPC that takes a studio as an
// argument.
func Require(b *authzgrpc.RequirementsBuilder) *authzgrpc.RequirementsBuilder {
	if b == nil {
		return nil
	}

	for _, method := range []string{
		fijopb.ScheduleService_GetScheduleSettings_FullMethodName,
		fijopb.ScheduleService_SetScheduleSettings_FullMethodName,
		fijopb.ScheduleService_ListOfferings_FullMethodName,
		fijopb.ScheduleService_CreateOffering_FullMethodName,
		fijopb.ScheduleService_UpdateOffering_FullMethodName,
		fijopb.ScheduleService_ArchiveOffering_FullMethodName,
		fijopb.ScheduleService_ListSeries_FullMethodName,
		fijopb.ScheduleService_CreateSeries_FullMethodName,
		fijopb.ScheduleService_UpdateSeries_FullMethodName,
		fijopb.ScheduleService_EndSeries_FullMethodName,
		fijopb.ScheduleService_ListOccurrences_FullMethodName,
		fijopb.ScheduleService_CancelOccurrence_FullMethodName,
		fijopb.ScheduleService_CompleteOccurrence_FullMethodName,
		fijopb.ScheduleService_NoShowOccurrence_FullMethodName,
		fijopb.ScheduleService_RevertOccurrence_FullMethodName,
		fijopb.ScheduleService_MoveOccurrence_FullMethodName,
		fijopb.ScheduleService_ScheduleMakeUp_FullMethodName,
		fijopb.ScheduleService_ReassignOccurrence_FullMethodName,
		fijopb.ScheduleService_ListClosures_FullMethodName,
		fijopb.ScheduleService_CloseStudio_FullMethodName,
		fijopb.ScheduleService_ReopenStudio_FullMethodName,
		fijopb.ScheduleService_ListHistory_FullMethodName,
	} {
		b.Public(method)
	}

	return b
}

// caller resolves who is asking and which studio they are in.
func (s *Server) caller(ctx context.Context, op observability.Operation, method string) (*principal.Caller, tenancy.Scope, error) {
	c, ok := principal.FromContext(ctx)
	if !ok {
		return nil, tenancy.Scope{}, grpcerrors.PrepareAndLogGRPCStatus(ErrNoPrincipal, op.Logger(), op.Span(), codes.Unauthenticated, "resolving the caller of %s", method)
	}

	scope := c.StudioScope()
	if err := scope.Validate(); err != nil || scope.IsGlobal() {
		return nil, tenancy.Scope{}, grpcerrors.PrepareAndLogGRPCStatus(ErrNoStudio, op.Logger(), op.Span(), codes.FailedPrecondition, "resolving the studio of %s", method)
	}

	return c, scope, nil
}

// fail turns a domain refusal into the status a client can act on.
func (s *Server) fail(err error, op observability.Operation, description string) error {
	code := codes.Internal

	switch {
	case errors.Is(err, schedule.ErrNotFound):
		code = codes.NotFound
	case errors.Is(err, schedule.ErrStateTransitionRefused):
		code = codes.FailedPrecondition
	case errors.Is(err, schedule.ErrInvalidOffering),
		errors.Is(err, schedule.ErrInvalidSeries),
		errors.Is(err, schedule.ErrInvalidOccurrence),
		errors.Is(err, schedule.ErrInvalidClosure),
		errors.Is(err, schedule.ErrNilInput),
		errors.Is(err, schedule.ErrEmptyID),
		errors.Is(err, platformerrors.ErrUnrecognizedInputValue):
		code = codes.InvalidArgument
	}

	return grpcerrors.PrepareAndLogGRPCStatus(err, op.Logger(), op.Span(), code, "%s", description)
}

// unavailable refuses a surface that was built without the thing it reads.
func (s *Server) unavailable(op observability.Operation, err error, description string) error {
	return grpcerrors.PrepareAndLogGRPCStatus(err, op.Logger(), op.Span(), codes.Unimplemented, "%s", description)
}

func (s *Server) begin(ctx context.Context) (context.Context, observability.Operation, func()) {
	ctx, op := s.o11y.Begin(ctx)

	return ctx, op, op.End
}
