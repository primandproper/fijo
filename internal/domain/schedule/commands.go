package schedule

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/primandproper/platform-go/v15/audit"
	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/identifiers"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

// The resource types the grid's entries name.
const (
	ResourceOccurrence = "schedule.occurrence"
	ResourceSeries     = "schedule.series"
	ResourceClosure    = "schedule.closure"
)

// The metadata keys the grid's entries carry.
const (
	KeyCommand      = "command"
	KeyState        = "state"
	KeyCancelledBy  = "cancelled_by"
	KeyInsideNotice = "inside_notice"
	KeyReason       = "reason"
	KeySeriesID     = "series_id"
	KeyMovedTo      = "moved_to_occurrence_id"
	KeyReplaces     = "replaces_occurrence_id"
	KeyStartsAt     = "starts_at"
	KeyEndsOn       = "ends_on"
	KeyStaff        = "staff"
	KeyAffected     = "affected_occurrences"
)

// The command names, which are what an owner reads in the history.
const (
	CommandCancel      = "cancel_occurrence"
	CommandComplete    = "complete_occurrence"
	CommandNoShow      = "no_show_occurrence"
	CommandRevert      = "revert_occurrence"
	CommandMove        = "move_occurrence"
	CommandMakeUp      = "schedule_make_up"
	CommandReassign    = "reassign_occurrence"
	CommandEndSeries   = "end_series"
	CommandCloseStudio = "close_studio"
)

// ErrNilRecorder is a Commands built over no audit recorder. It is refused
// rather than defaulted, because a command nobody can read back afterwards is
// the thing this layer exists to prevent.
var ErrNilRecorder = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil audit recorder")

// Commands are the audited acts on the grid.
//
// Every one of them is a transaction that writes the rows and the audit entry
// together, because an entry that can be missing describes a history that can
// lie. The store is deliberately unaware of any of this: it knows how to
// change a row and refuses an illegal transition, and this layer knows who
// asked, why, and what else has to change with it.
//
// The caller is passed in rather than read from the context. These run under a
// gRPC handler that already holds the principal, and a layer that reaches into
// a context for identity is a layer that can be called from somewhere without
// one and not notice.
type Commands struct {
	client   database.Client
	store    Store
	recorder audit.Recorder
	o11y     observability.Observer
	now      func() time.Time
}

// CommandsOption configures a Commands.
type CommandsOption func(*Commands)

// WithCommandsPillars attaches the observability pillars.
func WithCommandsPillars(p *observability.Pillars) CommandsOption {
	return func(c *Commands) {
		if p != nil {
			c.o11y = observability.NewObserver("schedule_commands", p.Logger, p.TracerProvider)
		}
	}
}

// WithCommandsClock replaces the clock, which decides what counts as late.
func WithCommandsClock(now func() time.Time) CommandsOption {
	return func(c *Commands) {
		if now != nil {
			c.now = now
		}
	}
}

// NewCommands builds the command layer.
func NewCommands(client database.Client, store Store, recorder audit.Recorder, opts ...CommandsOption) (*Commands, error) {
	switch {
	case client == nil:
		return nil, platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil database client")
	case store == nil:
		return nil, platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil schedule store")
	case recorder == nil:
		return nil, ErrNilRecorder
	}

	c := &Commands{
		client:   client,
		store:    store,
		recorder: recorder,
		o11y:     observability.NewObserver("schedule_commands", nil, nil),
		now:      time.Now,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}

	return c, nil
}

// Cancel records that an appointment is not happening, and whose decision that
// was.
//
// Whether it was late is judged here, against the studio's notice window and
// the clock, and then kept on the row: the window can be changed afterwards and
// the judgement must not move with it.
func (c *Commands) Cancel(ctx context.Context, scope tenancy.Scope, actor, id string, by Canceller, reason string) (_ *Occurrence, err error) {
	ctx, op := c.o11y.Begin(ctx)
	defer op.End()

	var cancelled *Occurrence

	if err = c.client.WithTransaction(ctx, func(tx database.Tx) error {
		occurrence, readErr := c.store.GetOccurrence(ctx, tx, scope, id)
		if readErr != nil {
			return readErr
		}

		settings, settingsErr := c.store.GetSettings(ctx, tx, scope)
		if settingsErr != nil {
			return settingsErr
		}

		inside := c.now().After(occurrence.StartsAt.Add(-settings.NoticeWindow))

		written, cancelErr := c.store.CancelOccurrence(ctx, tx, scope, id, by, inside, reason)
		if cancelErr != nil {
			return cancelErr
		}

		cancelled = written

		return c.record(ctx, tx, scope, ResourceOccurrence, id, audit.EventUpdated, actor, map[string]string{
			KeyCommand:      CommandCancel,
			KeyState:        string(StateCancelled),
			KeyCancelledBy:  string(by),
			KeyInsideNotice: strconv.FormatBool(inside),
			KeyReason:       reason,
		})
	}); err != nil {
		return nil, op.Error(err, "cancelling an occurrence")
	}

	return cancelled, nil
}

// Complete records that it happened.
func (c *Commands) Complete(ctx context.Context, scope tenancy.Scope, actor, id, reason string) (*Occurrence, error) {
	return c.setState(ctx, scope, actor, id, StateCompleted, CommandComplete, reason)
}

// NoShow records that nobody came.
func (c *Commands) NoShow(ctx context.Context, scope tenancy.Scope, actor, id, reason string) (*Occurrence, error) {
	return c.setState(ctx, scope, actor, id, StateNoShow, CommandNoShow, reason)
}

// Revert puts an appointment back to standing, whatever it had become.
//
// Every transition the provider can make is revertible, which is the settled
// rule the balance ledger will lean on when it arrives: a reverted completion
// gives the lesson back.
func (c *Commands) Revert(ctx context.Context, scope tenancy.Scope, actor, id, reason string) (*Occurrence, error) {
	return c.setState(ctx, scope, actor, id, StateScheduled, CommandRevert, reason)
}

func (c *Commands) setState(ctx context.Context, scope tenancy.Scope, actor, id string, state OccurrenceState, command, reason string) (_ *Occurrence, err error) {
	ctx, op := c.o11y.Begin(ctx)
	defer op.End()

	var written *Occurrence

	if err = c.client.WithTransaction(ctx, func(tx database.Tx) error {
		one, stateErr := c.store.SetOccurrenceState(ctx, tx, scope, id, state, reason)
		if stateErr != nil {
			return stateErr
		}

		written = one

		return c.record(ctx, tx, scope, ResourceOccurrence, id, audit.EventUpdated, actor, map[string]string{
			KeyCommand: command,
			KeyState:   string(state),
			KeyReason:  reason,
		})
	}); err != nil {
		return nil, op.Error(err, "setting an occurrence's state")
	}

	return written, nil
}

// Move puts an appointment at another time.
//
// It writes a new occurrence and leaves the old one naming it, rather than
// editing the instant in place, because everything that already points at the
// old row — a reminder that has fired, a message on a thread — described a
// lesson at the old time and still does.
func (c *Commands) Move(ctx context.Context, scope tenancy.Scope, actor, id string, startsAt time.Time, reason string) (_ *Occurrence, err error) {
	ctx, op := c.o11y.Begin(ctx)
	defer op.End()

	var moved *Occurrence

	if err = c.client.WithTransaction(ctx, func(tx database.Tx) error {
		original, readErr := c.store.GetOccurrence(ctx, tx, scope, id)
		if readErr != nil {
			return readErr
		}

		if original.State != StateScheduled {
			return platformerrors.Wrap(ErrStateTransitionRefused, "only a standing appointment can be moved")
		}

		replacement, createErr := c.store.CreateOccurrence(ctx, tx, scope, &Occurrence{
			ID:         identifiers.New(),
			SeriesID:   original.SeriesID,
			OfferingID: original.OfferingID,
			StartsAt:   startsAt,
			EndsAt:     startsAt.Add(original.EndsAt.Sub(original.StartsAt)),
			Reason:     reason,
			Staff:      original.Staff,
			Attendees:  original.Attendees,
		})
		if createErr != nil {
			return createErr
		}

		if _, markErr := c.store.MarkOccurrenceMoved(ctx, tx, scope, id, replacement.ID, reason); markErr != nil {
			return markErr
		}

		moved = replacement

		return c.record(ctx, tx, scope, ResourceOccurrence, id, audit.EventUpdated, actor, map[string]string{
			KeyCommand:  CommandMove,
			KeyState:    string(StateMoved),
			KeyMovedTo:  replacement.ID,
			KeyStartsAt: startsAt.UTC().Format(time.RFC3339),
			KeyReason:   reason,
		})
	}); err != nil {
		return nil, op.Error(err, "moving an occurrence")
	}

	return moved, nil
}

// ScheduleMakeUp puts a replacement lesson against one that was cancelled.
//
// The make-up carries no series: it is not part of the standing rule and must
// not be swept up by a later edit to it.
func (c *Commands) ScheduleMakeUp(ctx context.Context, scope tenancy.Scope, actor, cancelledID string, startsAt time.Time, reason string) (_ *Occurrence, err error) {
	ctx, op := c.o11y.Begin(ctx)
	defer op.End()

	var makeUp *Occurrence

	if err = c.client.WithTransaction(ctx, func(tx database.Tx) error {
		cancelled, readErr := c.store.GetOccurrence(ctx, tx, scope, cancelledID)
		if readErr != nil {
			return readErr
		}

		if cancelled.State != StateCancelled {
			return platformerrors.Wrap(ErrStateTransitionRefused, "a make-up answers a cancellation")
		}

		written, createErr := c.store.CreateOccurrence(ctx, tx, scope, &Occurrence{
			ID:         identifiers.New(),
			OfferingID: cancelled.OfferingID,
			StartsAt:   startsAt,
			EndsAt:     startsAt.Add(cancelled.EndsAt.Sub(cancelled.StartsAt)),
			Reason:     reason,
			Replaces:   cancelled.ID,
			Staff:      cancelled.Staff,
			Attendees:  cancelled.Attendees,
		})
		if createErr != nil {
			return createErr
		}

		makeUp = written

		return c.record(ctx, tx, scope, ResourceOccurrence, written.ID, audit.EventCreated, actor, map[string]string{
			KeyCommand:  CommandMakeUp,
			KeyReplaces: cancelled.ID,
			KeyStartsAt: startsAt.UTC().Format(time.RFC3339),
			KeyReason:   reason,
		})
	}); err != nil {
		return nil, op.Error(err, "scheduling a make-up")
	}

	return makeUp, nil
}

// Reassign changes who is teaching one appointment, and never the series.
func (c *Commands) Reassign(ctx context.Context, scope tenancy.Scope, actor, id string, staff []string) (_ *Occurrence, err error) {
	ctx, op := c.o11y.Begin(ctx)
	defer op.End()

	var reassigned *Occurrence

	if err = c.client.WithTransaction(ctx, func(tx database.Tx) error {
		one, staffErr := c.store.SetOccurrenceStaff(ctx, tx, scope, id, staff)
		if staffErr != nil {
			return staffErr
		}

		reassigned = one

		return c.record(ctx, tx, scope, ResourceOccurrence, id, audit.EventUpdated, actor, map[string]string{
			KeyCommand: CommandReassign,
			KeyStaff:   joinIDs(one.Staff),
		})
	}); err != nil {
		return nil, op.Error(err, "reassigning an occurrence")
	}

	return reassigned, nil
}

// EndSeries stops a standing slot from a date and cancels what it had already
// put on the grid from that day onwards.
//
// The cancellations are the studio's rather than the household's: the studio
// is the one that stopped.
func (c *Commands) EndSeries(ctx context.Context, scope tenancy.Scope, actor, seriesID string, endsOn Date, reason string) (_ *Series, err error) {
	ctx, op := c.o11y.Begin(ctx)
	defer op.End()

	var ended *Series

	if err = c.client.WithTransaction(ctx, func(tx database.Tx) error {
		series, endErr := c.store.EndSeries(ctx, tx, scope, seriesID, endsOn)
		if endErr != nil {
			return endErr
		}

		// Everything standing after the last day the rule applies. The day
		// after the end date at midnight UTC is early enough to catch any
		// zone's morning and late enough to keep the final day's lessons.
		from := endsOn.AddDays(1).Time()

		standing, listErr := c.store.ListScheduledForSeriesFrom(ctx, tx, scope, seriesID, from)
		if listErr != nil {
			return listErr
		}

		for _, occurrence := range standing {
			if _, cancelErr := c.store.CancelOccurrence(ctx, tx, scope, occurrence.ID, CancelledByStudio, false, reason); cancelErr != nil {
				return cancelErr
			}
		}

		ended = series

		return c.record(ctx, tx, scope, ResourceSeries, seriesID, audit.EventUpdated, actor, map[string]string{
			KeyCommand:  CommandEndSeries,
			KeyEndsOn:   endsOn.String(),
			KeyAffected: strconv.Itoa(len(standing)),
			KeyReason:   reason,
		})
	}); err != nil {
		return nil, op.Error(err, "ending a series")
	}

	return ended, nil
}

// CloseStudio shuts the studio for a range of days: it records the closure, so
// the materializer stays off those days, and cancels what is already standing
// inside it.
func (c *Commands) CloseStudio(ctx context.Context, scope tenancy.Scope, actor string, startsOn, endsOn Date, reason string) (_ *Closure, err error) {
	ctx, op := c.o11y.Begin(ctx)
	defer op.End()

	var closure *Closure

	if err = c.client.WithTransaction(ctx, func(tx database.Tx) error {
		written, createErr := c.store.CreateClosure(ctx, tx, scope, &Closure{
			StartsOn: startsOn,
			EndsOn:   endsOn,
			Reason:   reason,
		})
		if createErr != nil {
			return createErr
		}

		// The range is inclusive at both ends, so the window runs to midnight
		// on the day after it.
		standing, listErr := c.store.ListScheduledInWindow(ctx, tx, scope, startsOn.Time(), endsOn.AddDays(1).Time())
		if listErr != nil {
			return listErr
		}

		for _, occurrence := range standing {
			if _, cancelErr := c.store.CancelOccurrence(ctx, tx, scope, occurrence.ID, CancelledByClosure, false, reason); cancelErr != nil {
				return cancelErr
			}
		}

		closure = written

		return c.record(ctx, tx, scope, ResourceClosure, written.ID, audit.EventCreated, actor, map[string]string{
			KeyCommand:  CommandCloseStudio,
			KeyStartsAt: startsOn.String(),
			KeyEndsOn:   endsOn.String(),
			KeyAffected: strconv.Itoa(len(standing)),
			KeyReason:   reason,
		})
	}); err != nil {
		return nil, op.Error(err, "closing the studio")
	}

	return closure, nil
}

// record writes the entry on the command's own transaction, in the studio's
// scope.
func (c *Commands) record(
	ctx context.Context,
	tx database.Tx,
	scope tenancy.Scope,
	resourceType, resourceID string,
	event audit.EventType,
	actor string,
	metadata map[string]string,
) error {
	if actor == "" {
		actor = audit.ActorUnattributed
	}

	for key, value := range metadata {
		if value == "" {
			delete(metadata, key)
		}
	}

	return c.recorder.Record(ctx, tx, scope, &audit.Entry{
		ID:           identifiers.New(),
		ResourceType: resourceType,
		ResourceID:   resourceID,
		EventType:    event,
		Actor:        audit.Actor{ID: actor, Type: audit.ActorUser},
		Metadata:     metadata,
	})
}

func joinIDs(ids []string) string {
	var out strings.Builder

	for i, id := range ids {
		if i > 0 {
			out.WriteString(",")
		}

		out.WriteString(id)
	}

	return out.String()
}
