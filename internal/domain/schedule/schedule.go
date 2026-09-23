// Package schedule is Fijo's grid: what a studio sells, the standing slots it
// sells them in, and the individual appointments those slots produce.
//
// The shape of the domain is one sentence. A Series is a rule — Tuesdays at
// four, every week, with Maria — and an Occurrence is a thing that happens. The
// materializer turns the rule into rows out to a horizon, and everything
// downstream points at the rows: reminders, threads, balances, the audit trail.
// The series is never the thing that fires, which is why a cover teacher is an
// edit to one occurrence and ending a series does not erase the lessons it
// already produced.
//
// Recurrence here is weekly with an interval of one or two, and nothing else.
// There is no RRULE and no intention of one. What Fijo needs is series
// materialization with exceptions, which is a noun with a table and therefore
// platform-go's tier; it lives here as an extraction candidate rather than
// being generalized from a single consumer. See platform-go#871.
//
// Attendees are not here yet. A series has an attendee set and so does an
// occurrence, but attendees are roster nouns and the roster is phase 2.
package schedule

import (
	"context"
	"time"

	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

// OfferingKind is what a studio is selling.
type OfferingKind string

// The kinds. What list price means depends on which one it is: per lesson for
// Single, for the whole pack for Package, per month for Recurring.
const (
	// KindSingle is one lesson at a time.
	KindSingle OfferingKind = "single"

	// KindPackage is a number of lessons bought at once, drawn down by a
	// balance ledger that arrives with the money.
	KindPackage OfferingKind = "package"

	// KindRecurring is "until otherwise noted" at a flat monthly price.
	KindRecurring OfferingKind = "recurring"
)

// Valid reports whether the kind is one this package knows.
func (k OfferingKind) Valid() bool {
	switch k {
	case KindSingle, KindPackage, KindRecurring:
		return true
	default:
		return false
	}
}

// OccurrenceState is where an appointment got to.
type OccurrenceState string

// The states. Every appointment begins Scheduled and reaches one of the other
// four, and every transition is revertible by the provider.
const (
	StateScheduled OccurrenceState = "scheduled"
	StateCompleted OccurrenceState = "completed"
	StateCancelled OccurrenceState = "cancelled"
	StateNoShow    OccurrenceState = "no_show"
	StateMoved     OccurrenceState = "moved"
)

// Valid reports whether the state is one this package knows.
func (s OccurrenceState) Valid() bool {
	switch s {
	case StateScheduled, StateCompleted, StateCancelled, StateNoShow, StateMoved:
		return true
	default:
		return false
	}
}

// Canceller is whose decision a cancellation was.
//
// It matters because the three are charged differently: a household cancelling
// inside the notice window spends a lesson, a studio cancelling does not, and a
// closure is nobody's fault.
type Canceller string

// The cancellers.
const (
	CancelledByHousehold Canceller = "household"
	CancelledByStudio    Canceller = "studio"
	CancelledByClosure   Canceller = "closure"
)

// Valid reports whether the canceller is one this package knows.
func (c Canceller) Valid() bool {
	switch c {
	case CancelledByHousehold, CancelledByStudio, CancelledByClosure:
		return true
	default:
		return false
	}
}

// The refusals.
var (
	ErrNilExecutor = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil query executor")
	ErrNilInput    = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil input")
	ErrEmptyID     = platformerrors.Wrap(platformerrors.ErrEmptyInputParameter, "empty identifier")

	// ErrNotFound is any row this package was asked for and does not have,
	// within the scope it was asked under. A row in another studio is not
	// found rather than forbidden, because saying which it is tells a caller
	// that somebody else's row exists.
	ErrNotFound = platformerrors.New("not found")

	// ErrInvalidOffering, ErrInvalidSeries and ErrInvalidOccurrence are the
	// shapes this package refuses before the database sees them. The database
	// refuses them too; these exist so the refusal has a name in Go.
	ErrInvalidOffering   = platformerrors.Wrap(platformerrors.ErrUnrecognizedInputValue, "invalid offering")
	ErrInvalidSeries     = platformerrors.Wrap(platformerrors.ErrUnrecognizedInputValue, "invalid series")
	ErrInvalidOccurrence = platformerrors.Wrap(platformerrors.ErrUnrecognizedInputValue, "invalid occurrence")
	ErrInvalidClosure    = platformerrors.Wrap(platformerrors.ErrUnrecognizedInputValue, "invalid closure")

	// ErrStateTransitionRefused is a command that named a row the state
	// machine would not let it touch — completing a moved occurrence,
	// cancelling one that is already cancelled.
	ErrStateTransitionRefused = platformerrors.New("occurrence is not in a state that permits this")
)

// Settings is the studio's own half of the schedule's rules.
type Settings struct {
	_ struct{} `json:"-"`

	CreatedAt     time.Time  `json:"createdAt"`
	LastUpdatedAt *time.Time `json:"lastUpdatedAt,omitempty"`

	// StudioID is the identity account the studio is.
	StudioID string `json:"studioID"`

	// NoticeWindow is how far ahead a household has to cancel for it not to
	// count as late.
	NoticeWindow time.Duration `json:"noticeWindow"`

	// HorizonWeeks is how far ahead the materializer writes.
	HorizonWeeks int `json:"horizonWeeks"`
}

// DefaultSettings is what a studio that has set nothing gets: a day's notice
// and eight weeks of grid.
func DefaultSettings(studioID string) *Settings {
	return &Settings{StudioID: studioID, NoticeWindow: 24 * time.Hour, HorizonWeeks: 8}
}

// Offering is what a studio sells.
type Offering struct {
	_ struct{} `json:"-"`

	CreatedAt     time.Time  `json:"createdAt"`
	LastUpdatedAt *time.Time `json:"lastUpdatedAt,omitempty"`
	ArchivedAt    *time.Time `json:"archivedAt,omitempty"`

	ID       string `json:"id"`
	StudioID string `json:"studioID"`

	// NameEN and NameES are both required, because every client-facing string
	// in Fijo ships in both and an offering's name is the one that ends up in
	// a reminder text.
	NameEN string `json:"nameEN"`
	NameES string `json:"nameES"`

	Kind     OfferingKind  `json:"kind"`
	Duration time.Duration `json:"duration"`

	// ListPrice is the advertised price in the smallest currency unit. Every
	// agreement may override it.
	ListPrice int64 `json:"listPrice"`

	// LessonCount, ExpiresAfter and Shareable belong to a package and are
	// zero for anything else. ExpiresAfter is zero for a pack that never
	// expires.
	LessonCount  int           `json:"lessonCount,omitempty"`
	ExpiresAfter time.Duration `json:"expiresAfter,omitempty"`
	Shareable    bool          `json:"shareable,omitempty"`
}

// Validate is the shape this package refuses before the database does.
func (o *Offering) Validate() error {
	switch {
	case o == nil:
		return ErrNilInput
	case o.StudioID == "":
		return platformerrors.Wrap(ErrInvalidOffering, "no studio")
	case o.NameEN == "" || o.NameES == "":
		return platformerrors.Wrap(ErrInvalidOffering, "both names are required")
	case !o.Kind.Valid():
		return platformerrors.Wrapf(ErrInvalidOffering, "unknown kind %q", o.Kind)
	case o.Duration <= 0:
		return platformerrors.Wrap(ErrInvalidOffering, "duration must be positive")
	case o.ListPrice < 0:
		return platformerrors.Wrap(ErrInvalidOffering, "list price may not be negative")
	case o.Kind == KindPackage && o.LessonCount <= 0:
		return platformerrors.Wrap(ErrInvalidOffering, "a package needs a lesson count")
	case o.Kind != KindPackage && (o.LessonCount != 0 || o.ExpiresAfter != 0 || o.Shareable):
		return platformerrors.Wrap(ErrInvalidOffering, "package columns belong to a package")
	}

	return nil
}

// Series is the standing slot.
type Series struct {
	_             struct{}     `json:"-"`
	CreatedAt     time.Time    `json:"createdAt"`
	LastUpdatedAt *time.Time   `json:"lastUpdatedAt,omitempty"`
	ArchivedAt    *time.Time   `json:"archivedAt,omitempty"`
	PriceOverride *int64       `json:"priceOverride,omitempty"`
	OfferingID    string       `json:"offeringID"`
	StudioID      string       `json:"studioID"`
	ID            string       `json:"id"`
	Staff         []string     `json:"staff"`
	StartsOn      Date         `json:"startsOn"`
	EndsOn        Date         `json:"endsOn"`
	StartTime     ClockTime    `json:"startTime"`
	Weekday       time.Weekday `json:"weekday"`
	IntervalWeeks int          `json:"intervalWeeks"`
}

// Validate is the shape this package refuses before the database does.
func (s *Series) Validate() error {
	switch {
	case s == nil:
		return ErrNilInput
	case s.StudioID == "":
		return platformerrors.Wrap(ErrInvalidSeries, "no studio")
	case s.OfferingID == "":
		return platformerrors.Wrap(ErrInvalidSeries, "no offering")
	case s.Weekday < time.Sunday || s.Weekday > time.Saturday:
		return platformerrors.Wrapf(ErrInvalidSeries, "weekday %d is not a day", int(s.Weekday))
	case !s.StartTime.Valid():
		return platformerrors.Wrapf(ErrInvalidSeries, "%s is not a time of day", s.StartTime)
	case s.IntervalWeeks != 1 && s.IntervalWeeks != 2:
		return platformerrors.Wrapf(ErrInvalidSeries, "interval of %d weeks is not weekly or fortnightly", s.IntervalWeeks)
	case s.StartsOn.IsZero():
		return platformerrors.Wrap(ErrInvalidSeries, "no start date")
	case !s.EndsOn.IsZero() && s.EndsOn.Before(s.StartsOn):
		return platformerrors.Wrap(ErrInvalidSeries, "ends before it starts")
	case s.PriceOverride != nil && *s.PriceOverride < 0:
		return platformerrors.Wrap(ErrInvalidSeries, "price override may not be negative")
	}

	return nil
}

// Runs reports whether the series still applies on a date: on or after the day
// it starts, on or before the day it ends, on its weekday, and on a week the
// interval lands on.
func (s *Series) Runs(on Date) bool {
	if s == nil || on.Before(s.StartsOn) {
		return false
	}

	if !s.EndsOn.IsZero() && on.After(s.EndsOn) {
		return false
	}

	if on.Weekday() != s.Weekday {
		return false
	}

	interval := max(s.IntervalWeeks, 1)

	// The fortnightly question is asked from the first day the rule could
	// apply rather than from StartsOn, so that a series starting on a Sunday
	// and running on Tuesdays counts weeks from its first Tuesday.
	first := s.FirstRunOnOrAfter(s.StartsOn)
	if first.IsZero() {
		return false
	}

	weeks := DaysBetween(first, on) / 7

	return weeks >= 0 && weeks%interval == 0
}

// FirstRunOnOrAfter is the first date on or after from that falls on the
// series' weekday, ignoring the interval. It is zero if the series has already
// ended by then.
func (s *Series) FirstRunOnOrAfter(from Date) Date {
	if s == nil {
		return Date{}
	}

	candidate := from
	for range 7 {
		if candidate.Weekday() == s.Weekday {
			if !s.EndsOn.IsZero() && candidate.After(s.EndsOn) {
				return Date{}
			}

			return candidate
		}

		candidate = candidate.AddDays(1)
	}

	return Date{}
}

// Occurrence is one appointment: the row everything else points at.
type Occurrence struct {
	_ struct{} `json:"-"`

	CreatedAt     time.Time  `json:"createdAt"`
	LastUpdatedAt *time.Time `json:"lastUpdatedAt,omitempty"`
	ArchivedAt    *time.Time `json:"archivedAt,omitempty"`

	// StartsAt and EndsAt are absolute instants, computed from the series'
	// wall-clock rule in the studio's zone when the row was written.
	StartsAt time.Time `json:"startsAt"`
	EndsAt   time.Time `json:"endsAt"`

	ID       string `json:"id"`
	StudioID string `json:"studioID"`

	// SeriesID is empty for a one-off appointment and for a make-up.
	SeriesID   string `json:"seriesID,omitempty"`
	OfferingID string `json:"offeringID"`

	State OccurrenceState `json:"state"`

	// CancelledBy and CancelledInsideNotice are set exactly when the state is
	// Cancelled. Whether it was late is judged against the notice window at
	// the moment of cancelling and then kept, because the window can be
	// changed afterwards and the judgement should not move with it.
	CancelledBy           Canceller `json:"cancelledBy,omitempty"`
	CancelledInsideNotice *bool     `json:"cancelledInsideNotice,omitempty"`

	// Reason is what the person doing it typed.
	Reason string `json:"reason,omitempty"`

	// MovedTo names what this occurrence became, and is set on the row left
	// behind. Replaces names the cancelled occurrence a make-up was scheduled
	// against, and is set on the new row.
	MovedTo  string `json:"movedTo,omitempty"`
	Replaces string `json:"replaces,omitempty"`

	// Staff is who is teaching this one, copied from the series when it was
	// materialized and editable per occurrence thereafter.
	Staff []string `json:"staff"`
}

// Closure is a date range the studio is shut. The range is inclusive at both
// ends, which is how a person says "we are closed the twenty-fourth through the
// twenty-sixth".
type Closure struct {
	_ struct{} `json:"-"`

	CreatedAt  time.Time  `json:"createdAt"`
	ArchivedAt *time.Time `json:"archivedAt,omitempty"`

	ID       string `json:"id"`
	StudioID string `json:"studioID"`
	Reason   string `json:"reason,omitempty"`

	StartsOn Date `json:"startsOn"`
	EndsOn   Date `json:"endsOn"`
}

// Covers reports whether the closure shuts the studio on a date.
func (c *Closure) Covers(on Date) bool {
	if c == nil {
		return false
	}

	return !on.Before(c.StartsOn) && !on.After(c.EndsOn)
}

// Validate is the shape this package refuses before the database does.
func (c *Closure) Validate() error {
	switch {
	case c == nil:
		return ErrNilInput
	case c.StudioID == "":
		return platformerrors.Wrap(ErrInvalidClosure, "no studio")
	case c.StartsOn.IsZero() || c.EndsOn.IsZero():
		return platformerrors.Wrap(ErrInvalidClosure, "a closure needs both ends")
	case c.EndsOn.Before(c.StartsOn):
		return platformerrors.Wrap(ErrInvalidClosure, "ends before it starts")
	}

	return nil
}

// Store reads and writes the grid. Writes take a Tx and reads take an
// executor, as every store on this stack does.
//
// Every method is scoped to a studio. The scope is not advice: it is a
// predicate on every statement, and a row in another studio is reported as
// ErrNotFound rather than refused, because the difference tells a caller that
// somebody else's row exists.
type Store interface {
	// GetSettings returns the studio's rules, or DefaultSettings for a studio
	// that has never set any.
	GetSettings(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope) (*Settings, error)

	// SetSettings writes them.
	SetSettings(ctx context.Context, tx database.Tx, scope tenancy.Scope, settings *Settings) (*Settings, error)

	// CreateOffering writes a new offering, minting its identifier.
	CreateOffering(ctx context.Context, tx database.Tx, scope tenancy.Scope, offering *Offering) (*Offering, error)

	// GetOffering reads one, or ErrNotFound.
	GetOffering(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, id string) (*Offering, error)

	// ListOfferings reads the studio's, archived ones included only if asked.
	ListOfferings(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, includeArchived bool) ([]*Offering, error)

	// UpdateOffering rewrites one.
	UpdateOffering(ctx context.Context, tx database.Tx, scope tenancy.Scope, offering *Offering) (*Offering, error)

	// ArchiveOffering retires one. Series already pointing at it keep working.
	ArchiveOffering(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string) error

	// CreateSeries writes a standing slot and its staff.
	CreateSeries(ctx context.Context, tx database.Tx, scope tenancy.Scope, series *Series) (*Series, error)

	// GetSeries reads one with its staff, or ErrNotFound.
	GetSeries(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, id string) (*Series, error)

	// ListSeries reads the studio's, with their staff.
	ListSeries(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, includeArchived bool) ([]*Series, error)

	// ListLiveSeries is the materializer's question and crosses studios: every
	// series that could still produce an occurrence between today and the
	// horizon. It takes an executor rather than a Tx because it is a read the
	// worker makes before it decides to write anything.
	ListLiveSeries(ctx context.Context, q database.SQLQueryExecutor, today, horizon Date) ([]*Series, error)

	// UpdateSeries rewrites the rule and its staff.
	UpdateSeries(ctx context.Context, tx database.Tx, scope tenancy.Scope, series *Series) (*Series, error)

	// EndSeries sets the last day the rule applies. It is not an archive: what
	// the series already produced stays readable.
	EndSeries(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string, endsOn Date) (*Series, error)

	// ArchiveSeries retires a rule that should never have existed.
	ArchiveSeries(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string) error

	// MaterializeOccurrence writes the row a series produces at an instant and
	// reports whether it wrote one. A row already there is not an error: the
	// worker runs again and must be able to.
	MaterializeOccurrence(ctx context.Context, tx database.Tx, scope tenancy.Scope, occurrence *Occurrence) (*Occurrence, bool, error)

	// CreateOccurrence writes an appointment no series produced: a one-off, or
	// a make-up against a cancellation.
	CreateOccurrence(ctx context.Context, tx database.Tx, scope tenancy.Scope, occurrence *Occurrence) (*Occurrence, error)

	// GetOccurrence reads one with its staff, or ErrNotFound.
	GetOccurrence(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, id string) (*Occurrence, error)

	// ListOccurrencesInWindow is the week view: everything between two
	// instants whatever state it is in, because a cancelled lesson is
	// something the owner needs to see in the grid.
	ListOccurrencesInWindow(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, from, until time.Time) ([]*Occurrence, error)

	// ListOccurrencesForSeries reads everything one rule produced.
	ListOccurrencesForSeries(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, seriesID string) ([]*Occurrence, error)

	// ListScheduledForSeriesFrom and ListScheduledInWindow are what ending a
	// series and closing the studio ask for: the rows still standing that the
	// command is about to cancel.
	ListScheduledForSeriesFrom(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, seriesID string, from time.Time) ([]*Occurrence, error)
	ListScheduledInWindow(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, from, until time.Time) ([]*Occurrence, error)

	// SetOccurrenceState is completed, no-show, and the revert back to
	// scheduled. It refuses a moved occurrence.
	SetOccurrenceState(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string, state OccurrenceState, reason string) (*Occurrence, error)

	// CancelOccurrence records whose decision it was and whether it was late.
	// It refuses anything not currently scheduled.
	CancelOccurrence(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string, by Canceller, insideNotice bool, reason string) (*Occurrence, error)

	// MarkOccurrenceMoved is the second half of a move: the row left behind
	// names what it became.
	MarkOccurrenceMoved(ctx context.Context, tx database.Tx, scope tenancy.Scope, id, movedToID, reason string) (*Occurrence, error)

	// SetOccurrenceStaff replaces who is teaching this one.
	SetOccurrenceStaff(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string, staff []string) (*Occurrence, error)

	// CreateClosure shuts the studio for a range.
	CreateClosure(ctx context.Context, tx database.Tx, scope tenancy.Scope, closure *Closure) (*Closure, error)

	// ListClosures reads the studio's.
	ListClosures(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope) ([]*Closure, error)

	// ListClosuresForStudios is the materializer's question, asked once for
	// the studios whose series it is about to walk.
	ListClosuresForStudios(ctx context.Context, q database.SQLQueryExecutor, studioIDs []string) (map[string][]*Closure, error)

	// ArchiveClosure reopens the studio.
	ArchiveClosure(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string) error
}
