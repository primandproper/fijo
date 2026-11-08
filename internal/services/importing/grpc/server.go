// Package importinggrpc serves fijo.v1.ImportService: bringing a studio's
// clients and standing lessons over from Acuity.
//
// Like the roster's and the schedule's servers, every method is public to the
// enforcer and runs under the caller's own studio. Preview reads; apply writes
// everything on one transaction, so an import that fails part-way writes
// nothing.
package importinggrpc

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/primandproper/fijo/internal/auth/principal"
	"github.com/primandproper/fijo/internal/domain/roster"
	"github.com/primandproper/fijo/internal/domain/schedule"
	"github.com/primandproper/fijo/internal/grpc/fijopb"
	"github.com/primandproper/fijo/internal/importing"
	"github.com/primandproper/fijo/internal/importing/acuity"

	authzgrpc "github.com/primandproper/primitives-go/v2/authorization/grpc"
	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	grpcerrors "github.com/primandproper/primitives-go/v2/errors/grpc"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/tenancy"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

const serverName = "importing_grpc"

// maxFileBytes is the most either export may be. A studio of a few hundred
// clients and a season of appointments is well under a megabyte.
const maxFileBytes = 3 << 20

// The refusals.
var (
	ErrNilWriter         = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil import writer")
	ErrNilZones          = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil zone lookup")
	ErrNilDatabaseClient = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil database client")
	ErrNoPrincipal       = platformerrors.New("no principal on the request")
	ErrNoStudio          = platformerrors.New("the caller belongs to no studio")
	ErrNilRequest        = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil request body")
	ErrTooLarge          = platformerrors.New("export too large")
)

// Server implements fijopb.ImportServiceServer.
type Server struct {
	fijopb.UnimplementedImportServiceServer

	writer *importing.Writer
	zones  schedule.Zones
	client database.Client
	o11y   observability.Observer
	now    func() time.Time
}

var _ fijopb.ImportServiceServer = (*Server)(nil)

// NewServer builds the server.
func NewServer(writer *importing.Writer, zones schedule.Zones, client database.Client, pillars *observability.Pillars) (*Server, error) {
	switch {
	case writer == nil:
		return nil, ErrNilWriter
	case zones == nil:
		return nil, ErrNilZones
	case client == nil:
		return nil, ErrNilDatabaseClient
	}

	s := &Server{
		writer: writer,
		zones:  zones,
		client: client,
		o11y:   observability.NewObserver(serverName, nil, nil),
		now:    time.Now,
	}

	if pillars != nil {
		s.o11y = observability.NewObserver(serverName, pillars.Logger, pillars.TracerProvider)
	}

	return s, nil
}

// RegisterOn mounts the service.
func (s *Server) RegisterOn(srv *grpc.Server) {
	fijopb.RegisterImportServiceServer(srv, s)
}

// Require declares every method public to the enforcer: the scope comes off
// the principal, never the wire.
func Require(b *authzgrpc.RequirementsBuilder) *authzgrpc.RequirementsBuilder {
	if b == nil {
		return nil
	}

	b.Public(fijopb.ImportService_PreviewAcuityImport_FullMethodName)
	b.Public(fijopb.ImportService_ApplyAcuityImport_FullMethodName)

	return b
}

// PreviewAcuityImport implements fijopb.ImportServiceServer.
func (s *Server) PreviewAcuityImport(ctx context.Context, request *fijopb.PreviewAcuityImportRequest) (*fijopb.ImportPreview, error) {
	ctx, op := s.o11y.Begin(ctx)
	defer op.End()

	_, scope, err := s.caller(ctx, op, fijopb.ImportService_PreviewAcuityImport_FullMethodName)
	if err != nil {
		return nil, err
	}

	plan, err := s.plan(ctx, scope, request.GetFiles())
	if err != nil {
		return nil, s.fail(err, op, "reading the export")
	}

	if err = s.writer.Annotate(ctx, s.client.Reader(), scope, plan); err != nil {
		return nil, s.fail(err, op, "comparing the export with the roster")
	}

	return previewToProto(plan), nil
}

// ApplyAcuityImport implements fijopb.ImportServiceServer.
func (s *Server) ApplyAcuityImport(ctx context.Context, request *fijopb.ApplyAcuityImportRequest) (*fijopb.ApplyAcuityImportResponse, error) {
	ctx, op := s.o11y.Begin(ctx)
	defer op.End()

	caller, scope, err := s.caller(ctx, op, fijopb.ImportService_ApplyAcuityImport_FullMethodName)
	if err != nil {
		return nil, err
	}

	plan, err := s.plan(ctx, scope, request.GetFiles())
	if err != nil {
		return nil, s.fail(err, op, "reading the export")
	}

	choices := importing.Choices{
		Staff:           []string{caller.UserID()},
		SkipHouseholds:  keySet(request.GetSkipHouseholdKeys()),
		SkipLessons:     keySet(request.GetSkipLessonKeys()),
		IncludeInactive: request.GetIncludeInactive(),
	}

	var result *importing.Result

	if err = s.client.WithTransaction(ctx, func(tx database.Tx) error {
		var writeErr error

		result, writeErr = s.writer.Write(ctx, tx, scope, plan, choices)

		return writeErr
	}); err != nil {
		return nil, s.fail(err, op, "importing")
	}

	return &fijopb.ApplyAcuityImportResponse{
		HouseholdsWritten:  int32(result.HouseholdsWritten),
		HouseholdsOnRoster: int32(result.HouseholdsOnRoster),
		HouseholdsSkipped:  int32(result.HouseholdsSkipped),
		OfferingsWritten:   int32(result.OfferingsWritten),
		LessonsWritten:     int32(result.LessonsWritten),
	}, nil
}

// plan reads the files against the studio's today.
func (s *Server) plan(ctx context.Context, scope tenancy.Scope, files *fijopb.AcuityFiles) (*importing.Plan, error) {
	if files == nil {
		return nil, ErrNilRequest
	}

	if len(files.GetClientsCsv()) > maxFileBytes || len(files.GetAppointmentsCsv()) > maxFileBytes {
		return nil, ErrTooLarge
	}

	zone, err := s.zones.ZoneOf(ctx, scope.Owner())
	if err != nil {
		return nil, platformerrors.Wrap(err, "reading the studio's time zone")
	}

	return acuity.Plan(acuity.Input{
		Clients:      files.GetClientsCsv(),
		Appointments: files.GetAppointmentsCsv(),
		Today:        schedule.Today(s.now(), zone),
		Language:     languageFromProto(files.GetLanguage()),
	})
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

// fail turns a refusal into a status, and, for a refusal the owner can act
// on, into the words the page shows. As in the roster's server, the status's
// message is the first client-safe sentinel in the chain when there is one,
// and an unreadable export wraps the platform's generic "not a recognized
// value", so the chain is cut and the words go in its place.
func (s *Server) fail(err error, op observability.Operation, description string) error {
	code := codes.Internal

	var say string

	switch {
	case errors.Is(err, acuity.ErrUnreadable):
		code = codes.InvalidArgument
		say = readable(err)
	case errors.Is(err, ErrTooLarge):
		code = codes.InvalidArgument
		say = "Each file can be at most 3 MB. Export a shorter range of appointments."
	case errors.Is(err, roster.ErrPhoneTaken), errors.Is(err, roster.ErrEmailTaken):
		// Two households in one import cannot share a reach (the plan
		// groups them), so this is a contact added to the roster between
		// the preview and the apply.
		code = codes.Aborted
		say = "Somebody in the export was added to your clients while you were looking. Preview again."
	case errors.Is(err, ErrNilRequest):
		code = codes.InvalidArgument
	}

	if say != "" {
		err = platformerrors.New(description + ": " + err.Error())
		description = say
	}

	return grpcerrors.PrepareAndLogGRPCStatus(err, op.Logger(), op.Span(), code, "%s", description)
}

// readable is the owner-facing sentence an unreadable export's error carries:
// what wraps the sentinel, without the sentinel's own words.
func readable(err error) string {
	return strings.TrimSuffix(err.Error(), ": "+acuity.ErrUnreadable.Error()) + "."
}

func keySet(keys []string) map[string]bool {
	set := make(map[string]bool, len(keys))
	for _, key := range keys {
		set[key] = true
	}

	return set
}
