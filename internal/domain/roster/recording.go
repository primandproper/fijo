package roster

import (
	"context"

	"github.com/primandproper/platform-go/v15/audit"
	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/identifiers"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

// The resource types the roster's audit entries name.
const (
	ResourceHousehold = "roster.household"
	ResourceContact   = "roster.contact"
	ResourceAttendee  = "roster.attendee"
)

// KeyHousehold is the metadata key naming the household a contact's or an
// attendee's entry is on, so a household's history can be read as the entries
// about it and about its people.
const KeyHousehold = "household_id"

// ErrNilRecorder is a RecordingHooks built over no audit recorder.
var ErrNilRecorder = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil audit recorder")

// personal is what an update's diff hashes before it is recorded: what the
// roster knows about a person rather than about the studio's arrangements
// with them. The audit log is built not to forget, and a client's name, email,
// phone and the owner's notes about them outlive a client who has asked to be
// forgotten if they are written there as they are. The digest still says the
// field changed. This is the hook's obligation rather than a deployment's
// policy, as audit.Redaction.Apply says.
var personal = audit.Redaction{Hash: []string{"name", "email", "phone", "notes"}}

// ActorOf reads who is making a write off its context: a user's identifier,
// or empty for a write nobody is signed in to make (a seed, a test).
type ActorOf func(ctx context.Context) string

// RecordingHooks writes an audit entry for every roster write, in the
// studio's scope and on the write's transaction.
//
// It writes to audit.Recorder directly rather than through platform-go's
// recording.Recorder, which also emits a domain event for every write and so
// needs the webhook tables Fijo does not have.
type RecordingHooks struct {
	recorder audit.Recorder
	actorOf  ActorOf
}

var _ Hooks = (*RecordingHooks)(nil)

// NewRecordingHooks builds the hooks. actorOf may be nil, in which case every
// entry is unattributed.
func NewRecordingHooks(recorder audit.Recorder, actorOf ActorOf) (*RecordingHooks, error) {
	if recorder == nil {
		return nil, ErrNilRecorder
	}

	if actorOf == nil {
		actorOf = func(context.Context) string { return "" }
	}

	return &RecordingHooks{recorder: recorder, actorOf: actorOf}, nil
}

// AfterCreateHousehold implements Hooks.
func (h *RecordingHooks) AfterCreateHousehold(ctx context.Context, tx database.Tx, scope tenancy.Scope, household *Household) error {
	if household == nil {
		return ErrNilInput
	}

	return h.record(ctx, tx, scope, ResourceHousehold, household.ID, audit.EventCreated, nil, nil)
}

// AfterUpdateHousehold implements Hooks.
func (h *RecordingHooks) AfterUpdateHousehold(ctx context.Context, tx database.Tx, scope tenancy.Scope, before, after *Household) error {
	if before == nil || after == nil {
		return ErrNilInput
	}

	// A household's people have their own entries.
	b, a := *before, *after
	b.Contacts, b.Attendees, a.Contacts, a.Attendees = nil, nil, nil, nil

	return h.recordUpdate(ctx, tx, scope, ResourceHousehold, after.ID, &b, &a, nil)
}

// AfterArchiveHousehold implements Hooks.
func (h *RecordingHooks) AfterArchiveHousehold(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string) error {
	return h.record(ctx, tx, scope, ResourceHousehold, id, audit.EventArchived, nil, nil)
}

// AfterCreateContact implements Hooks.
func (h *RecordingHooks) AfterCreateContact(ctx context.Context, tx database.Tx, scope tenancy.Scope, contact *Contact) error {
	if contact == nil {
		return ErrNilInput
	}

	return h.record(ctx, tx, scope, ResourceContact, contact.ID, audit.EventCreated, nil, onHousehold(contact.HouseholdID))
}

// AfterUpdateContact implements Hooks.
func (h *RecordingHooks) AfterUpdateContact(ctx context.Context, tx database.Tx, scope tenancy.Scope, before, after *Contact) error {
	if before == nil || after == nil {
		return ErrNilInput
	}

	return h.recordUpdate(ctx, tx, scope, ResourceContact, after.ID, before, after, onHousehold(after.HouseholdID))
}

// AfterArchiveContact implements Hooks.
func (h *RecordingHooks) AfterArchiveContact(ctx context.Context, tx database.Tx, scope tenancy.Scope, contact *Contact) error {
	if contact == nil {
		return ErrNilInput
	}

	return h.record(ctx, tx, scope, ResourceContact, contact.ID, audit.EventArchived, nil, onHousehold(contact.HouseholdID))
}

// AfterCreateAttendee implements Hooks.
func (h *RecordingHooks) AfterCreateAttendee(ctx context.Context, tx database.Tx, scope tenancy.Scope, attendee *Attendee) error {
	if attendee == nil {
		return ErrNilInput
	}

	return h.record(ctx, tx, scope, ResourceAttendee, attendee.ID, audit.EventCreated, nil, onHousehold(attendee.HouseholdID))
}

// AfterUpdateAttendee implements Hooks.
func (h *RecordingHooks) AfterUpdateAttendee(ctx context.Context, tx database.Tx, scope tenancy.Scope, before, after *Attendee) error {
	if before == nil || after == nil {
		return ErrNilInput
	}

	return h.recordUpdate(ctx, tx, scope, ResourceAttendee, after.ID, before, after, onHousehold(after.HouseholdID))
}

// AfterArchiveAttendee implements Hooks.
func (h *RecordingHooks) AfterArchiveAttendee(ctx context.Context, tx database.Tx, scope tenancy.Scope, attendee *Attendee) error {
	if attendee == nil {
		return ErrNilInput
	}

	return h.record(ctx, tx, scope, ResourceAttendee, attendee.ID, audit.EventArchived, nil, onHousehold(attendee.HouseholdID))
}

// recordUpdate records the fields an update moved, the personal ones hashed.
// An update that moved nothing but the timestamp every save stamps records
// nothing: an owner pressing save on an unchanged form is not an event.
func (h *RecordingHooks) recordUpdate(
	ctx context.Context,
	tx database.Tx,
	scope tenancy.Scope,
	resourceType, id string,
	before, after any,
	metadata map[string]string,
) error {
	changes, err := audit.Diff(before, after)
	if err != nil {
		return platformerrors.Wrap(err, "diffing the update")
	}

	delete(changes, "lastUpdatedAt")

	if len(changes) == 0 {
		return nil
	}

	if changes, err = personal.Apply(changes); err != nil {
		return platformerrors.Wrap(err, "redacting the update")
	}

	return h.record(ctx, tx, scope, resourceType, id, audit.EventUpdated, changes, metadata)
}

func (h *RecordingHooks) record(
	ctx context.Context,
	tx database.Tx,
	scope tenancy.Scope,
	resourceType, id string,
	event audit.EventType,
	changes map[string]audit.Change,
	metadata map[string]string,
) error {
	actor := audit.Actor{ID: h.actorOf(ctx), Type: audit.ActorUser}
	if actor.ID == "" {
		actor = audit.Actor{ID: audit.ActorUnattributed, Type: audit.ActorSystem}
	}

	return h.recorder.Record(ctx, tx, scope, &audit.Entry{
		ID:           identifiers.New(),
		ResourceType: resourceType,
		ResourceID:   id,
		EventType:    event,
		Actor:        actor,
		Changes:      changes,
		Metadata:     metadata,
	})
}

func onHousehold(id string) map[string]string {
	if id == "" {
		return nil
	}

	return map[string]string{KeyHousehold: id}
}
