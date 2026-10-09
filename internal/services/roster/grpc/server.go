// Package rostergrpc serves fijo.v1.RosterService: the studio's clients.
//
// Like the schedule's server, every method is public to the enforcer, refuses a
// request with no principal inside the method, and runs under the caller's own
// studio, so there is no studio on the wire to get wrong.
//
// The one rule here that is not the store's is that nobody with a standing
// slot is archived. The roster cannot see the schedule's tables from its own
// statements, so the server asks the schedule through Lessons before it lets
// the store archive anyone.
package rostergrpc

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/primandproper/fijo/internal/auth/principal"
	"github.com/primandproper/fijo/internal/domain/roster"
	"github.com/primandproper/fijo/internal/domain/schedule"
	"github.com/primandproper/fijo/internal/grpc/fijopb"

	authzgrpc "github.com/primandproper/primitives-go/v2/authorization/grpc"
	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	grpcerrors "github.com/primandproper/primitives-go/v2/errors/grpc"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/tenancy"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

const serverName = "roster_grpc"

// The refusals.
var (
	ErrNilStore          = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil roster store")
	ErrNilLessons        = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil lessons lookup")
	ErrNilZones          = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil zone lookup")
	ErrNilDatabaseClient = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil database client")
	ErrNoPrincipal       = platformerrors.New("no principal on the request")
	ErrNoStudio          = platformerrors.New("the caller belongs to no studio")
	ErrNilRequest        = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil request body")

	// ErrHasStandingLessons is an archive of somebody a standing slot still
	// names. Archiving them would leave the slot producing lessons for a
	// person who is not on the roster.
	ErrHasStandingLessons = platformerrors.New("still has standing lessons")
)

// Lessons is the slice of schedule.Store this server asks before archiving.
type Lessons interface {
	LiveSeriesForAttendees(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, attendeeIDs []string, today schedule.Date) (map[string][]string, error)
}

// Server implements fijopb.RosterServiceServer.
type Server struct {
	fijopb.UnimplementedRosterServiceServer

	store   roster.Store
	lessons Lessons
	zones   schedule.Zones
	client  database.Client
	o11y    observability.Observer
	now     func() time.Time
}

var _ fijopb.RosterServiceServer = (*Server)(nil)

// NewServer builds the server.
func NewServer(store roster.Store, lessons Lessons, zones schedule.Zones, client database.Client, pillars *observability.Pillars) (*Server, error) {
	switch {
	case store == nil:
		return nil, ErrNilStore
	case lessons == nil:
		return nil, ErrNilLessons
	case zones == nil:
		return nil, ErrNilZones
	case client == nil:
		return nil, ErrNilDatabaseClient
	}

	s := &Server{
		store:   store,
		lessons: lessons,
		zones:   zones,
		client:  client,
		o11y:    observability.NewObserver(serverName, nil, nil),
		now:     time.Now,
	}

	if pillars != nil {
		s.o11y = observability.NewObserver(serverName, pillars.Logger, pillars.TracerProvider)
	}

	return s, nil
}

// RegisterOn mounts the service.
func (s *Server) RegisterOn(srv *grpc.Server) {
	fijopb.RegisterRosterServiceServer(srv, s)
}

// Require declares every method public to the enforcer, for the reason the
// schedule's Require gives: the scope comes off the principal, never the wire.
func Require(b *authzgrpc.RequirementsBuilder) *authzgrpc.RequirementsBuilder {
	if b == nil {
		return nil
	}

	for _, method := range []string{
		fijopb.RosterService_ListHouseholds_FullMethodName,
		fijopb.RosterService_GetHousehold_FullMethodName,
		fijopb.RosterService_CreateHousehold_FullMethodName,
		fijopb.RosterService_UpdateHousehold_FullMethodName,
		fijopb.RosterService_ArchiveHousehold_FullMethodName,
		fijopb.RosterService_CreateContact_FullMethodName,
		fijopb.RosterService_UpdateContact_FullMethodName,
		fijopb.RosterService_ArchiveContact_FullMethodName,
		fijopb.RosterService_CreateAttendee_FullMethodName,
		fijopb.RosterService_UpdateAttendee_FullMethodName,
		fijopb.RosterService_ArchiveAttendee_FullMethodName,
	} {
		b.Public(method)
	}

	return b
}

// caller resolves who is asking and which studio they are in.
func (s *Server) caller(ctx context.Context, op observability.Operation, method string) (tenancy.Scope, error) {
	c, ok := principal.FromContext(ctx)
	if !ok {
		return tenancy.Scope{}, grpcerrors.PrepareAndLogGRPCStatus(ErrNoPrincipal, op.Logger(), op.Span(), codes.Unauthenticated, "resolving the caller of %s", method)
	}

	scope := c.StudioScope()
	if err := scope.Validate(); err != nil || scope.IsGlobal() {
		return tenancy.Scope{}, grpcerrors.PrepareAndLogGRPCStatus(ErrNoStudio, op.Logger(), op.Span(), codes.FailedPrecondition, "resolving the studio of %s", method)
	}

	return scope, nil
}

// fail turns a domain refusal into a status, and, for the refusals a person
// can act on, into words they can act on: the description is what the owner
// app shows.
func (s *Server) fail(err error, op observability.Operation, description string) error {
	code := codes.Internal

	// say is set for a refusal Fijo words itself. The status's message is the
	// first client-safe sentinel in the error's chain when there is one, and a
	// bad phone wraps the platform's generic "not a recognized value", so the
	// chain is cut here and the words go in its place.
	var say string

	switch {
	case errors.Is(err, roster.ErrNotFound):
		code = codes.NotFound
	case errors.Is(err, roster.ErrPhoneTaken):
		code = codes.AlreadyExists
		say = "Another client already has that phone number."
	case errors.Is(err, ErrHasStandingLessons):
		code = codes.FailedPrecondition
		say = "They still have standing lessons. End those first."
	case errors.Is(err, roster.ErrInvalidPhone):
		code = codes.InvalidArgument
		say = "That is not a phone number we can text. A number outside the US and Canada needs its country code, like +52."
	case errors.Is(err, roster.ErrInvalidHousehold),
		errors.Is(err, roster.ErrInvalidContact),
		errors.Is(err, roster.ErrInvalidAttendee),
		errors.Is(err, roster.ErrNilInput),
		errors.Is(err, roster.ErrEmptyID),
		errors.Is(err, ErrNilRequest),
		errors.Is(err, platformerrors.ErrUnrecognizedInputValue):
		code = codes.InvalidArgument
	}

	if say != "" {
		err = platformerrors.New(description + ": " + err.Error())
		description = say
	}

	return grpcerrors.PrepareAndLogGRPCStatus(err, op.Logger(), op.Span(), code, "%s", description)
}

func (s *Server) begin(ctx context.Context) (context.Context, observability.Operation, func()) {
	ctx, op := s.o11y.Begin(ctx)

	return ctx, op, op.End
}

// guardArchive refuses when any of the attendees still has a standing slot.
func (s *Server) guardArchive(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, attendeeIDs []string) error {
	if len(attendeeIDs) == 0 {
		return nil
	}

	zone, err := s.zones.ZoneOf(ctx, scope.Owner())
	if err != nil {
		return err
	}

	live, err := s.lessons.LiveSeriesForAttendees(ctx, q, scope, attendeeIDs, schedule.Today(s.now(), zone))
	if err != nil {
		return err
	}

	if len(live) > 0 {
		return ErrHasStandingLessons
	}

	return nil
}

// ListHouseholds implements fijopb.RosterServiceServer.
func (s *Server) ListHouseholds(ctx context.Context, request *fijopb.ListHouseholdsRequest) (*fijopb.ListHouseholdsResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	scope, err := s.caller(ctx, op, fijopb.RosterService_ListHouseholds_FullMethodName)
	if err != nil {
		return nil, err
	}

	households, err := s.store.ListHouseholds(ctx, s.client.Reader(), scope, request.GetIncludeArchived())
	if err != nil {
		return nil, s.fail(err, op, "listing clients")
	}

	out := make([]*fijopb.Household, 0, len(households))
	for _, household := range households {
		out = append(out, householdToProto(household))
	}

	return &fijopb.ListHouseholdsResponse{Households: out}, nil
}

// GetHousehold implements fijopb.RosterServiceServer.
func (s *Server) GetHousehold(ctx context.Context, request *fijopb.GetHouseholdRequest) (*fijopb.HouseholdResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	scope, err := s.caller(ctx, op, fijopb.RosterService_GetHousehold_FullMethodName)
	if err != nil {
		return nil, err
	}

	household, err := s.store.GetHousehold(ctx, s.client.Reader(), scope, request.GetId())
	if err != nil {
		return nil, s.fail(err, op, "reading a client")
	}

	return &fijopb.HouseholdResponse{Household: householdToProto(household)}, nil
}

// CreateHousehold implements fijopb.RosterServiceServer.
func (s *Server) CreateHousehold(ctx context.Context, request *fijopb.CreateHouseholdRequest) (*fijopb.HouseholdResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	scope, err := s.caller(ctx, op, fijopb.RosterService_CreateHousehold_FullMethodName)
	if err != nil {
		return nil, err
	}

	if request.GetHousehold() == nil {
		return nil, s.fail(ErrNilRequest, op, "adding a client")
	}

	var written *roster.Household

	if err = s.client.WithTransaction(ctx, func(tx database.Tx) error {
		household, createErr := s.store.CreateHousehold(ctx, tx, scope, householdFromProto(request.GetHousehold()))
		if createErr != nil {
			return createErr
		}

		for _, added := range request.GetContacts() {
			if added.GetContact() == nil {
				return ErrNilRequest
			}

			if createErr = s.createContact(ctx, tx, scope, household.ID, added.GetContact(), added.GetAttends()); createErr != nil {
				return createErr
			}
		}

		for _, attendee := range request.GetAttendees() {
			staged := attendeeFromProto(attendee)
			staged.HouseholdID = household.ID

			if _, createErr = s.store.CreateAttendee(ctx, tx, scope, withLanguage(staged, household.Language)); createErr != nil {
				return createErr
			}
		}

		written, createErr = s.store.GetHousehold(ctx, tx, scope, household.ID)

		return createErr
	}); err != nil {
		return nil, s.fail(err, op, "adding a client")
	}

	return &fijopb.HouseholdResponse{Household: householdToProto(written)}, nil
}

// UpdateHousehold implements fijopb.RosterServiceServer.
func (s *Server) UpdateHousehold(ctx context.Context, request *fijopb.UpdateHouseholdRequest) (*fijopb.HouseholdResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	scope, err := s.caller(ctx, op, fijopb.RosterService_UpdateHousehold_FullMethodName)
	if err != nil {
		return nil, err
	}

	if request.GetHousehold() == nil {
		return nil, s.fail(ErrNilRequest, op, "saving a client")
	}

	var written *roster.Household

	if err = s.client.WithTransaction(ctx, func(tx database.Tx) error {
		var writeErr error

		written, writeErr = s.store.UpdateHousehold(ctx, tx, scope, householdFromProto(request.GetHousehold()))

		return writeErr
	}); err != nil {
		return nil, s.fail(err, op, "saving a client")
	}

	return &fijopb.HouseholdResponse{Household: householdToProto(written)}, nil
}

// ArchiveHousehold implements fijopb.RosterServiceServer.
func (s *Server) ArchiveHousehold(ctx context.Context, request *fijopb.ArchiveHouseholdRequest) (*fijopb.ArchiveHouseholdResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	scope, err := s.caller(ctx, op, fijopb.RosterService_ArchiveHousehold_FullMethodName)
	if err != nil {
		return nil, err
	}

	if err = s.client.WithTransaction(ctx, func(tx database.Tx) error {
		attendees, listErr := s.store.AttendeesOfHousehold(ctx, tx, scope, request.GetId())
		if listErr != nil {
			return listErr
		}

		if guardErr := s.guardArchive(ctx, tx, scope, attendees); guardErr != nil {
			return guardErr
		}

		return s.store.ArchiveHousehold(ctx, tx, scope, request.GetId())
	}); err != nil {
		return nil, s.fail(err, op, "archiving a client")
	}

	return &fijopb.ArchiveHouseholdResponse{}, nil
}

// CreateContact implements fijopb.RosterServiceServer.
func (s *Server) CreateContact(ctx context.Context, request *fijopb.CreateContactRequest) (*fijopb.HouseholdResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	scope, err := s.caller(ctx, op, fijopb.RosterService_CreateContact_FullMethodName)
	if err != nil {
		return nil, err
	}

	if request.GetContact() == nil {
		return nil, s.fail(ErrNilRequest, op, "adding a contact")
	}

	written, err := s.writeThenRead(ctx, scope, func(tx database.Tx) (string, error) {
		householdID := request.GetContact().GetHouseholdId()

		return householdID, s.createContact(ctx, tx, scope, householdID, request.GetContact(), request.GetAttends())
	})
	if err != nil {
		return nil, s.fail(err, op, "adding a contact")
	}

	return &fijopb.HouseholdResponse{Household: householdToProto(written)}, nil
}

// UpdateContact implements fijopb.RosterServiceServer.
func (s *Server) UpdateContact(ctx context.Context, request *fijopb.UpdateContactRequest) (*fijopb.HouseholdResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	scope, err := s.caller(ctx, op, fijopb.RosterService_UpdateContact_FullMethodName)
	if err != nil {
		return nil, err
	}

	if request.GetContact() == nil {
		return nil, s.fail(ErrNilRequest, op, "saving a contact")
	}

	written, err := s.writeThenRead(ctx, scope, func(tx database.Tx) (string, error) {
		contact, writeErr := s.store.UpdateContact(ctx, tx, scope, contactFromProto(request.GetContact()))
		if writeErr != nil {
			return "", writeErr
		}

		return contact.HouseholdID, nil
	})
	if err != nil {
		return nil, s.fail(err, op, "saving a contact")
	}

	return &fijopb.HouseholdResponse{Household: householdToProto(written)}, nil
}

// ArchiveContact implements fijopb.RosterServiceServer.
func (s *Server) ArchiveContact(ctx context.Context, request *fijopb.ArchiveContactRequest) (*fijopb.HouseholdResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	scope, err := s.caller(ctx, op, fijopb.RosterService_ArchiveContact_FullMethodName)
	if err != nil {
		return nil, err
	}

	written, err := s.writeThenRead(ctx, scope, func(tx database.Tx) (string, error) {
		contact, readErr := s.store.GetContact(ctx, tx, scope, request.GetId())
		if readErr != nil {
			return "", readErr
		}

		return contact.HouseholdID, s.store.ArchiveContact(ctx, tx, scope, contact.ID)
	})
	if err != nil {
		return nil, s.fail(err, op, "removing a contact")
	}

	return &fijopb.HouseholdResponse{Household: householdToProto(written)}, nil
}

// CreateAttendee implements fijopb.RosterServiceServer.
func (s *Server) CreateAttendee(ctx context.Context, request *fijopb.CreateAttendeeRequest) (*fijopb.HouseholdResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	scope, err := s.caller(ctx, op, fijopb.RosterService_CreateAttendee_FullMethodName)
	if err != nil {
		return nil, err
	}

	if request.GetAttendee() == nil {
		return nil, s.fail(ErrNilRequest, op, "adding a student")
	}

	written, err := s.writeThenRead(ctx, scope, func(tx database.Tx) (string, error) {
		household, readErr := s.store.GetHousehold(ctx, tx, scope, request.GetAttendee().GetHouseholdId())
		if readErr != nil {
			return "", readErr
		}

		_, writeErr := s.store.CreateAttendee(ctx, tx, scope, withLanguage(attendeeFromProto(request.GetAttendee()), household.Language))

		return household.ID, writeErr
	})
	if err != nil {
		return nil, s.fail(err, op, "adding a student")
	}

	return &fijopb.HouseholdResponse{Household: householdToProto(written)}, nil
}

// UpdateAttendee implements fijopb.RosterServiceServer.
func (s *Server) UpdateAttendee(ctx context.Context, request *fijopb.UpdateAttendeeRequest) (*fijopb.HouseholdResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	scope, err := s.caller(ctx, op, fijopb.RosterService_UpdateAttendee_FullMethodName)
	if err != nil {
		return nil, err
	}

	if request.GetAttendee() == nil {
		return nil, s.fail(ErrNilRequest, op, "saving a student")
	}

	written, err := s.writeThenRead(ctx, scope, func(tx database.Tx) (string, error) {
		attendee, writeErr := s.store.UpdateAttendee(ctx, tx, scope, attendeeFromProto(request.GetAttendee()))
		if writeErr != nil {
			return "", writeErr
		}

		return attendee.HouseholdID, nil
	})
	if err != nil {
		return nil, s.fail(err, op, "saving a student")
	}

	return &fijopb.HouseholdResponse{Household: householdToProto(written)}, nil
}

// ArchiveAttendee implements fijopb.RosterServiceServer.
func (s *Server) ArchiveAttendee(ctx context.Context, request *fijopb.ArchiveAttendeeRequest) (*fijopb.HouseholdResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	scope, err := s.caller(ctx, op, fijopb.RosterService_ArchiveAttendee_FullMethodName)
	if err != nil {
		return nil, err
	}

	written, err := s.writeThenRead(ctx, scope, func(tx database.Tx) (string, error) {
		attendee, readErr := s.store.GetAttendee(ctx, tx, scope, request.GetId())
		if readErr != nil {
			return "", readErr
		}

		if guardErr := s.guardArchive(ctx, tx, scope, []string{attendee.ID}); guardErr != nil {
			return "", guardErr
		}

		return attendee.HouseholdID, s.store.ArchiveAttendee(ctx, tx, scope, attendee.ID)
	})
	if err != nil {
		return nil, s.fail(err, op, "removing a student")
	}

	return &fijopb.HouseholdResponse{Household: householdToProto(written)}, nil
}

// createContact writes a contact and, for an adult who takes the lessons, the
// attendee that is the same person.
func (s *Server) createContact(ctx context.Context, tx database.Tx, scope tenancy.Scope, householdID string, in *fijopb.Contact, attends bool) error {
	household, err := s.store.GetHousehold(ctx, tx, scope, householdID)
	if err != nil {
		return err
	}

	staged := contactFromProto(in)
	staged.HouseholdID = householdID

	if !staged.Language.Valid() {
		staged.Language = household.Language
	}

	if attends {
		staged.Relationship = roster.Self
	} else if !staged.Relationship.Valid() {
		staged.Relationship = roster.Guardian
	}

	contact, err := s.store.CreateContact(ctx, tx, scope, staged)
	if err != nil {
		return err
	}

	if !attends {
		return nil
	}

	_, err = s.store.CreateAttendee(ctx, tx, scope, &roster.Attendee{
		HouseholdID: householdID,
		Name:        contact.Name,
		Language:    contact.Language,
		ContactID:   contact.ID,
	})

	return err
}

// writeThenRead runs a write and reads back, on the same transaction, the
// household the write says it touched, so every write answers with the
// household as it now stands.
func (s *Server) writeThenRead(ctx context.Context, scope tenancy.Scope, write func(tx database.Tx) (string, error)) (*roster.Household, error) {
	var household *roster.Household

	err := s.client.WithTransaction(ctx, func(tx database.Tx) error {
		householdID, err := write(tx)
		if err != nil {
			return err
		}

		household, err = s.store.GetHousehold(ctx, tx, scope, householdID)

		return err
	})

	return household, err
}

// withLanguage gives an attendee who names no language the household's.
func withLanguage(attendee *roster.Attendee, fallback roster.Language) *roster.Attendee {
	if !attendee.Language.Valid() {
		attendee.Language = fallback
	}

	return attendee
}

func householdToProto(household *roster.Household) *fijopb.Household {
	if household == nil {
		return nil
	}

	out := &fijopb.Household{
		Id:        household.ID,
		Name:      household.Name,
		Language:  languageToProto(household.Language),
		Notes:     household.Notes,
		Archived:  household.ArchivedAt != nil,
		Contacts:  make([]*fijopb.Contact, 0, len(household.Contacts)),
		Attendees: make([]*fijopb.Attendee, 0, len(household.Attendees)),
	}

	for _, contact := range household.Contacts {
		out.Contacts = append(out.Contacts, &fijopb.Contact{
			Id:           contact.ID,
			HouseholdId:  contact.HouseholdID,
			Name:         contact.Name,
			Phone:        contact.Phone,
			Language:     languageToProto(contact.Language),
			Relationship: relationshipToProto(contact.Relationship),
			OptedOut:     contact.OptedOut,
			Verified:     contact.VerifiedAt != nil,
		})
	}

	for _, attendee := range household.Attendees {
		out.Attendees = append(out.Attendees, &fijopb.Attendee{
			Id:          attendee.ID,
			HouseholdId: attendee.HouseholdID,
			Name:        attendee.Name,
			Language:    languageToProto(attendee.Language),
			ContactId:   attendee.ContactID,
		})
	}

	return out
}

func householdFromProto(in *fijopb.Household) *roster.Household {
	return &roster.Household{
		ID:       in.GetId(),
		Name:     strings.TrimSpace(in.GetName()),
		Language: languageFromProto(in.GetLanguage()),
		Notes:    strings.TrimSpace(in.GetNotes()),
	}
}

func contactFromProto(in *fijopb.Contact) *roster.Contact {
	return &roster.Contact{
		ID:           in.GetId(),
		HouseholdID:  in.GetHouseholdId(),
		Name:         strings.TrimSpace(in.GetName()),
		Phone:        in.GetPhone(),
		Language:     languageFromProto(in.GetLanguage()),
		Relationship: relationshipFromProto(in.GetRelationship()),
	}
}

func attendeeFromProto(in *fijopb.Attendee) *roster.Attendee {
	return &roster.Attendee{
		ID:          in.GetId(),
		HouseholdID: in.GetHouseholdId(),
		Name:        strings.TrimSpace(in.GetName()),
		Language:    languageFromProto(in.GetLanguage()),
		ContactID:   in.GetContactId(),
	}
}

func languageToProto(language roster.Language) fijopb.Language {
	switch language {
	case roster.English:
		return fijopb.Language_LANGUAGE_EN
	case roster.Spanish:
		return fijopb.Language_LANGUAGE_ES
	default:
		return fijopb.Language_LANGUAGE_UNSPECIFIED
	}
}

func languageFromProto(language fijopb.Language) roster.Language {
	switch language {
	case fijopb.Language_LANGUAGE_EN:
		return roster.English
	case fijopb.Language_LANGUAGE_ES:
		return roster.Spanish
	default:
		return ""
	}
}

func relationshipToProto(relationship roster.Relationship) fijopb.Relationship {
	switch relationship {
	case roster.Guardian:
		return fijopb.Relationship_RELATIONSHIP_GUARDIAN
	case roster.Self:
		return fijopb.Relationship_RELATIONSHIP_SELF
	default:
		return fijopb.Relationship_RELATIONSHIP_UNSPECIFIED
	}
}

func relationshipFromProto(relationship fijopb.Relationship) roster.Relationship {
	switch relationship {
	case fijopb.Relationship_RELATIONSHIP_GUARDIAN:
		return roster.Guardian
	case fijopb.Relationship_RELATIONSHIP_SELF:
		return roster.Self
	default:
		return ""
	}
}
