// Package remindersgrpc serves fijo.v1.ReminderService: a studio's reminder
// switch, where a reply goes, a test reminder to the owner, and the record of
// what was sent.
//
// Like the roster's server, every method is public to the enforcer and runs
// under the caller's own studio. Nothing here sends a client anything: the
// worker does that, once reminders are on.
package remindersgrpc

import (
	"context"
	"errors"
	"time"

	"github.com/primandproper/fijo/internal/auth/principal"
	"github.com/primandproper/fijo/internal/domain/messaging"
	"github.com/primandproper/fijo/internal/domain/roster"
	"github.com/primandproper/fijo/internal/grpc/fijopb"

	authzgrpc "github.com/primandproper/primitives-go/v2/authorization/grpc"
	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	grpcerrors "github.com/primandproper/primitives-go/v2/errors/grpc"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/tenancy"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const serverName = "reminders_grpc"

// The refusals.
var (
	ErrNilStore          = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil messaging store")
	ErrNilReminders      = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil reminders")
	ErrNilDatabaseClient = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil database client")
	ErrNoPrincipal       = platformerrors.New("no principal on the request")
	ErrNoStudio          = platformerrors.New("the caller belongs to no studio")
)

// Server implements fijopb.ReminderServiceServer.
type Server struct {
	fijopb.UnimplementedReminderServiceServer

	store     messaging.Store
	reminders *messaging.Reminders
	client    database.Client
	o11y      observability.Observer
}

var _ fijopb.ReminderServiceServer = (*Server)(nil)

// NewServer builds the server.
func NewServer(store messaging.Store, reminders *messaging.Reminders, client database.Client, pillars *observability.Pillars) (*Server, error) {
	switch {
	case store == nil:
		return nil, ErrNilStore
	case reminders == nil:
		return nil, ErrNilReminders
	case client == nil:
		return nil, ErrNilDatabaseClient
	}

	s := &Server{
		store:     store,
		reminders: reminders,
		client:    client,
		o11y:      observability.NewObserver(serverName, nil, nil),
	}

	if pillars != nil {
		s.o11y = observability.NewObserver(serverName, pillars.Logger, pillars.TracerProvider)
	}

	return s, nil
}

// RegisterOn mounts the service.
func (s *Server) RegisterOn(srv *grpc.Server) {
	fijopb.RegisterReminderServiceServer(srv, s)
}

// Require declares every method public to the enforcer: the scope comes off
// the principal, never the wire.
func Require(b *authzgrpc.RequirementsBuilder) *authzgrpc.RequirementsBuilder {
	if b == nil {
		return nil
	}

	for _, method := range []string{
		fijopb.ReminderService_GetReminderSettings_FullMethodName,
		fijopb.ReminderService_SaveReminderSettings_FullMethodName,
		fijopb.ReminderService_SendTestReminder_FullMethodName,
		fijopb.ReminderService_ListReminders_FullMethodName,
	} {
		b.Public(method)
	}

	return b
}

// GetReminderSettings implements fijopb.ReminderServiceServer.
func (s *Server) GetReminderSettings(ctx context.Context, _ *fijopb.GetReminderSettingsRequest) (*fijopb.ReminderSettings, error) {
	ctx, op := s.o11y.Begin(ctx)
	defer op.End()

	caller, scope, err := s.caller(ctx, op, fijopb.ReminderService_GetReminderSettings_FullMethodName)
	if err != nil {
		return nil, err
	}

	settings, err := s.store.GetSettings(ctx, s.client.Reader(), scope)
	if err != nil {
		return nil, s.fail(err, op, "reading reminder settings")
	}

	return settingsToProto(settings, caller), nil
}

// SaveReminderSettings implements fijopb.ReminderServiceServer.
func (s *Server) SaveReminderSettings(ctx context.Context, request *fijopb.SaveReminderSettingsRequest) (*fijopb.ReminderSettings, error) {
	ctx, op := s.o11y.Begin(ctx)
	defer op.End()

	caller, scope, err := s.caller(ctx, op, fijopb.ReminderService_SaveReminderSettings_FullMethodName)
	if err != nil {
		return nil, err
	}

	var saved *messaging.Settings

	if err = s.client.WithTransaction(ctx, func(tx database.Tx) error {
		var saveErr error

		saved, saveErr = s.store.SaveSettings(ctx, tx, scope, &messaging.Settings{
			RemindersOn: request.GetRemindersOn(),
			ReplyTo:     request.GetReplyTo(),
		})

		return saveErr
	}); err != nil {
		return nil, s.fail(err, op, "saving reminder settings")
	}

	return settingsToProto(saved, caller), nil
}

// SendTestReminder implements fijopb.ReminderServiceServer.
func (s *Server) SendTestReminder(ctx context.Context, request *fijopb.SendTestReminderRequest) (*fijopb.SendTestReminderResponse, error) {
	ctx, op := s.o11y.Begin(ctx)
	defer op.End()

	_, scope, err := s.caller(ctx, op, fijopb.ReminderService_SendTestReminder_FullMethodName)
	if err != nil {
		return nil, err
	}

	language := roster.English
	if request.GetLanguage() == fijopb.Language_LANGUAGE_ES {
		language = roster.Spanish
	}

	sentTo, err := s.reminders.SendTest(ctx, scope, language)
	if err != nil {
		return nil, s.fail(err, op, "sending a test reminder")
	}

	return &fijopb.SendTestReminderResponse{SentTo: sentTo}, nil
}

// ListReminders implements fijopb.ReminderServiceServer.
func (s *Server) ListReminders(ctx context.Context, request *fijopb.ListRemindersRequest) (*fijopb.ListRemindersResponse, error) {
	ctx, op := s.o11y.Begin(ctx)
	defer op.End()

	_, scope, err := s.caller(ctx, op, fijopb.ReminderService_ListReminders_FullMethodName)
	if err != nil {
		return nil, err
	}

	reminders, err := s.store.ListReminders(ctx, s.client.Reader(), scope, request.GetHouseholdId(), int(request.GetLimit()))
	if err != nil {
		return nil, s.fail(err, op, "listing reminders")
	}

	out := &fijopb.ListRemindersResponse{Reminders: make([]*fijopb.Reminder, 0, len(reminders))}

	for _, r := range reminders {
		out.Reminders = append(out.Reminders, reminderToProto(r))
	}

	return out, nil
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

// fail turns a refusal into a status, cutting the chain for the ones the owner
// can act on so the page shows Fijo's words rather than a sentinel's.
func (s *Server) fail(err error, op observability.Operation, description string) error {
	code := codes.Internal

	var say string

	switch {
	case errors.Is(err, messaging.ErrNoReplyTo):
		code = codes.InvalidArgument
		say = "Reminders need an email address for your clients to write back to."
	case errors.Is(err, messaging.ErrInvalidSettings):
		code = codes.InvalidArgument
		say = "That is not an email address we can send to."
	}

	if say != "" {
		err = platformerrors.New(description + ": " + err.Error())
		description = say
	}

	return grpcerrors.PrepareAndLogGRPCStatus(err, op.Logger(), op.Span(), code, "%s", description)
}

func settingsToProto(settings *messaging.Settings, caller *principal.Caller) *fijopb.ReminderSettings {
	out := &fijopb.ReminderSettings{
		RemindersOn: settings.RemindersOn,
		ReplyTo:     settings.ReplyTo,
	}

	if settings.RemindersOnSince != nil {
		out.RemindersOnSince = timestamppb.New(*settings.RemindersOnSince)
	}

	if caller != nil && caller.Principal != nil && caller.Principal.User != nil {
		out.SuggestedReplyTo = caller.Principal.User.EmailAddress
	}

	return out
}

func reminderToProto(r *messaging.Reminder) *fijopb.Reminder {
	out := &fijopb.Reminder{
		Id:             r.ID,
		HouseholdId:    r.HouseholdID,
		HouseholdName:  r.HouseholdName,
		ContactId:      r.ContactID,
		ContactName:    r.ContactName,
		ToAddress:      r.ToAddress,
		Language:       fijopb.Language_LANGUAGE_EN,
		Subject:        r.Subject,
		BodyHtml:       r.BodyHTML,
		Status:         statusToProto(r.Status),
		Reason:         r.Reason,
		OffsetMinutes:  int32(r.Offset / time.Minute),
		LessonStartsAt: timestamppb.New(r.LessonStartsAt),
		CreatedAt:      timestamppb.New(r.CreatedAt),
	}

	if r.Language == roster.Spanish {
		out.Language = fijopb.Language_LANGUAGE_ES
	}

	if r.SentAt != nil {
		out.SentAt = timestamppb.New(*r.SentAt)
	}

	return out
}

func statusToProto(status messaging.Status) fijopb.ReminderStatus {
	switch status {
	case messaging.Queued:
		return fijopb.ReminderStatus_REMINDER_STATUS_QUEUED
	case messaging.Sent:
		return fijopb.ReminderStatus_REMINDER_STATUS_SENT
	case messaging.Skipped:
		return fijopb.ReminderStatus_REMINDER_STATUS_SKIPPED
	case messaging.Failed:
		return fijopb.ReminderStatus_REMINDER_STATUS_FAILED
	default:
		return fijopb.ReminderStatus_REMINDER_STATUS_UNSPECIFIED
	}
}
