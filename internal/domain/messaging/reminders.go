package messaging

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/primandproper/fijo/internal/domain/roster"

	"github.com/primandproper/platform-go/v15/metering"
	"github.com/primandproper/primitives-go/v2/database"
	"github.com/primandproper/primitives-go/v2/email"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/identifiers"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

const remindersName = "reminders"

// MeterEmails is the meter every reminder email is counted on, one per email
// the provider took, against the studio that sent it.
const MeterEmails = "reminder_emails"

// MaxAttempts is how many times the provider may refuse a reminder before it
// is given up as failed. The timer under it retries a minute apart, so a
// provider down for a few minutes costs lateness and nothing else.
const MaxAttempts = 5

// The refusals the constructor makes.
var (
	ErrNilDependency = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "a reminder dependency is nil")
	ErrNoFromAddress = platformerrors.Wrap(platformerrors.ErrEmptyInputParameter, "reminders need a from-address")
)

// Studios answers what a reminder needs about a studio that lives in the
// identity directory: what it is called and what time it is there.
type Studios interface {
	ZoneOf(ctx context.Context, studioID string) (*time.Location, error)
	NameOf(ctx context.Context, studioID string) (string, error)
}

// Scheduler writes the timer that delivers a reminder, on the transaction that
// writes the reminder. A *timers.Timers[string] is one.
type Scheduler interface {
	ScheduleAt(ctx context.Context, tx database.Tx, key string, runAt time.Time, payload []byte) error
}

// Meter counts a sent reminder on the transaction that records it sent. A
// *metering.DurableRecorder is one.
type Meter interface {
	Record(ctx context.Context, tx database.Tx, scope tenancy.Scope, usage ...metering.Usage) error
}

// Reminders finds the reminders owed, writes them, and delivers them.
type Reminders struct {
	o11y    observability.Observer
	client  database.Client
	store   Store
	studios Studios
	timers  Scheduler
	emailer email.Emailer
	meter   Meter
	now     func() time.Time
	newID   func() string

	// from is the address every reminder is sent from, under the studio's
	// name.
	from string
}

// RemindersOption configures a Reminders.
type RemindersOption func(*Reminders)

// WithRemindersPillars attaches the observability pillars.
func WithRemindersPillars(p *observability.Pillars) RemindersOption {
	return func(r *Reminders) {
		if p != nil {
			r.o11y = observability.NewObserver(remindersName, p.Logger, p.TracerProvider)
		}
	}
}

// WithRemindersClock replaces the clock, for a test that needs it to be a
// particular minute.
func WithRemindersClock(now func() time.Time) RemindersOption {
	return func(r *Reminders) {
		if now != nil {
			r.now = now
		}
	}
}

// NewReminders builds the reminder service. The scheduler and the meter may
// be nil for a process that only sends test reminders, which is what serve
// does; Plan and Deliver refuse without them.
func NewReminders(
	client database.Client,
	store Store,
	studios Studios,
	timers Scheduler,
	emailer email.Emailer,
	meter Meter,
	from string,
	opts ...RemindersOption,
) (*Reminders, error) {
	switch {
	case client == nil, store == nil, studios == nil, emailer == nil:
		return nil, ErrNilDependency
	case from == "":
		return nil, ErrNoFromAddress
	}

	r := &Reminders{
		o11y:    observability.NewObserver(remindersName, nil, nil),
		client:  client,
		store:   store,
		studios: studios,
		timers:  timers,
		emailer: emailer,
		meter:   meter,
		now:     time.Now,
		newID:   identifiers.New,
		from:    from,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}

	return r, nil
}

// RunOnce writes every reminder owed now and the timer that delivers each,
// reporting how many it wrote. A studio whose name or zone cannot be read is
// logged and passed over, and tried again next minute.
func (r *Reminders) RunOnce(ctx context.Context) (_ int64, err error) {
	ctx, op := r.o11y.Begin(ctx)
	defer op.End()

	if r.timers == nil {
		return 0, op.Error(ErrNilDependency, "planning reminders without a timer set")
	}

	now := r.now()

	candidates, err := r.store.Candidates(ctx, r.client.Reader(), now, now.Add(roster.MaxReminder))
	if err != nil {
		return 0, op.Error(err, "reading reminder candidates")
	}

	var (
		written int64
		failed  []error
	)

	for _, candidate := range candidates {
		offset, owed := candidate.Owed(now)
		if !owed {
			continue
		}

		reminder, renderErr := r.reminderFor(ctx, candidate, offset, now)
		if renderErr != nil {
			failed = append(failed, renderErr)

			continue
		}

		writeErr := r.client.WithTransaction(ctx, func(tx database.Tx) error {
			wrote, txErr := r.store.WriteReminder(ctx, tx, reminder)
			if txErr != nil || !wrote {
				return txErr
			}

			written++

			return r.timers.ScheduleAt(ctx, tx, reminder.ID, now, nil)
		})
		if writeErr != nil {
			failed = append(failed, writeErr)
		}
	}

	if len(failed) > 0 {
		op.Acknowledge(stderrors.Join(failed...), "writing %d of the reminders owed", len(failed))
	}

	return written, nil
}

func (r *Reminders) reminderFor(ctx context.Context, c *Candidate, offset time.Duration, now time.Time) (*Reminder, error) {
	zone, err := r.studios.ZoneOf(ctx, c.StudioID)
	if err != nil {
		return nil, err
	}

	name, err := r.studios.NameOf(ctx, c.StudioID)
	if err != nil {
		return nil, err
	}

	offering := c.OfferingNameEN
	if c.ContactLang == roster.Spanish && c.OfferingNameES != "" {
		offering = c.OfferingNameES
	}

	rendered := Render(&Lesson{
		StartsAt:       c.StartsAt.In(zone),
		Now:            now.In(zone),
		StudioName:     name,
		ContactName:    c.ContactName,
		OfferingName:   offering,
		ReplyTo:        c.ReplyTo,
		Language:       c.ContactLang,
		Others:         c.OtherAttendees,
		ContactAttends: c.ContactAttends,
	})

	return &Reminder{
		ID:           r.newID(),
		StudioID:     c.StudioID,
		OccurrenceID: c.OccurrenceID,
		HouseholdID:  c.HouseholdID,
		ContactID:    c.ContactID,
		ToAddress:    c.ContactEmail,
		ToName:       c.ContactName,
		Language:     c.ContactLang,
		Subject:      rendered.Subject,
		BodyHTML:     rendered.HTML,
		Status:       Queued,
		Offset:       offset,
	}, nil
}

// lateness is how far past its instant a reminder may still go. A provider
// down for an hour delivers the day-before reminder an hour late, which is
// still worth having; the lesson starting is the bound that matters, and is
// checked on its own.
const lateness = 6 * time.Hour

// Deliver sends one reminder: the timer's handler. It is idempotent, because a
// timer fires at least once: a reminder already sent, skipped or failed is
// left alone, and the row is held while the provider is called so a second
// firing waits and then finds it sent.
//
// A reminder that stopped being owed since it was written — the lesson
// cancelled, moved or already started, the contact's or the studio's
// reminders turned off — is skipped rather than sent. A provider refusal is
// counted and handed back to the timer to retry, up to MaxAttempts.
func (r *Reminders) Deliver(ctx context.Context, id string) (err error) {
	ctx, op := r.o11y.Begin(ctx, observability.WithValue(keyReminder, id))
	defer op.End()

	if r.meter == nil {
		return op.Error(ErrNilDependency, "delivering a reminder without a meter")
	}

	var sendErr error

	err = r.client.WithTransaction(ctx, func(tx database.Tx) error {
		d, lockErr := r.store.LockForDelivery(ctx, tx, id)
		if lockErr != nil {
			if stderrors.Is(lockErr, ErrNotFound) {
				// Deleted with its lesson or its contact: nothing is owed.
				return nil
			}

			return lockErr
		}

		if d.Status != Queued {
			return nil
		}

		if reason := r.stale(d); reason != "" {
			return r.store.MarkUnsent(ctx, tx, id, Skipped, reason)
		}

		name, nameErr := r.studios.NameOf(ctx, d.StudioID)
		if nameErr != nil {
			return nameErr
		}

		sendErr = r.emailer.SendEmail(ctx, &email.OutboundEmailMessage{
			ToAddress:      d.ToAddress,
			ToName:         d.ToName,
			FromAddress:    r.from,
			FromName:       name,
			ReplyToAddress: d.ReplyTo,
			ReplyToName:    name,
			Subject:        d.Subject,
			HTMLContent:    d.BodyHTML,
		})
		if sendErr != nil {
			attempts, attemptErr := r.store.RecordAttempt(ctx, tx, id, sendErr.Error())
			if attemptErr != nil {
				return attemptErr
			}

			if attempts >= MaxAttempts {
				op.Acknowledge(sendErr, "giving up on reminder %q after %d attempts", id, attempts)
				reason := "The email provider refused it: " + sendErr.Error()
				sendErr = nil

				return r.store.MarkUnsent(ctx, tx, id, Failed, reason)
			}

			return nil
		}

		if markErr := r.store.MarkSent(ctx, tx, id); markErr != nil {
			return markErr
		}

		return r.meter.Record(ctx, tx, tenancy.Of(d.StudioID), metering.Usage{
			Subject:        d.StudioID,
			Meter:          MeterEmails,
			Quantity:       1,
			IdempotencyKey: id,
		})
	})
	if err != nil {
		return op.Error(err, "delivering a reminder")
	}

	if sendErr != nil {
		return op.Error(sendErr, "sending a reminder")
	}

	return nil
}

// stale is why a written reminder should no longer go, or empty when it should.
func (r *Reminders) stale(d *Delivery) string {
	now := r.now()
	due := d.OccurrenceStartsAt.Add(-d.Offset)

	switch {
	case !d.RemindersOn:
		return "Reminders were turned off for the studio."
	case !d.ContactWantsIt:
		return "Reminders were turned off for this contact."
	case d.OccurrenceState == "cancelled":
		return "The lesson was cancelled."
	case d.OccurrenceState == "moved":
		return "The lesson was moved."
	case d.OccurrenceState != "scheduled":
		return "The lesson is no longer scheduled."
	case !d.OccurrenceStartsAt.After(now):
		return "The lesson had started before it could be sent."
	case due.After(now.Add(time.Minute)):
		return "The lesson's time changed after it was written."
	case now.Sub(due) > lateness:
		return "It could not be sent in time."
	}

	return ""
}

// SendTest mails a sample reminder, as a client in the given language would
// get it, to the studio's own reply address: the owner seeing what the clients
// will see before turning reminders on.
func (r *Reminders) SendTest(ctx context.Context, scope tenancy.Scope, language roster.Language) (sentTo string, err error) {
	ctx, op := r.o11y.Begin(ctx)
	defer op.End()

	settings, err := r.store.GetSettings(ctx, r.client.Reader(), scope)
	if err != nil {
		return "", op.Error(err, "reading reminder settings")
	}

	if settings.ReplyTo == "" {
		return "", op.Error(ErrNoReplyTo, "sending a test reminder")
	}

	studio := scope.Owner()

	zone, err := r.studios.ZoneOf(ctx, studio)
	if err != nil {
		return "", op.Error(err, "sending a test reminder")
	}

	name, err := r.studios.NameOf(ctx, studio)
	if err != nil {
		return "", op.Error(err, "sending a test reminder")
	}

	now := r.now().In(zone)
	tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 16, 0, 0, 0, zone)

	sample := &Lesson{
		StartsAt:     tomorrow,
		Now:          now,
		StudioName:   name,
		ContactName:  "Lucía García",
		OfferingName: "Piano 30",
		ReplyTo:      settings.ReplyTo,
		Language:     language,
		Others:       []string{"Sofía García"},
	}

	rendered := Render(sample)

	subject := "[Test] " + rendered.Subject
	if language == roster.Spanish {
		subject = "[Prueba] " + rendered.Subject
	}

	if err = r.emailer.SendEmail(ctx, &email.OutboundEmailMessage{
		ToAddress:      settings.ReplyTo,
		FromAddress:    r.from,
		FromName:       name,
		ReplyToAddress: settings.ReplyTo,
		ReplyToName:    name,
		Subject:        subject,
		HTMLContent:    rendered.HTML,
	}); err != nil {
		return "", op.Error(err, "sending a test reminder")
	}

	return settings.ReplyTo, nil
}
