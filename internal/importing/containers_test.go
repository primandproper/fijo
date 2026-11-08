package importing_test

import (
	"context"
	"testing"
	"time"

	"github.com/primandproper/fijo/internal/bootstrap"
	"github.com/primandproper/fijo/internal/domain/roster"
	"github.com/primandproper/fijo/internal/domain/schedule"
	"github.com/primandproper/fijo/internal/importing"
	"github.com/primandproper/fijo/internal/importing/acuity"
	"github.com/primandproper/fijo/internal/testutils/pgclient"
	"github.com/primandproper/fijo/internal/testutils/seed"

	"github.com/primandproper/primitives-go/v2/database"
	"github.com/primandproper/primitives-go/v2/tenancy"
	"github.com/primandproper/primitives-go/v2/testutils/containers/pgtest"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

const clients = "Client Name,Client Email Address,Client Phone Number,Notes on the Client File\n" +
	"Lucía García,lucia@example.com,(512) 555-0101,Pickup is grandma on Thursdays.\n" +
	"Ana Ruiz,ana@example.com,,\n" +
	"Already Here,here@example.com,,\n"

const appointments = "Start Time,End Time,First Name,Last Name,Email,Type,Appointment Price\n" +
	`"November 10, 2026 4:00pm","November 10, 2026 4:30pm",Sofía,García,lucia@example.com,Piano 30,45.00` + "\n" +
	`"November 17, 2026 4:00pm","November 17, 2026 4:30pm",Sofía,García,lucia@example.com,Piano 30,45.00` + "\n" +
	`"November 12, 2026 6:00pm","November 12, 2026 7:00pm",Ana,Ruiz,ana@example.com,Voice 60,70.00` + "\n" +
	`"November 19, 2026 6:00pm","November 19, 2026 7:00pm",Ana,Ruiz,ana@example.com,Voice 60,70.00` + "\n" +
	`"November 13, 2026 5:00pm","November 13, 2026 5:30pm",Already,Here,here@example.com,Piano 30,45.00` + "\n" +
	`"November 20, 2026 5:00pm","November 20, 2026 5:30pm",Already,Here,here@example.com,Piano 30,45.00` + "\n"

func TestWriterAgainstPostgres(t *testing.T) {
	t.Parallel()

	pgtest.Run(t, func(ctx context.Context, pg *pgtest.Instance) {
		client := pgclient.Open(t, ctx, pg)
		seeder := seed.New(t, client)

		studio := seed.Studio(t, ctx, seeder, &bootstrap.Studio{
			Username: "maria", EmailAddress: "maria@example.com",
			Password: "a good long password", Name: "Casa de Musica", TimeZone: "US/Central",
		})
		scope := tenancy.Of(studio.Account.ID)

		rosterStore, err := roster.NewSQLStore()
		must.NoError(t, err)

		scheduleStore, err := schedule.NewSQLStore()
		must.NoError(t, err)

		central, err := time.LoadLocation("US/Central")
		must.NoError(t, err)

		// Monday the ninth of November.
		now := time.Date(2026, time.November, 9, 9, 0, 0, 0, central)

		grid, err := schedule.NewMaterializer(client, scheduleStore,
			schedule.ZoneFunc(func(context.Context, string) (*time.Location, error) { return central, nil }),
			schedule.WithClock(func() time.Time { return now }),
		)
		must.NoError(t, err)

		writer, err := importing.NewWriter(rosterStore, scheduleStore, grid)
		must.NoError(t, err)

		// The studio already sells "Piano 30", and already has one of the
		// clients, typed in by hand.
		var piano *schedule.Offering

		must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
			var createErr error

			piano, createErr = scheduleStore.CreateOffering(ctx, tx, scope, &schedule.Offering{
				NameEN: "Piano 30", NameES: "Piano 30", Kind: schedule.KindSingle, Duration: 30 * time.Minute, ListPrice: 4000,
			})
			if createErr != nil {
				return createErr
			}

			here, createErr := rosterStore.CreateHousehold(ctx, tx, scope, &roster.Household{Name: "Here", Language: roster.English})
			if createErr != nil {
				return createErr
			}

			_, createErr = rosterStore.CreateContact(ctx, tx, scope, &roster.Contact{
				HouseholdID: here.ID, Name: "Already Here", Email: "HERE@example.com",
				Language: roster.English, Relationship: roster.Self,
			})

			return createErr
		}))

		plan := func() *importing.Plan {
			p, planErr := acuity.Plan(acuity.Input{
				Clients: clients, Appointments: appointments,
				Today: schedule.Today(now, central), Language: roster.Spanish,
			})
			must.NoError(t, planErr)

			return p
		}

		t.Run("annotating finds what the studio already has", func(t *testing.T) {
			p := plan()
			must.NoError(t, writer.Annotate(ctx, client.Reader(), scope, p))

			onRoster := map[string]bool{}
			for _, h := range p.Households {
				onRoster[h.Name] = h.OnRoster
			}

			test.Eq(t, map[string]bool{"García": false, "Ana Ruiz": false, "Already Here": true}, onRoster)

			existing := map[string]string{}
			for _, o := range p.Offerings {
				existing[o.Name] = o.ExistingID
			}

			test.Eq(t, map[string]string{"Piano 30": piano.ID, "Voice 60": ""}, existing)
		})

		t.Run("writing adds the clients, their lessons and their rows", func(t *testing.T) {
			var result *importing.Result

			must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
				var writeErr error

				result, writeErr = writer.Write(ctx, tx, scope, plan(), importing.Choices{Staff: []string{studio.User.ID}})

				return writeErr
			}))

			test.Eq(t, importing.Result{HouseholdsWritten: 2, HouseholdsOnRoster: 1, OfferingsWritten: 1, LessonsWritten: 2}, *result)

			households, listErr := rosterStore.ListHouseholds(ctx, client.Reader(), scope, false)
			must.NoError(t, listErr)

			byName := map[string]*roster.Household{}
			for _, h := range households {
				byName[h.Name] = h
			}

			must.MapLen(t, 3, byName)

			garcia := byName["García"]
			test.Eq(t, roster.Spanish, garcia.Language)
			must.SliceLen(t, 1, garcia.Contacts)
			test.Eq(t, "lucia@example.com", garcia.Contacts[0].Email)
			test.Eq(t, "+15125550101", garcia.Contacts[0].Phone)
			must.SliceLen(t, 1, garcia.Attendees)
			test.Eq(t, "Sofía García", garcia.Attendees[0].Name)

			ana := byName["Ana Ruiz"]
			must.SliceLen(t, 1, ana.Attendees)
			test.Eq(t, ana.Contacts[0].ID, ana.Attendees[0].ContactID, test.Sprint("an adult student is one person"))

			series, seriesErr := scheduleStore.ListSeries(ctx, client.Reader(), scope, false)
			must.NoError(t, seriesErr)
			must.SliceLen(t, 2, series)

			for _, s := range series {
				test.Eq(t, []string{studio.User.ID}, s.Staff)

				if s.OfferingID == piano.ID {
					test.Eq(t, time.Tuesday, s.Weekday)
					test.Eq(t, schedule.Date{Year: 2026, Month: time.November, Day: 10}, s.StartsOn)
					must.NotNil(t, s.PriceOverride, must.Sprint("Acuity charged 45 for what the studio lists at 40"))
					test.Eq(t, int64(4500), *s.PriceOverride)
				} else {
					test.Nil(t, s.PriceOverride, test.Sprint("a new offering's list price is what Acuity charged"))
				}

				occurrences, occErr := scheduleStore.ListOccurrencesForSeries(ctx, client.Reader(), scope, s.ID)
				must.NoError(t, occErr)
				test.SliceNotEmpty(t, occurrences, test.Sprint("the lesson is on the grid"))
			}
		})

		t.Run("running it again writes nothing twice", func(t *testing.T) {
			var result *importing.Result

			must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
				var writeErr error

				result, writeErr = writer.Write(ctx, tx, scope, plan(), importing.Choices{Staff: []string{studio.User.ID}})

				return writeErr
			}))

			test.Eq(t, importing.Result{HouseholdsOnRoster: 3}, *result)
		})
	})
}

func TestWriterSkipsWhatTheOwnerUnticked(t *testing.T) {
	t.Parallel()

	pgtest.Run(t, func(ctx context.Context, pg *pgtest.Instance) {
		client := pgclient.Open(t, ctx, pg)
		studio := seed.Studio(t, ctx, seed.New(t, client), &bootstrap.Studio{
			Username: "maria", EmailAddress: "maria@example.com",
			Password: "a good long password", Name: "Casa de Musica",
		})
		scope := tenancy.Of(studio.Account.ID)

		rosterStore, err := roster.NewSQLStore()
		must.NoError(t, err)

		scheduleStore, err := schedule.NewSQLStore()
		must.NoError(t, err)

		writer, err := importing.NewWriter(rosterStore, scheduleStore, nil)
		must.NoError(t, err)

		plan, err := acuity.Plan(acuity.Input{
			Clients: clients + "Gone Away,gone@example.com,,\n", Appointments: appointments,
			Today: schedule.Date{Year: 2026, Month: time.November, Day: 9},
		})
		must.NoError(t, err)

		skipHouseholds, skipLessons := map[string]bool{}, map[string]bool{}

		for _, h := range plan.Households {
			if h.Name == "Already Here" {
				skipHouseholds[h.Key] = true
			}
		}

		for _, l := range plan.Lessons {
			if l.AttendeeName == "Ana Ruiz" {
				skipLessons[l.Key] = true
			}
		}

		var result *importing.Result

		must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
			var writeErr error

			result, writeErr = writer.Write(ctx, tx, scope, plan, importing.Choices{
				Staff: []string{studio.User.ID}, SkipHouseholds: skipHouseholds, SkipLessons: skipLessons,
			})

			return writeErr
		}))

		test.Eq(t, importing.Result{HouseholdsWritten: 2, HouseholdsSkipped: 2, OfferingsWritten: 1, LessonsWritten: 1}, *result,
			test.Sprint("Already Here unticked, Gone Away inactive; Ana's lesson unticked, so Voice 60 is never needed"))
	})
}
