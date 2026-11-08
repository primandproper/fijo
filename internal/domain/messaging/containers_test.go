package messaging_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/primandproper/fijo/internal/bootstrap"
	"github.com/primandproper/fijo/internal/domain/messaging"
	"github.com/primandproper/fijo/internal/domain/roster"
	"github.com/primandproper/fijo/internal/domain/schedule"
	"github.com/primandproper/fijo/internal/importing"
	"github.com/primandproper/fijo/internal/importing/acuity"
	"github.com/primandproper/fijo/internal/testutils/pgclient"
	"github.com/primandproper/fijo/internal/testutils/seed"
	"github.com/primandproper/fijo/internal/workers/reminders"

	"github.com/primandproper/platform-go/v15/metering"
	"github.com/primandproper/platform-go/v15/timers"
	"github.com/primandproper/primitives-go/v2/database"
	"github.com/primandproper/primitives-go/v2/email"
	"github.com/primandproper/primitives-go/v2/tenancy"
	"github.com/primandproper/primitives-go/v2/testutils/containers/pgtest"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// mailbox is an emailer that keeps what it is handed, or refuses it.
type mailbox struct {
	refuse error
	sent   []*email.OutboundEmailMessage
	mu     sync.Mutex
}

func (m *mailbox) SendEmail(_ context.Context, msg *email.OutboundEmailMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.refuse != nil {
		return m.refuse
	}

	m.sent = append(m.sent, msg)

	return nil
}

func (m *mailbox) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.sent)
}

// studio answers the identity questions without identity.
type studio struct{ zone *time.Location }

func (s studio) ZoneOf(context.Context, string) (*time.Location, error) { return s.zone, nil }
func (studio) NameOf(context.Context, string) (string, error)           { return "Casa de Musica", nil }

// clock is a settable now.
type clock struct {
	now time.Time
	mu  sync.Mutex
}

func (c *clock) get() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}

// The reminders are timed against the database's own clock — when they were
// turned on, when a lesson was booked — so the lessons are put a couple of days
// after whenever the test runs, rather than on a date that will one day be in
// the past.
func TestRemindersAgainstPostgres(t *testing.T) {
	t.Parallel()

	pgtest.Run(t, func(ctx context.Context, pg *pgtest.Instance) {
		client := pgclient.Open(t, ctx, pg)

		central, err := time.LoadLocation("US/Central")
		must.NoError(t, err)

		registered := seed.Studio(t, ctx, seed.New(t, client), &bootstrap.Studio{
			Username: "maria", EmailAddress: "maria@example.com",
			Password: "a good long password", Name: "Casa de Musica", TimeZone: "US/Central",
		})
		scope := tenancy.Of(registered.Account.ID)

		rosterStore, err := roster.NewSQLStore()
		must.NoError(t, err)

		scheduleStore, err := schedule.NewSQLStore()
		must.NoError(t, err)

		store, err := messaging.NewSQLStore()
		must.NoError(t, err)

		// Sofía's piano is two days out at four; Ana's voice is the day after.
		realNow := time.Now().In(central)
		day := time.Date(realNow.Year(), realNow.Month(), realNow.Day(), 0, 0, 0, 0, central)
		sofia := day.AddDate(0, 0, 2).Add(16 * time.Hour)
		ana := day.AddDate(0, 0, 3).Add(18 * time.Hour)

		writeLessons(t, ctx, client, rosterStore, scheduleStore, scope, registered.User.ID, central, realNow, sofia, ana)

		now := &clock{now: sofia.Add(-23 * time.Hour)}

		set, err := timers.New[string](ctx, &timers.Config{Name: reminders.TimerSet}, client)
		must.NoError(t, err)

		meteringStore, err := metering.NewSQLStore(client)
		must.NoError(t, err)

		registry, err := reminders.Registry()
		must.NoError(t, err)

		recorder, err := metering.NewDurableRecorder(ctx, &metering.RecorderConfig{}, meteringStore, registry)
		must.NoError(t, err)

		box := &mailbox{}

		r, err := messaging.NewReminders(client, store, studio{zone: central}, set, box, recorder, "reminders@fijo.test",
			messaging.WithRemindersClock(now.get))
		must.NoError(t, err)

		listed := func() []*messaging.Reminder {
			out, listErr := store.ListReminders(ctx, client.Reader(), scope, "", 0)
			must.NoError(t, listErr)

			return out
		}

		plan := func() int64 {
			written, planErr := r.RunOnce(ctx)
			must.NoError(t, planErr)

			return written
		}

		t.Run("nothing goes while reminders are off", func(t *testing.T) {
			test.Eq(t, int64(0), plan())
		})

		t.Run("turning them on needs somewhere to reply to", func(t *testing.T) {
			refusal := client.WithTransaction(ctx, func(tx database.Tx) error {
				_, saveErr := store.SaveSettings(ctx, tx, scope, &messaging.Settings{RemindersOn: true})

				return saveErr
			})
			test.ErrorIs(t, refusal, messaging.ErrNoReplyTo)

			must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
				saved, saveErr := store.SaveSettings(ctx, tx, scope, &messaging.Settings{RemindersOn: true, ReplyTo: " Maria@Example.com "})
				if saveErr == nil {
					test.Eq(t, "maria@example.com", saved.ReplyTo)
					test.NotNil(t, saved.RemindersOnSince)
				}

				return saveErr
			}))
		})

		var dayBefore *messaging.Reminder

		t.Run("the day before is written once, with its timer", func(t *testing.T) {
			test.Eq(t, int64(1), plan())
			test.Eq(t, int64(0), plan(), test.Sprint("a second pass finds it written"))

			all := listed()
			must.SliceLen(t, 1, all)

			dayBefore = all[0]
			test.Eq(t, messaging.Queued, dayBefore.Status)
			test.Eq(t, "lucia@example.com", dayBefore.ToAddress)
			test.Eq(t, roster.Spanish, dayBefore.Language)
			test.Eq(t, 24*time.Hour, dayBefore.Offset)
			test.StrContains(t, dayBefore.Subject, "Recordatorio: Sofía García, Piano 30, mañana a las 4:00 p. m.")
			test.StrContains(t, dayBefore.BodyHTML, "maria@example.com")

			stats, statsErr := set.Stats(ctx)
			must.NoError(t, statsErr)
			test.Eq(t, int64(1), stats.Outstanding)
		})

		t.Run("delivering sends it once and counts it", func(t *testing.T) {
			must.NoError(t, r.Deliver(ctx, dayBefore.ID))
			must.NoError(t, r.Deliver(ctx, dayBefore.ID), must.Sprint("a timer fires at least once"))

			must.Eq(t, 1, box.count())

			sent := box.sent[0]
			test.Eq(t, "lucia@example.com", sent.ToAddress)
			test.Eq(t, "Casa de Musica", sent.FromName)
			test.Eq(t, "reminders@fijo.test", sent.FromAddress)
			test.Eq(t, dayBefore.Subject, sent.Subject)

			test.Eq(t, messaging.Sent, listed()[0].Status)

			var events int
			must.NoError(t, client.Reader().QueryRowContext(ctx,
				"SELECT count(*) FROM metering_events WHERE meter = $1", messaging.MeterEmails).Scan(&events))
			test.Eq(t, 1, events)
		})

		t.Run("the hour before follows, and is skipped when the lesson is cancelled", func(t *testing.T) {
			now.set(sofia.Add(-time.Hour))
			test.Eq(t, int64(1), plan())

			hourBefore := listed()[0]
			test.Eq(t, time.Hour, hourBefore.Offset)

			must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
				_, cancelErr := scheduleStore.CancelOccurrence(ctx, tx, scope, hourBefore.OccurrenceID, schedule.CancelledByHousehold, true, "sick")

				return cancelErr
			}))

			must.NoError(t, r.Deliver(ctx, hourBefore.ID))
			test.Eq(t, 1, box.count(), test.Sprint("nothing more was sent"))

			skipped := listed()[0]
			test.Eq(t, messaging.Skipped, skipped.Status)
			test.Eq(t, "The lesson was cancelled.", skipped.Reason)
		})

		t.Run("a contact with reminders off gets none", func(t *testing.T) {
			households, listErr := rosterStore.ListHouseholds(ctx, client.Reader(), scope, false)
			must.NoError(t, listErr)

			var anaContact string

			for _, h := range households {
				for _, c := range h.Contacts {
					if c.Name == "Ana Ruiz" {
						anaContact = c.ID
					}
				}
			}

			must.NotEq(t, "", anaContact)

			setOff := func(off bool) {
				must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
					_, setErr := rosterStore.SetContactReminders(ctx, tx, scope, anaContact, off)

					return setErr
				}))
			}

			now.set(ana.Add(-23 * time.Hour))
			setOff(true)
			test.Eq(t, int64(0), plan())

			setOff(false)
			test.Eq(t, int64(1), plan())
		})

		t.Run("a provider that keeps refusing is given up on", func(t *testing.T) {
			pending := listed()[0]
			must.Eq(t, "ana@example.com", pending.ToAddress)
			test.StrContains(t, pending.Subject, "Reminder: Voice 60, tomorrow at 6:00 PM", test.Sprint("Ana reads English, about her own lesson"))

			box.refuse = errors.New("provider down")

			for attempt := 1; attempt < messaging.MaxAttempts; attempt++ {
				test.Error(t, r.Deliver(ctx, pending.ID), test.Sprintf("attempt %d goes back to the timer", attempt))
			}

			test.Eq(t, messaging.Queued, listed()[0].Status)

			must.NoError(t, r.Deliver(ctx, pending.ID), must.Sprint("the last attempt retires it"))

			failed := listed()[0]
			test.Eq(t, messaging.Failed, failed.Status)
			test.StrContains(t, failed.Reason, "provider down")
			test.Eq(t, messaging.MaxAttempts, failed.Attempts)
		})

		t.Run("a household narrows the list", func(t *testing.T) {
			garcia, listErr := store.ListReminders(ctx, client.Reader(), scope, dayBefore.HouseholdID, 0)
			must.NoError(t, listErr)
			test.SliceLen(t, 2, garcia)

			for _, one := range garcia {
				test.Eq(t, "García", one.HouseholdName)
			}
		})
	})
}

// writeLessons imports two households through the Acuity importer and puts
// their lessons on the grid: Lucía García, a parent who reads Spanish, for
// Sofía's piano; and Ana Ruiz, an adult student, who reads English.
func writeLessons(
	t *testing.T,
	ctx context.Context,
	client database.Client,
	rosterStore roster.Store,
	scheduleStore schedule.Store,
	scope tenancy.Scope,
	staff string,
	central *time.Location,
	realNow, sofia, ana time.Time,
) {
	t.Helper()

	grid, err := schedule.NewMaterializer(client, scheduleStore,
		schedule.ZoneFunc(func(context.Context, string) (*time.Location, error) { return central, nil }),
		schedule.WithClock(func() time.Time { return realNow }),
	)
	must.NoError(t, err)

	writer, err := importing.NewWriter(rosterStore, scheduleStore, grid)
	must.NoError(t, err)

	at := func(when time.Time, minutes int) string {
		return fmt.Sprintf("%q,%q", when.Format("January 2, 2006 3:04pm"), when.Add(time.Duration(minutes)*time.Minute).Format("January 2, 2006 3:04pm"))
	}

	rows := []string{"Start Time,End Time,First Name,Last Name,Email,Type,Appointment Price"}
	for week := range 2 {
		rows = append(rows,
			at(sofia.AddDate(0, 0, 7*week), 30)+",Sofía,García,lucia@example.com,Piano 30,45.00",
			at(ana.AddDate(0, 0, 7*week), 60)+",Ana,Ruiz,ana@example.com,Voice 60,70.00",
		)
	}

	plan, err := acuity.Plan(acuity.Input{
		Clients:      "Client Name,Client Email Address\nLucía García,lucia@example.com\nAna Ruiz,ana@example.com\n",
		Appointments: strings.Join(rows, "\n") + "\n",
		Today:        schedule.Today(realNow, central),
		Language:     roster.English,
	})
	must.NoError(t, err)

	// Lucía reads Spanish; the import starts everybody in one language.
	must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
		result, writeErr := writer.Write(ctx, tx, scope, plan, importing.Choices{Staff: []string{staff}})
		if writeErr != nil {
			return writeErr
		}

		must.Eq(t, 2, result.LessonsWritten)

		households, listErr := rosterStore.ListHouseholds(ctx, tx, scope, false)
		if listErr != nil {
			return listErr
		}

		for _, h := range households {
			for _, c := range h.Contacts {
				if c.Name != "Lucía García" {
					continue
				}

				c.Language = roster.Spanish
				if _, updateErr := rosterStore.UpdateContact(ctx, tx, scope, c); updateErr != nil {
					return updateErr
				}
			}
		}

		return nil
	}))
}
