package schedule_test

import (
	"context"
	"testing"
	"time"

	"github.com/primandproper/fijo/internal/bootstrap"
	"github.com/primandproper/fijo/internal/domain/schedule"
	"github.com/primandproper/fijo/internal/testutils/pgclient"
	"github.com/primandproper/fijo/internal/testutils/seed"

	"github.com/primandproper/platform-go/v15/audit"
	"github.com/primandproper/primitives-go/v2/database"
	"github.com/primandproper/primitives-go/v2/database/dialect"
	"github.com/primandproper/primitives-go/v2/tenancy"
	"github.com/primandproper/primitives-go/v2/testutils/containers/pgtest"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestCommandsAgainstPostgres(t *testing.T) {
	t.Parallel()

	pgtest.Run(t, func(ctx context.Context, pg *pgtest.Instance) {
		client := pgclient.Open(t, ctx, pg)
		seeder := seed.New(t, client)

		studio := seed.Studio(t, ctx, seeder, &bootstrap.Studio{
			Username: "maria", EmailAddress: "maria@example.com",
			Password: "a good long password", Name: "Casa de Musica", TimeZone: "US/Central",
		})

		scope := tenancy.Of(studio.Account.ID)
		sofia := seed.Attendee(t, ctx, client, scope, "Sofia")
		actor := studio.User.ID

		store, err := schedule.NewSQLStore()
		must.NoError(t, err)

		recorder, err := audit.NewRecorder(dialect.Postgres)
		must.NoError(t, err)

		central, err := time.LoadLocation("US/Central")
		must.NoError(t, err)

		// Tuesday the tenth of March 2026, nine in the morning. The lesson
		// under test is that afternoon at four.
		now := time.Date(2026, time.March, 10, 9, 0, 0, 0, central)

		commands, err := schedule.NewCommands(client, store, recorder,
			schedule.WithCommandsClock(func() time.Time { return now }))
		must.NoError(t, err)

		materializer, err := schedule.NewMaterializer(client, store,
			schedule.ZoneFunc(func(context.Context, string) (*time.Location, error) { return central, nil }),
			schedule.WithClock(func() time.Time { return now }),
		)
		must.NoError(t, err)

		var (
			offering *schedule.Offering
			series   *schedule.Series
		)

		must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
			var createErr error

			offering, createErr = store.CreateOffering(ctx, tx, scope, &schedule.Offering{
				NameEN: "Half-hour piano lesson", NameES: "Clase de piano de media hora",
				Kind: schedule.KindSingle, Duration: 30 * time.Minute, ListPrice: 3500,
			})
			if createErr != nil {
				return createErr
			}

			if _, createErr = store.SetSettings(ctx, tx, scope, &schedule.Settings{
				NoticeWindow: 24 * time.Hour,
				HorizonWeeks: 6,
			}); createErr != nil {
				return createErr
			}

			series, createErr = store.CreateSeries(ctx, tx, scope, &schedule.Series{
				OfferingID:    offering.ID,
				Weekday:       time.Tuesday,
				StartTime:     schedule.ClockTime{Hour: 16},
				IntervalWeeks: 1,
				StartsOn:      schedule.Date{Year: 2026, Month: time.March, Day: 10},
				Staff:         []string{actor},
				Attendees:     []string{sofia},
			})

			return createErr
		}))

		_, err = materializer.RunOnce(ctx)
		must.NoError(t, err)

		occurrencesOn := func(t *testing.T, month time.Month, day int) []*schedule.Occurrence {
			t.Helper()

			from := time.Date(2026, month, day, 0, 0, 0, 0, central)

			found, listErr := store.ListOccurrencesInWindow(ctx, client.Reader(), scope, from, from.AddDate(0, 0, 1))
			must.NoError(t, listErr)

			return found
		}

		today := occurrencesOn(t, time.March, 10)
		must.Len(t, 1, today)
		lesson := today[0]

		t.Run("cancelling seven hours out is inside a day's notice", func(t *testing.T) {
			cancelled, cancelErr := commands.Cancel(ctx, scope, actor, lesson.ID, schedule.CancelledByHousehold, "sick")
			must.NoError(t, cancelErr)

			test.Eq(t, schedule.StateCancelled, cancelled.State)
			test.Eq(t, schedule.CancelledByHousehold, cancelled.CancelledBy)
			must.NotNil(t, cancelled.CancelledInsideNotice)
			test.True(t, *cancelled.CancelledInsideNotice, test.Sprint("four this afternoon is inside a day's notice from nine this morning"))
		})

		t.Run("the same cancellation on a later lesson is outside it", func(t *testing.T) {
			nextWeek := occurrencesOn(t, time.March, 17)
			must.Len(t, 1, nextWeek)

			cancelled, cancelErr := commands.Cancel(ctx, scope, actor, nextWeek[0].ID, schedule.CancelledByHousehold, "holiday")
			must.NoError(t, cancelErr)

			must.NotNil(t, cancelled.CancelledInsideNotice)
			test.False(t, *cancelled.CancelledInsideNotice)
		})

		t.Run("a make-up answers a cancellation and joins no series", func(t *testing.T) {
			startsAt := time.Date(2026, time.March, 14, 10, 0, 0, 0, central)

			makeUp, makeUpErr := commands.ScheduleMakeUp(ctx, scope, actor, lesson.ID, startsAt, "Saturday morning instead")
			must.NoError(t, makeUpErr)

			test.Eq(t, lesson.ID, makeUp.Replaces)
			test.Eq(t, "", makeUp.SeriesID)
			test.Eq(t, 30*time.Minute, makeUp.EndsAt.Sub(makeUp.StartsAt))
			test.Eq(t, []string{actor}, makeUp.Staff)

			// A make-up against something that was not cancelled is refused.
			standing := occurrencesOn(t, time.March, 24)
			must.Len(t, 1, standing)

			_, makeUpErr = commands.ScheduleMakeUp(ctx, scope, actor, standing[0].ID, startsAt, "")
			test.ErrorIs(t, makeUpErr, schedule.ErrStateTransitionRefused)
		})

		t.Run("moving writes a new row and leaves the old one naming it", func(t *testing.T) {
			standing := occurrencesOn(t, time.March, 24)
			must.Len(t, 1, standing)

			original := standing[0]
			startsAt := time.Date(2026, time.March, 25, 16, 0, 0, 0, central)

			moved, moveErr := commands.Move(ctx, scope, actor, original.ID, startsAt, "Wednesday this week")
			must.NoError(t, moveErr)

			test.True(t, moved.StartsAt.Equal(startsAt))
			test.Eq(t, 30*time.Minute, moved.EndsAt.Sub(moved.StartsAt))
			test.Eq(t, []string{sofia}, moved.Attendees, test.Sprint("a moved lesson is still Sofia's"))

			left, getErr := store.GetOccurrence(ctx, client.Reader(), scope, original.ID)
			must.NoError(t, getErr)
			test.Eq(t, schedule.StateMoved, left.State)
			test.Eq(t, moved.ID, left.MovedTo)

			// Moving it again is refused: it is no longer standing.
			_, moveErr = commands.Move(ctx, scope, actor, original.ID, startsAt, "")
			test.ErrorIs(t, moveErr, schedule.ErrStateTransitionRefused)
		})

		t.Run("completing and reverting", func(t *testing.T) {
			standing := occurrencesOn(t, time.March, 31)
			must.Len(t, 1, standing)

			completed, completeErr := commands.Complete(ctx, scope, actor, standing[0].ID, "she played well")
			must.NoError(t, completeErr)
			test.Eq(t, schedule.StateCompleted, completed.State)

			reverted, revertErr := commands.Revert(ctx, scope, actor, standing[0].ID, "wrong lesson")
			must.NoError(t, revertErr)
			test.Eq(t, schedule.StateScheduled, reverted.State)

			noShow, noShowErr := commands.NoShow(ctx, scope, actor, standing[0].ID, "")
			must.NoError(t, noShowErr)
			test.Eq(t, schedule.StateNoShow, noShow.State)
		})

		t.Run("closing the studio cancels what is standing inside it", func(t *testing.T) {
			closure, closeErr := commands.CloseStudio(ctx, scope, actor,
				schedule.Date{Year: 2026, Month: time.April, Day: 6},
				schedule.Date{Year: 2026, Month: time.April, Day: 12},
				"spring break")
			must.NoError(t, closeErr)
			must.NotNil(t, closure)

			// The Tuesday inside the range is cancelled, and by the closure.
			closed := occurrencesOn(t, time.April, 7)
			must.Len(t, 1, closed)
			test.Eq(t, schedule.StateCancelled, closed[0].State)
			test.Eq(t, schedule.CancelledByClosure, closed[0].CancelledBy)

			// The materializer does not put it back.
			_, runErr := materializer.RunOnce(ctx)
			must.NoError(t, runErr)

			closed = occurrencesOn(t, time.April, 7)
			test.Len(t, 1, closed)
		})

		t.Run("ending a series cancels what stands after its last day", func(t *testing.T) {
			endsOn := schedule.Date{Year: 2026, Month: time.April, Day: 14}

			ended, endErr := commands.EndSeries(ctx, scope, actor, series.ID, endsOn, "she is stopping")
			must.NoError(t, endErr)
			test.Eq(t, endsOn, ended.EndsOn)

			// The fourteenth is the last day, so that Tuesday stands.
			lastDay := occurrencesOn(t, time.April, 14)
			must.Len(t, 1, lastDay)
			test.Eq(t, schedule.StateScheduled, lastDay[0].State)

			// The following Tuesday is cancelled, by the studio.
			after := occurrencesOn(t, time.April, 21)
			must.Len(t, 1, after)
			test.Eq(t, schedule.StateCancelled, after[0].State)
			test.Eq(t, schedule.CancelledByStudio, after[0].CancelledBy)

			// And the materializer does not write past the end date.
			_, runErr := materializer.RunOnce(ctx)
			must.NoError(t, runErr)

			from := time.Date(2026, time.April, 21, 0, 0, 0, 0, central)

			later, listErr := store.ListOccurrencesInWindow(ctx, client.Reader(), scope, from.AddDate(0, 0, 7), from.AddDate(0, 0, 28))
			must.NoError(t, listErr)
			test.SliceEmpty(t, later)
		})

		t.Run("every command left an entry in the studio's scope", func(t *testing.T) {
			reader, readerErr := audit.NewReader(dialect.Postgres)
			must.NoError(t, readerErr)

			entries, listErr := reader.List(ctx, client.Reader(), scope, &audit.Query{
				ResourceType: schedule.ResourceOccurrence,
				ResourceID:   lesson.ID,
			}, nil)
			must.NoError(t, listErr)
			must.True(t, len(entries.Data) > 0, must.Sprint("the cancellation was not recorded"))

			found := false

			for _, entry := range entries.Data {
				if entry.Metadata[schedule.KeyCommand] == schedule.CommandCancel {
					found = true

					test.Eq(t, actor, entry.Actor.ID)
					test.Eq(t, "true", entry.Metadata[schedule.KeyInsideNotice])
					test.Eq(t, "sick", entry.Metadata[schedule.KeyReason])
				}
			}

			test.True(t, found, test.Sprint("no entry named the cancel command"))
		})
	})
}
