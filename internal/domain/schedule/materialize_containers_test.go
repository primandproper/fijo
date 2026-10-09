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

func TestMaterializerAgainstPostgres(t *testing.T) {
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

		store, err := schedule.NewSQLStore()
		must.NoError(t, err)

		central, err := time.LoadLocation("US/Central")
		must.NoError(t, err)

		// The first of March 2026 is a Sunday, so a four-week horizon reaches
		// the twenty-ninth and catches four Tuesdays.
		today := time.Date(2026, time.March, 1, 9, 0, 0, 0, time.UTC)

		materializer, err := schedule.NewMaterializer(client, store,
			schedule.ZoneFunc(func(context.Context, string) (*time.Location, error) { return central, nil }),
			schedule.WithClock(func() time.Time { return today }),
		)
		must.NoError(t, err)

		var offering *schedule.Offering

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
				HorizonWeeks: 4,
			}); createErr != nil {
				return createErr
			}

			_, createErr = store.CreateSeries(ctx, tx, scope, &schedule.Series{
				OfferingID:    offering.ID,
				Weekday:       time.Tuesday,
				StartTime:     schedule.ClockTime{Hour: 16},
				IntervalWeeks: 1,
				StartsOn:      schedule.Date{Year: 2026, Month: time.March, Day: 1},
				Staff:         []string{studio.User.ID},
				Attendees:     []string{sofia},
			})

			return createErr
		}))

		t.Run("writes the horizon and is free to run again", func(t *testing.T) {
			written, runErr := materializer.RunOnce(ctx)
			must.NoError(t, runErr)
			test.Eq(t, int64(4), written)

			again, runErr := materializer.RunOnce(ctx)
			must.NoError(t, runErr)
			test.Eq(t, int64(0), again, test.Sprint("a horizon already written writes nothing"))
		})

		t.Run("every occurrence is at four o'clock in the studio's zone", func(t *testing.T) {
			from := time.Date(2026, time.March, 1, 0, 0, 0, 0, central)

			occurrences, listErr := store.ListOccurrencesInWindow(ctx, client.Reader(), scope, from, from.AddDate(0, 0, 28))
			must.NoError(t, listErr)
			must.Len(t, 4, occurrences)

			for _, occurrence := range occurrences {
				local := occurrence.StartsAt.In(central)
				test.Eq(t, 16, local.Hour(), test.Sprintf("%s is not at four", local))
				test.Eq(t, time.Tuesday, local.Weekday())
				test.Eq(t, 30*time.Minute, occurrence.EndsAt.Sub(occurrence.StartsAt))
				test.Eq(t, []string{studio.User.ID}, occurrence.Staff)
			}

			// The clocks go forward on the eighth, so the first Tuesday is six
			// hours behind UTC and the rest are five.
			_, firstOffset := occurrences[0].StartsAt.In(central).Zone()
			_, lastOffset := occurrences[3].StartsAt.In(central).Zone()
			test.Eq(t, -6*60*60, firstOffset)
			test.Eq(t, -5*60*60, lastOffset)
		})

		t.Run("a closure keeps the materializer off the days it covers", func(t *testing.T) {
			// A second studio, so the closure is tested against a horizon that
			// has not already been written.
			closed := seed.Studio(t, ctx, seeder, &bootstrap.Studio{
				Username: "closed", EmailAddress: "closed@example.com",
				Password: "a good long password", Name: "Closed Studio", TimeZone: "US/Central",
			})
			closedScope := tenancy.Of(closed.Account.ID)
			theirs := seed.Attendee(t, ctx, client, closedScope, "Somebody Else")

			must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
				their, createErr := store.CreateOffering(ctx, tx, closedScope, &schedule.Offering{
					NameEN: "Lesson", NameES: "Clase",
					Kind: schedule.KindSingle, Duration: 45 * time.Minute, ListPrice: 5000,
				})
				if createErr != nil {
					return createErr
				}

				if _, createErr = store.SetSettings(ctx, tx, closedScope, &schedule.Settings{
					NoticeWindow: 24 * time.Hour, HorizonWeeks: 4,
				}); createErr != nil {
					return createErr
				}

				if _, createErr = store.CreateClosure(ctx, tx, closedScope, &schedule.Closure{
					StartsOn: schedule.Date{Year: 2026, Month: time.March, Day: 16},
					EndsOn:   schedule.Date{Year: 2026, Month: time.March, Day: 22},
					Reason:   "spring break",
				}); createErr != nil {
					return createErr
				}

				_, createErr = store.CreateSeries(ctx, tx, closedScope, &schedule.Series{
					OfferingID:    their.ID,
					Weekday:       time.Tuesday,
					StartTime:     schedule.ClockTime{Hour: 10},
					IntervalWeeks: 1,
					StartsOn:      schedule.Date{Year: 2026, Month: time.March, Day: 1},
					Attendees:     []string{theirs},
				})

				return createErr
			}))

			_, runErr := materializer.RunOnce(ctx)
			must.NoError(t, runErr)

			from := time.Date(2026, time.March, 1, 0, 0, 0, 0, central)

			occurrences, listErr := store.ListOccurrencesInWindow(ctx, client.Reader(), closedScope, from, from.AddDate(0, 0, 28))
			must.NoError(t, listErr)
			must.Len(t, 3, occurrences, must.Sprint("the closed Tuesday is not written"))

			for _, occurrence := range occurrences {
				test.NotEq(t, 17, occurrence.StartsAt.In(central).Day())
			}
		})

		t.Run("a studio whose zone cannot be resolved does not stop the others", func(t *testing.T) {
			broken, buildErr := schedule.NewMaterializer(client, store,
				schedule.ZoneFunc(func(_ context.Context, studioID string) (*time.Location, error) {
					if studioID == studio.Account.ID {
						return nil, context.DeadlineExceeded
					}

					return central, nil
				}),
				schedule.WithClock(func() time.Time { return today }),
			)
			must.NoError(t, buildErr)

			// It returns without error: the other studios were written and the
			// broken one was acknowledged against its own name.
			_, runErr := broken.RunOnce(ctx)
			must.NoError(t, runErr)
		})
	})
}
