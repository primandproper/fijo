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

func TestSyncSeriesAgainstPostgres(t *testing.T) {
	t.Parallel()

	pgtest.Run(t, func(ctx context.Context, pg *pgtest.Instance) {
		client := pgclient.Open(t, ctx, pg)
		seeder := seed.New(t, client)

		studio := seed.Studio(t, ctx, seeder, &bootstrap.Studio{
			Username: "sync", EmailAddress: "sync@example.com",
			Password: "a good long password", Name: "Casa de Sync", TimeZone: "US/Central",
		})

		scope := tenancy.Of(studio.Account.ID)
		sofia := seed.Attendee(t, ctx, client, scope, "Sofia")

		store, err := schedule.NewSQLStore()
		must.NoError(t, err)

		central, err := time.LoadLocation("US/Central")
		must.NoError(t, err)

		// A Sunday morning, with a four-week horizon.
		now := time.Date(2026, time.March, 1, 15, 0, 0, 0, time.UTC)

		materializer, err := schedule.NewMaterializer(client, store,
			schedule.ZoneFunc(func(context.Context, string) (*time.Location, error) { return central, nil }),
			schedule.WithClock(func() time.Time { return now }),
		)
		must.NoError(t, err)

		var series *schedule.Series

		must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
			offering, createErr := store.CreateOffering(ctx, tx, scope, &schedule.Offering{
				NameEN: "Lesson", NameES: "Clase",
				Kind: schedule.KindSingle, Duration: 30 * time.Minute, ListPrice: 3500,
			})
			if createErr != nil {
				return createErr
			}

			if _, createErr = store.SetSettings(ctx, tx, scope, &schedule.Settings{NoticeWindow: 24 * time.Hour, HorizonWeeks: 4}); createErr != nil {
				return createErr
			}

			series, createErr = store.CreateSeries(ctx, tx, scope, &schedule.Series{
				OfferingID:    offering.ID,
				Weekday:       time.Tuesday,
				StartTime:     schedule.ClockTime{Hour: 16},
				IntervalWeeks: 1,
				StartsOn:      schedule.Date{Year: 2026, Month: time.March, Day: 1},
				Attendees:     []string{sofia},
			})

			return createErr
		}))

		sync := func(t *testing.T) (written, retracted int64) {
			t.Helper()

			must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
				var syncErr error

				written, retracted, syncErr = materializer.SyncSeries(ctx, tx, scope, series.ID)

				return syncErr
			}))

			return written, retracted
		}

		from := time.Date(2026, time.March, 1, 0, 0, 0, 0, central)

		written, retracted := sync(t)
		test.Eq(t, int64(4), written)
		test.Eq(t, int64(0), retracted)

		// The owner cancels the second Tuesday, then moves the slot to
		// Thursdays at five.
		occurrences, err := store.ListOccurrencesInWindow(ctx, client.Reader(), scope, from, from.AddDate(0, 0, 28))
		must.NoError(t, err)
		must.Len(t, 4, occurrences)

		cancelled := occurrences[1]

		must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
			if _, cancelErr := store.CancelOccurrence(ctx, tx, scope, cancelled.ID, schedule.CancelledByHousehold, false, "sick"); cancelErr != nil {
				return cancelErr
			}

			series.Weekday = time.Thursday
			series.StartTime = schedule.ClockTime{Hour: 17}

			var updateErr error

			series, updateErr = store.UpdateSeries(ctx, tx, scope, series)

			return updateErr
		}))

		written, retracted = sync(t)
		test.Eq(t, int64(4), written)
		test.Eq(t, int64(3), retracted, test.Sprint("the cancelled Tuesday is history and stays"))

		occurrences, err = store.ListOccurrencesInWindow(ctx, client.Reader(), scope, from, from.AddDate(0, 0, 28))
		must.NoError(t, err)
		must.Len(t, 5, occurrences)

		var thursdays int

		for _, occurrence := range occurrences {
			local := occurrence.StartsAt.In(central)

			if occurrence.ID == cancelled.ID {
				test.Eq(t, schedule.StateCancelled, occurrence.State)

				continue
			}

			test.Eq(t, time.Thursday, local.Weekday())
			test.Eq(t, 17, local.Hour())

			thursdays++
		}

		test.Eq(t, 4, thursdays)

		written, retracted = sync(t)
		test.Eq(t, int64(0), written, test.Sprint("a synced series is free to sync again"))
		test.Eq(t, int64(0), retracted)

		// Her brother joins the slot: every lesson still standing becomes
		// theirs, under the same identifiers, and the cancelled one does not.
		mateo := seed.Attendee(t, ctx, client, scope, "Mateo")

		must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
			series.Attendees = []string{sofia, mateo}

			var updateErr error

			series, updateErr = store.UpdateSeries(ctx, tx, scope, series)

			return updateErr
		}))

		written, retracted = sync(t)
		test.Eq(t, int64(0), written)
		test.Eq(t, int64(0), retracted, test.Sprint("a change of who is not a change of when"))

		after, err := store.ListOccurrencesInWindow(ctx, client.Reader(), scope, from, from.AddDate(0, 0, 28))
		must.NoError(t, err)
		must.Len(t, 5, after)

		for _, occurrence := range after {
			if occurrence.ID == cancelled.ID {
				test.Eq(t, []string{sofia}, occurrence.Attendees)

				continue
			}

			test.SliceContainsAll(t, []string{sofia, mateo}, occurrence.Attendees)
		}
	})
}
