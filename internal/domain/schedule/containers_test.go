package schedule_test

import (
	"context"
	"testing"
	"time"

	"github.com/primandproper/fijo/internal/bootstrap"
	"github.com/primandproper/fijo/internal/domain/schedule"
	"github.com/primandproper/fijo/internal/testutils/pgclient"
	"github.com/primandproper/fijo/internal/testutils/seed"

	"github.com/primandproper/primitives-go/v2/database"
	"github.com/primandproper/primitives-go/v2/tenancy"
	"github.com/primandproper/primitives-go/v2/testutils/containers/pgtest"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestSQLStoreAgainstPostgres(t *testing.T) {
	t.Parallel()

	pgtest.Run(t, func(ctx context.Context, pg *pgtest.Instance) {
		client := pgclient.Open(t, ctx, pg)
		seeder := seed.New(t, client)

		casa := seed.Studio(t, ctx, seeder, &bootstrap.Studio{
			Username: "maria", EmailAddress: "maria@example.com",
			Password: "a good long password", Name: "Casa de Musica", TimeZone: "US/Central",
		})
		other := seed.Studio(t, ctx, seeder, &bootstrap.Studio{
			Username: "other", EmailAddress: "other@example.com",
			Password: "a good long password", Name: "Another Studio",
		})

		scope := tenancy.Of(casa.Account.ID)
		otherScope := tenancy.Of(other.Account.ID)
		teacher := casa.User.ID

		store, err := schedule.NewSQLStore()
		must.NoError(t, err)

		central, err := time.LoadLocation("US/Central")
		must.NoError(t, err)

		write := func(t *testing.T, fn func(tx database.Tx) error) {
			t.Helper()
			must.NoError(t, client.WithTransaction(ctx, fn))
		}

		t.Run("the global scope is refused", func(t *testing.T) {
			_, refusal := store.ListOfferings(ctx, client.Reader(), tenancy.Global(), false)
			test.ErrorIs(t, refusal, schedule.ErrNotStudioScoped)
		})

		t.Run("a studio that has set nothing has the defaults", func(t *testing.T) {
			settings, readErr := store.GetSettings(ctx, client.Reader(), scope)
			must.NoError(t, readErr)
			test.Eq(t, 24*time.Hour, settings.NoticeWindow)
			test.Eq(t, 8, settings.HorizonWeeks)

			write(t, func(tx database.Tx) error {
				written, setErr := store.SetSettings(ctx, tx, scope, &schedule.Settings{
					NoticeWindow: 48 * time.Hour,
					HorizonWeeks: 12,
				})
				if setErr != nil {
					return setErr
				}

				test.Eq(t, 48*time.Hour, written.NoticeWindow)
				test.Eq(t, 12, written.HorizonWeeks)

				return nil
			})

			settings, readErr = store.GetSettings(ctx, client.Reader(), scope)
			must.NoError(t, readErr)
			test.Eq(t, 48*time.Hour, settings.NoticeWindow)
		})

		var offering *schedule.Offering

		t.Run("offerings", func(t *testing.T) {
			write(t, func(tx database.Tx) error {
				var createErr error

				offering, createErr = store.CreateOffering(ctx, tx, scope, &schedule.Offering{
					NameEN:    "Half-hour piano lesson",
					NameES:    "Clase de piano de media hora",
					Kind:      schedule.KindSingle,
					Duration:  30 * time.Minute,
					ListPrice: 3500,
				})

				return createErr
			})

			must.NotNil(t, offering)
			test.NotEq(t, "", offering.ID)
			test.Eq(t, casa.Account.ID, offering.StudioID)
			test.Eq(t, 30*time.Minute, offering.Duration)

			read, getErr := store.GetOffering(ctx, client.Reader(), scope, offering.ID)
			must.NoError(t, getErr)
			test.Eq(t, offering.ID, read.ID)

			// Another studio's offering is not found rather than forbidden.
			_, getErr = store.GetOffering(ctx, client.Reader(), otherScope, offering.ID)
			test.ErrorIs(t, getErr, schedule.ErrNotFound)

			listed, listErr := store.ListOfferings(ctx, client.Reader(), scope, false)
			must.NoError(t, listErr)
			test.Len(t, 1, listed)

			listed, listErr = store.ListOfferings(ctx, client.Reader(), otherScope, false)
			must.NoError(t, listErr)
			test.SliceEmpty(t, listed)
		})

		t.Run("a package needs a lesson count and nothing else may have one", func(t *testing.T) {
			write(t, func(tx database.Tx) error {
				_, createErr := store.CreateOffering(ctx, tx, scope, &schedule.Offering{
					NameEN: "Five pack", NameES: "Paquete de cinco",
					Kind: schedule.KindPackage, Duration: 30 * time.Minute, ListPrice: 25000,
				})
				test.ErrorIs(t, createErr, schedule.ErrInvalidOffering)

				_, createErr = store.CreateOffering(ctx, tx, scope, &schedule.Offering{
					NameEN: "One lesson", NameES: "Una clase",
					Kind: schedule.KindSingle, Duration: 30 * time.Minute, ListPrice: 3500,
					LessonCount: 5,
				})
				test.ErrorIs(t, createErr, schedule.ErrInvalidOffering)

				return nil
			})
		})

		var series *schedule.Series

		t.Run("series", func(t *testing.T) {
			write(t, func(tx database.Tx) error {
				var createErr error

				series, createErr = store.CreateSeries(ctx, tx, scope, &schedule.Series{
					OfferingID:    offering.ID,
					Weekday:       time.Tuesday,
					StartTime:     schedule.ClockTime{Hour: 16},
					IntervalWeeks: 1,
					StartsOn:      schedule.Date{Year: 2026, Month: time.March, Day: 3},
					Staff:         []string{teacher, teacher},
				})

				return createErr
			})

			must.NotNil(t, series)
			test.Eq(t, []string{teacher}, series.Staff)

			read, getErr := store.GetSeries(ctx, client.Reader(), scope, series.ID)
			must.NoError(t, getErr)
			test.Eq(t, time.Tuesday, read.Weekday)
			test.Eq(t, schedule.ClockTime{Hour: 16}, read.StartTime)
			test.Eq(t, schedule.Date{Year: 2026, Month: time.March, Day: 3}, read.StartsOn)
			test.True(t, read.EndsOn.IsZero())
			test.Eq(t, []string{teacher}, read.Staff)

			live, liveErr := store.ListLiveSeries(ctx, client.Reader(),
				schedule.Date{Year: 2026, Month: time.March, Day: 1},
				schedule.Date{Year: 2026, Month: time.April, Day: 30})
			must.NoError(t, liveErr)
			test.Len(t, 1, live)
			test.Eq(t, []string{teacher}, live[0].Staff)

			// A window entirely before the series begins finds nothing.
			live, liveErr = store.ListLiveSeries(ctx, client.Reader(),
				schedule.Date{Year: 2026, Month: time.January, Day: 1},
				schedule.Date{Year: 2026, Month: time.January, Day: 31})
			must.NoError(t, liveErr)
			test.SliceEmpty(t, live)
		})

		t.Run("a fortnightly interval is the only other one", func(t *testing.T) {
			write(t, func(tx database.Tx) error {
				_, createErr := store.CreateSeries(ctx, tx, scope, &schedule.Series{
					OfferingID: offering.ID, Weekday: time.Monday,
					StartTime: schedule.ClockTime{Hour: 9}, IntervalWeeks: 3,
					StartsOn: schedule.Date{Year: 2026, Month: time.March, Day: 2},
				})
				test.ErrorIs(t, createErr, schedule.ErrInvalidSeries)

				return nil
			})
		})

		var occurrence *schedule.Occurrence

		t.Run("materializing is idempotent", func(t *testing.T) {
			must.NotNil(t, series)

			startsAt := schedule.Date{Year: 2026, Month: time.March, Day: 3}.At(schedule.ClockTime{Hour: 16}, central)

			write(t, func(tx database.Tx) error {
				var (
					wrote       bool
					materialize error
				)

				occurrence, wrote, materialize = store.MaterializeOccurrence(ctx, tx, scope, &schedule.Occurrence{
					SeriesID:   series.ID,
					OfferingID: offering.ID,
					StartsAt:   startsAt,
					EndsAt:     startsAt.Add(30 * time.Minute),
					Staff:      []string{teacher},
				})
				if materialize != nil {
					return materialize
				}

				test.True(t, wrote)

				// The same instant again writes nothing and says so.
				second, wroteAgain, materializeAgain := store.MaterializeOccurrence(ctx, tx, scope, &schedule.Occurrence{
					SeriesID:   series.ID,
					OfferingID: offering.ID,
					StartsAt:   startsAt,
					EndsAt:     startsAt.Add(30 * time.Minute),
				})
				if materializeAgain != nil {
					return materializeAgain
				}

				test.False(t, wroteAgain)
				test.Nil(t, second)

				return nil
			})

			must.NotNil(t, occurrence)
			test.Eq(t, schedule.StateScheduled, occurrence.State)
			test.True(t, occurrence.StartsAt.Equal(startsAt))
		})

		t.Run("the week view reads what is in the window", func(t *testing.T) {
			must.NotNil(t, occurrence)

			from := schedule.Date{Year: 2026, Month: time.March, Day: 2}.At(schedule.ClockTime{}, central)

			found, listErr := store.ListOccurrencesInWindow(ctx, client.Reader(), scope, from, from.AddDate(0, 0, 7))
			must.NoError(t, listErr)
			must.Len(t, 1, found)
			test.Eq(t, occurrence.ID, found[0].ID)
			test.Eq(t, []string{teacher}, found[0].Staff)

			found, listErr = store.ListOccurrencesInWindow(ctx, client.Reader(), otherScope, from, from.AddDate(0, 0, 7))
			must.NoError(t, listErr)
			test.SliceEmpty(t, found)
		})

		t.Run("completing, reverting and refusing", func(t *testing.T) {
			write(t, func(tx database.Tx) error {
				completed, stateErr := store.SetOccurrenceState(ctx, tx, scope, occurrence.ID, schedule.StateCompleted, "she came")
				if stateErr != nil {
					return stateErr
				}

				test.Eq(t, schedule.StateCompleted, completed.State)
				test.Eq(t, "she came", completed.Reason)

				reverted, revertErr := store.SetOccurrenceState(ctx, tx, scope, occurrence.ID, schedule.StateScheduled, "")
				if revertErr != nil {
					return revertErr
				}

				test.Eq(t, schedule.StateScheduled, reverted.State)

				// Cancelling and moving have their own doors.
				_, stateErr = store.SetOccurrenceState(ctx, tx, scope, occurrence.ID, schedule.StateCancelled, "")
				test.ErrorIs(t, stateErr, schedule.ErrInvalidOccurrence)

				// A row in another studio is not found.
				_, stateErr = store.SetOccurrenceState(ctx, tx, otherScope, occurrence.ID, schedule.StateCompleted, "")
				test.ErrorIs(t, stateErr, schedule.ErrNotFound)

				return nil
			})
		})

		t.Run("cancelling keeps whose decision it was and whether it was late", func(t *testing.T) {
			write(t, func(tx database.Tx) error {
				cancelled, cancelErr := store.CancelOccurrence(ctx, tx, scope, occurrence.ID, schedule.CancelledByHousehold, true, "sick")
				if cancelErr != nil {
					return cancelErr
				}

				test.Eq(t, schedule.StateCancelled, cancelled.State)
				test.Eq(t, schedule.CancelledByHousehold, cancelled.CancelledBy)
				must.NotNil(t, cancelled.CancelledInsideNotice)
				test.True(t, *cancelled.CancelledInsideNotice)

				// Cancelling twice is refused by the state machine, not by the
				// row being missing.
				_, cancelErr = store.CancelOccurrence(ctx, tx, scope, occurrence.ID, schedule.CancelledByStudio, false, "")
				test.ErrorIs(t, cancelErr, schedule.ErrStateTransitionRefused)

				return nil
			})
		})

		t.Run("a make-up points back at what it replaces", func(t *testing.T) {
			startsAt := schedule.Date{Year: 2026, Month: time.March, Day: 7}.At(schedule.ClockTime{Hour: 10}, central)

			var makeUp *schedule.Occurrence

			write(t, func(tx database.Tx) error {
				var createErr error

				makeUp, createErr = store.CreateOccurrence(ctx, tx, scope, &schedule.Occurrence{
					OfferingID: offering.ID,
					StartsAt:   startsAt,
					EndsAt:     startsAt.Add(30 * time.Minute),
					Reason:     "make-up for the week she was sick",
					Replaces:   occurrence.ID,
					Staff:      []string{teacher},
				})

				return createErr
			})

			must.NotNil(t, makeUp)
			test.Eq(t, occurrence.ID, makeUp.Replaces)
			test.Eq(t, "", makeUp.SeriesID)

			// Reverting the cancellation clears who cancelled it.
			write(t, func(tx database.Tx) error {
				reverted, revertErr := store.SetOccurrenceState(ctx, tx, scope, occurrence.ID, schedule.StateScheduled, "")
				if revertErr != nil {
					return revertErr
				}

				test.Eq(t, schedule.StateScheduled, reverted.State)
				test.Eq(t, schedule.Canceller(""), reverted.CancelledBy)
				test.Nil(t, reverted.CancelledInsideNotice)

				return nil
			})
		})

		t.Run("moving leaves a row that names what it became", func(t *testing.T) {
			startsAt := schedule.Date{Year: 2026, Month: time.March, Day: 4}.At(schedule.ClockTime{Hour: 16}, central)

			write(t, func(tx database.Tx) error {
				moved, createErr := store.CreateOccurrence(ctx, tx, scope, &schedule.Occurrence{
					OfferingID: offering.ID,
					StartsAt:   startsAt,
					EndsAt:     startsAt.Add(30 * time.Minute),
					Staff:      []string{teacher},
				})
				if createErr != nil {
					return createErr
				}

				left, moveErr := store.MarkOccurrenceMoved(ctx, tx, scope, occurrence.ID, moved.ID, "moved to Wednesday")
				if moveErr != nil {
					return moveErr
				}

				test.Eq(t, schedule.StateMoved, left.State)
				test.Eq(t, moved.ID, left.MovedTo)

				// A moved occurrence is past the reach of the state setter.
				_, stateErr := store.SetOccurrenceState(ctx, tx, scope, occurrence.ID, schedule.StateCompleted, "")
				test.ErrorIs(t, stateErr, schedule.ErrStateTransitionRefused)

				return nil
			})
		})

		t.Run("staff is reassignable per occurrence", func(t *testing.T) {
			write(t, func(tx database.Tx) error {
				reassigned, staffErr := store.SetOccurrenceStaff(ctx, tx, scope, occurrence.ID, nil)
				if staffErr != nil {
					return staffErr
				}

				test.SliceEmpty(t, reassigned.Staff)

				reassigned, staffErr = store.SetOccurrenceStaff(ctx, tx, scope, occurrence.ID, []string{teacher})
				if staffErr != nil {
					return staffErr
				}

				test.Eq(t, []string{teacher}, reassigned.Staff)

				// Another studio cannot reassign it.
				_, staffErr = store.SetOccurrenceStaff(ctx, tx, otherScope, occurrence.ID, []string{teacher})
				test.ErrorIs(t, staffErr, schedule.ErrNotFound)

				return nil
			})
		})

		t.Run("closures", func(t *testing.T) {
			var closure *schedule.Closure

			write(t, func(tx database.Tx) error {
				var createErr error

				closure, createErr = store.CreateClosure(ctx, tx, scope, &schedule.Closure{
					StartsOn: schedule.Date{Year: 2026, Month: time.December, Day: 24},
					EndsOn:   schedule.Date{Year: 2026, Month: time.December, Day: 26},
					Reason:   "Christmas",
				})

				return createErr
			})

			must.NotNil(t, closure)
			test.True(t, closure.Covers(schedule.Date{Year: 2026, Month: time.December, Day: 25}))
			test.False(t, closure.Covers(schedule.Date{Year: 2026, Month: time.December, Day: 27}))

			byStudio, listErr := store.ListClosuresForStudios(ctx, client.Reader(), []string{casa.Account.ID, other.Account.ID})
			must.NoError(t, listErr)
			test.Len(t, 1, byStudio[casa.Account.ID])
			test.SliceEmpty(t, byStudio[other.Account.ID])

			write(t, func(tx database.Tx) error {
				return store.ArchiveClosure(ctx, tx, scope, closure.ID)
			})

			remaining, remainingErr := store.ListClosures(ctx, client.Reader(), scope)
			must.NoError(t, remainingErr)
			test.SliceEmpty(t, remaining)
		})

		t.Run("ending a series leaves what it already produced", func(t *testing.T) {
			endsOn := schedule.Date{Year: 2026, Month: time.March, Day: 10}

			write(t, func(tx database.Tx) error {
				ended, endErr := store.EndSeries(ctx, tx, scope, series.ID, endsOn)
				if endErr != nil {
					return endErr
				}

				test.Eq(t, endsOn, ended.EndsOn)
				test.Eq(t, []string{teacher}, ended.Staff)

				return nil
			})

			produced, listErr := store.ListOccurrencesForSeries(ctx, client.Reader(), scope, series.ID)
			must.NoError(t, listErr)
			test.Len(t, 1, produced)
		})
	})
}
