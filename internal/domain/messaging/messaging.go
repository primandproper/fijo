// Package messaging is what a studio sends its clients. In v1 that is the
// reminder: an email to each contact on a household before each of its lessons,
// in the contact's language, from the studio's name, with the owner's own
// address to write back to.
//
// When a reminder is owed is never stored ahead of time. Lessons are
// cancelled, moved, re-timed and handed to new students by a dozen writes in
// the schedule, and a reminder scheduled at booking would have to be found and
// moved by every one of them. Instead the worker asks, once a minute, which
// reminders are owed now (Store.Candidates and Candidate.Owed), reading the
// lessons as they are. What is stored is a reminder once it is owed: the record
// of what was sent, and, through its unique key, the fence that sends it once
// across the fleet. Delivery rides a platform timer written on the same
// transaction, so a reminder written is a reminder that will be tried, with the
// timer's lease and retries, and nothing in Fijo re-implements an outbox.
//
// SMS joins later as a second channel; nothing here is email-shaped but the
// rendering and the one column that says so.
package messaging

import (
	"context"
	"time"

	"github.com/primandproper/fijo/internal/domain/roster"

	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

// The refusals.
var (
	ErrNilExecutor = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil query executor")
	ErrNilInput    = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil input")
	ErrEmptyID     = platformerrors.Wrap(platformerrors.ErrEmptyInputParameter, "empty identifier")

	// ErrNotFound is a reminder this package was asked for and does not have.
	ErrNotFound = platformerrors.New("not found")

	// ErrNotStudioScoped is a read or a write handed the global scope.
	ErrNotStudioScoped = platformerrors.New("messaging is studio-scoped and the scope names no studio")

	// ErrInvalidSettings is a studio's settings this package refuses.
	ErrInvalidSettings = platformerrors.Wrap(platformerrors.ErrUnrecognizedInputValue, "invalid reminder settings")

	// ErrNoReplyTo is reminders turned on with nowhere for a client to write
	// back to. Every reminder tells the client where to write to change a
	// lesson, so there is no reminder without one.
	ErrNoReplyTo = platformerrors.Wrap(ErrInvalidSettings, "reminders need an address for clients to write back to")
)

// Status is where a reminder is.
type Status string

// The statuses.
const (
	// Queued is written and not yet taken by the email provider.
	Queued Status = "queued"

	// Sent is taken by the email provider.
	Sent Status = "sent"

	// Skipped is a reminder that stopped being owed between being written and
	// being sent: the lesson was cancelled or moved, or the contact's or the
	// studio's reminders were turned off.
	Skipped Status = "skipped"

	// Failed is the provider refusing it past every try.
	Failed Status = "failed"
)

// Settings are a studio's reminders: whether they go, and where a reply goes.
type Settings struct {
	_ struct{} `json:"-"`

	// RemindersOnSince is when reminders were last turned on. A reminder owed
	// before it is not sent, so turning them on is not a burst of reminders for
	// the day already under way.
	RemindersOnSince *time.Time `json:"remindersOnSince,omitempty"`

	StudioID string `json:"studioID"`

	// ReplyTo is the owner's own address, which every reminder names for
	// changing a lesson.
	ReplyTo string `json:"replyTo"`

	// RemindersOn is off until the owner turns it on. A studio moving from a
	// system that still sends its own wants one set of reminders, not two.
	RemindersOn bool `json:"remindersOn"`
}

// Validate is the shape this package refuses before the database does.
func (s *Settings) Validate() error {
	switch {
	case s == nil:
		return ErrNilInput
	case s.StudioID == "":
		return platformerrors.Wrap(ErrInvalidSettings, "no studio")
	case s.RemindersOn && s.ReplyTo == "":
		return ErrNoReplyTo
	}

	if s.ReplyTo != "" {
		if _, err := roster.NormalizeEmail(s.ReplyTo); err != nil {
			return platformerrors.Wrapf(ErrInvalidSettings, "%q is not an email address", s.ReplyTo)
		}
	}

	return nil
}

// Candidate is one contact on one lesson in the week ahead whose household's
// earlier reminder has come due: the facts Owed decides from, and everything a
// reminder is rendered from but the studio's name and zone.
type Candidate struct {
	_ struct{} `json:"-"`

	StartsAt            time.Time `json:"startsAt"`
	OccurrenceCreatedAt time.Time `json:"occurrenceCreatedAt"`
	RemindersOnSince    time.Time `json:"remindersOnSince"`

	OccurrenceID   string          `json:"occurrenceID"`
	StudioID       string          `json:"studioID"`
	OfferingNameEN string          `json:"offeringNameEN"`
	OfferingNameES string          `json:"offeringNameES"`
	HouseholdID    string          `json:"householdID"`
	ContactID      string          `json:"contactID"`
	ContactName    string          `json:"contactName"`
	ContactEmail   string          `json:"contactEmail"`
	ContactLang    roster.Language `json:"contactLanguage"`
	ReplyTo        string          `json:"replyTo"`

	// OtherAttendees are the household's attendees on the lesson other than
	// the contact, in name order.
	OtherAttendees []string `json:"otherAttendees"`

	// FirstReminder and SecondReminder are the household's, zero for none.
	FirstReminder  time.Duration `json:"firstReminder"`
	SecondReminder time.Duration `json:"secondReminder"`

	// SmallestWritten is the smallest offset a reminder has already been
	// written at for this contact and lesson, or zero for none.
	SmallestWritten time.Duration `json:"smallestWritten"`

	// ContactAttends is the contact being an attendee on the lesson: an adult
	// student, reminded of their own lesson.
	ContactAttends bool `json:"contactAttends"`
}

// Owed is which of the candidate's reminders is owed at now, if one is.
//
// It is the latest-firing reminder that has come due — the hour before rather
// than the day before, once both have — so a worker that was down for a day
// sends one reminder, not two. And it is not owed at all when that instant was
// before reminders were turned on, before the lesson was booked, or when a
// reminder at the same offset or a later-firing one has been written already.
// A reminder that is not owed now may be owed later: the day before held back
// for a lesson booked this morning leaves the hour before to go.
func (c *Candidate) Owed(now time.Time) (time.Duration, bool) {
	var owed time.Duration

	for _, offset := range []time.Duration{c.FirstReminder, c.SecondReminder} {
		if offset <= 0 || c.StartsAt.Add(-offset).After(now) {
			continue
		}

		if owed == 0 || offset < owed {
			owed = offset
		}
	}

	if owed == 0 || !c.StartsAt.After(now) {
		return 0, false
	}

	fires := c.StartsAt.Add(-owed)

	switch {
	case fires.Before(c.RemindersOnSince):
		return 0, false
	case fires.Before(c.OccurrenceCreatedAt):
		return 0, false
	case c.SmallestWritten > 0 && c.SmallestWritten <= owed:
		return 0, false
	}

	return owed, true
}

// Reminder is one reminder owed to one contact about one lesson.
type Reminder struct {
	_ struct{} `json:"-"`

	CreatedAt time.Time  `json:"createdAt"`
	SentAt    *time.Time `json:"sentAt,omitempty"`

	// LessonStartsAt, ContactName and HouseholdName are filled by the reads,
	// as those rows are now.
	LessonStartsAt time.Time `json:"lessonStartsAt"`

	ID           string          `json:"id"`
	StudioID     string          `json:"studioID"`
	OccurrenceID string          `json:"occurrenceID"`
	HouseholdID  string          `json:"householdID"`
	ContactID    string          `json:"contactID"`
	ToAddress    string          `json:"toAddress"`
	ToName       string          `json:"toName"`
	Language     roster.Language `json:"language"`
	Subject      string          `json:"subject"`
	BodyHTML     string          `json:"bodyHTML"`
	Status       Status          `json:"status"`

	// Reason is why a reminder was skipped or failed, or the last provider
	// error on one still queued.
	Reason string `json:"reason"`

	ContactName   string `json:"contactName"`
	HouseholdName string `json:"householdName"`

	// Offset is which of the household's reminders this is: how long before
	// the lesson it was owed.
	Offset time.Duration `json:"offset"`

	Attempts int `json:"attempts"`
}

// Delivery is a reminder as its timer finds it, held for the length of the
// transaction it was read on, with what decides whether it should still go.
type Delivery struct {
	OccurrenceStartsAt time.Time
	OccurrenceState    string
	ReplyTo            string
	Reminder
	ContactWantsIt bool
	RemindersOn    bool
}

// Store reads and writes the reminders. Writes take a Tx and reads take an
// executor.
//
// Candidates and the delivery methods are the worker's and cross every studio,
// the way the materializer's reads do: the worker serves the fleet, and a
// reminder found by its identifier carries its studio on its row. Everything an
// owner's request reaches is scoped.
type Store interface {
	// GetSettings reads a studio's settings, which are off with no reply
	// address for a studio that has never saved any.
	GetSettings(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope) (*Settings, error)

	// SaveSettings writes a studio's settings.
	SaveSettings(ctx context.Context, tx database.Tx, scope tenancy.Scope, settings *Settings) (*Settings, error)

	// Candidates reads every contact on every lesson starting after now and
	// no later than horizon whose household's earlier reminder has come due,
	// in every studio with reminders on.
	Candidates(ctx context.Context, q database.SQLQueryExecutor, now, horizon time.Time) ([]*Candidate, error)

	// WriteReminder records a reminder as owed, reporting false for one
	// already written by somebody else.
	WriteReminder(ctx context.Context, tx database.Tx, reminder *Reminder) (bool, error)

	// LockForDelivery reads a reminder and holds it until tx ends.
	LockForDelivery(ctx context.Context, tx database.Tx, id string) (*Delivery, error)

	// MarkSent records the provider taking it.
	MarkSent(ctx context.Context, tx database.Tx, id string) error

	// MarkUnsent retires one that did not go, as Skipped or Failed.
	MarkUnsent(ctx context.Context, tx database.Tx, id string, status Status, reason string) error

	// RecordAttempt counts a try the provider refused, returning the count.
	RecordAttempt(ctx context.Context, tx database.Tx, id, reason string) (int, error)

	// ListReminders reads a studio's reminders, newest first; householdID
	// narrows it to one household when it is not empty.
	ListReminders(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, householdID string, limit int) ([]*Reminder, error)
}
