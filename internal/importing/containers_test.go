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

	"github.com/primandproper/platform-go/v15/audit"
	"github.com/primandproper/primitives-go/v2/database"
	"github.com/primandproper/primitives-go/v2/database/dialect"
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

		recorder, err := audit.NewRecorder(dialect.Postgres)
		must.NoError(t, err)

		// The roster records its writes as serve's does, the owner read off
		// the context; here the owner is the only one who ever writes.
		hooks, err := roster.NewRecordingHooks(recorder, func(context.Context) string { return studio.User.ID })
		must.NoError(t, err)

		rosterStore, err := roster.NewSQLStore(roster.WithHooks(hooks))
		must.NoError(t, err)

		scheduleStore, err := schedule.NewSQLStore()
		must.NoError(t, err)

		central, err := time.LoadLocation("US/Central")
		must.NoError(t, err)

		// Monday the ninth of November.
		now := time.Date(2026, time.November, 9, 9, 0, 0, 0, central)

		zones := schedule.ZoneFunc(func(context.Context, string) (*time.Location, error) { return central, nil })

		grid, err := schedule.NewMaterializer(client, scheduleStore, zones, schedule.WithClock(func() time.Time { return now }))
		must.NoError(t, err)

		writer, err := importing.NewWriter(rosterStore, scheduleStore, zones, recorder, grid)
		must.NoError(t, err)

		// The studio already sells "Piano 30", and already has one of the
		// clients, typed in by hand.
		var (
			piano *schedule.Offering
			here  *roster.Contact
		)

		must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
			var createErr error

			piano, createErr = scheduleStore.CreateOffering(ctx, tx, scope, &schedule.Offering{
				NameEN: "Piano 30", NameES: "Piano 30", Kind: schedule.KindSingle, Duration: 30 * time.Minute, ListPrice: 4000,
			})
			if createErr != nil {
				return createErr
			}

			household, createErr := rosterStore.CreateHousehold(ctx, tx, scope, &roster.Household{Name: "Here", Language: roster.English})
			if createErr != nil {
				return createErr
			}

			here, createErr = rosterStore.CreateContact(ctx, tx, scope, &roster.Contact{
				HouseholdID: household.ID, Name: "Already Here", Email: "HERE@example.com",
				Language: roster.English, Relationship: roster.Self,
			})

			return createErr
		}))

		planOf := func(appointments string) *importing.Plan {
			p, planErr := acuity.Plan(acuity.Input{
				Clients: clients, Appointments: appointments,
				Today: schedule.Today(now, central), Language: roster.Spanish,
			})
			must.NoError(t, planErr)

			return p
		}

		plan := func() *importing.Plan { return planOf(appointments) }

		write := func(t *testing.T, p *importing.Plan) importing.Result {
			t.Helper()

			var result *importing.Result

			must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
				var writeErr error

				result, writeErr = writer.Write(ctx, tx, scope, p, &importing.Choices{
					Staff: []string{studio.User.ID}, Actor: studio.User.ID, Source: "acuity",
				})

				return writeErr
			}))

			return *result
		}

		householdsByName := func(t *testing.T) map[string]*roster.Household {
			t.Helper()

			households, listErr := rosterStore.ListHouseholds(ctx, client.Reader(), scope, false)
			must.NoError(t, listErr)

			byName := map[string]*roster.Household{}
			for _, h := range households {
				byName[h.Name] = h
			}

			return byName
		}

		t.Run("annotating finds what the studio already has", func(t *testing.T) {
			p := plan()
			must.NoError(t, writer.Annotate(ctx, client.Reader(), scope, p))

			onRoster := map[string]string{}
			for _, h := range p.Households {
				onRoster[h.Name] = h.ExistingName
			}

			test.Eq(t, map[string]string{"García": "", "Ana Ruiz": "", "Already Here": "Here"}, onRoster)

			existing := map[string]string{}
			for _, o := range p.Offerings {
				existing[o.Name] = o.ExistingID
			}

			test.Eq(t, map[string]string{"Piano 30": piano.ID, "Voice 60": ""}, existing)
		})

		t.Run("writing adds the clients, their lessons and their rows", func(t *testing.T) {
			test.Eq(t, importing.Result{HouseholdsWritten: 2, HouseholdsUpdated: 1, OfferingsWritten: 1, LessonsWritten: 3}, write(t, plan()),
				test.Sprint("the client typed in by hand gets the student and the lesson they were missing"))

			byName := householdsByName(t)
			must.MapLen(t, 3, byName)

			existing := byName["Here"]
			must.SliceLen(t, 1, existing.Contacts, must.Sprint("the contact already here is not written again"))
			must.SliceLen(t, 1, existing.Attendees)
			test.Eq(t, here.ID, existing.Attendees[0].ContactID, test.Sprint("the adult student is the contact already here"))
			test.Eq(t, roster.English, existing.Attendees[0].Language, test.Sprint("added to a household, a person takes its language"))

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
			must.SliceLen(t, 3, series)

			for _, s := range series {
				test.Eq(t, []string{studio.User.ID}, s.Staff)

				if s.OfferingID == piano.ID && s.Weekday == time.Tuesday {
					test.Eq(t, time.Tuesday, s.Weekday)
					test.Eq(t, schedule.Date{Year: 2026, Month: time.November, Day: 10}, s.StartsOn)
					must.NotNil(t, s.PriceOverride, must.Sprint("Acuity charged 45 for what the studio lists at 40"))
					test.Eq(t, int64(4500), *s.PriceOverride)
				} else if s.OfferingID != piano.ID {
					test.Nil(t, s.PriceOverride, test.Sprint("a new offering's list price is what Acuity charged"))
				}

				occurrences, occErr := scheduleStore.ListOccurrencesForSeries(ctx, client.Reader(), scope, s.ID)
				must.NoError(t, occErr)
				test.SliceNotEmpty(t, occurrences, test.Sprint("the lesson is on the grid"))
			}
		})

		t.Run("running it again writes nothing twice", func(t *testing.T) {
			test.Eq(t, importing.Result{HouseholdsOnRoster: 3}, write(t, plan()))
		})

		// What Acuity booked after the first run: a second standing lesson
		// for Sofía, a brother on Saturdays, and one extra lesson for Ana.
		later := appointments +
			`"November 12, 2026 5:00pm","November 12, 2026 5:30pm",Sofía,García,lucia@example.com,Piano 30,45.00` + "\n" +
			`"November 19, 2026 5:00pm","November 19, 2026 5:30pm",Sofía,García,lucia@example.com,Piano 30,45.00` + "\n" +
			`"November 14, 2026 11:00am","November 14, 2026 11:30am",Mateo,García,lucia@example.com,Piano 30,45.00` + "\n" +
			`"November 21, 2026 11:00am","November 21, 2026 11:30am",Mateo,García,lucia@example.com,Piano 30,45.00` + "\n" +
			`"November 14, 2026 10:00am","November 14, 2026 10:45am",Ana,Ruiz,ana@example.com,Voice 60,70.00` + "\n"

		t.Run("a re-run previews only what is new", func(t *testing.T) {
			p := planOf(later)
			must.NoError(t, writer.Annotate(ctx, client.Reader(), scope, p))

			fresh := map[string]bool{}
			for _, l := range p.Lessons {
				fresh[l.AttendeeName+" "+l.Weekday.String()] = !l.OnRoster
			}

			test.Eq(t, map[string]bool{
				"Sofía García Tuesday": false, "Sofía García Thursday": true,
				"Mateo García Saturday": true, "Ana Ruiz Thursday": false, "Already Here Friday": false,
			}, fresh)

			must.SliceLen(t, 1, p.Appointments)
			test.False(t, p.Appointments[0].OnRoster)

			for _, h := range p.Households {
				if h.Name != "García" {
					continue
				}

				known := map[string]bool{}
				for _, a := range h.Attendees {
					known[a.Name] = a.OnRoster()
				}

				test.Eq(t, map[string]bool{"Sofía García": true, "Mateo García": false}, known)
			}
		})

		t.Run("a re-run adds what Acuity booked since, to the clients already here", func(t *testing.T) {
			test.Eq(t, importing.Result{HouseholdsUpdated: 2, HouseholdsOnRoster: 1, LessonsWritten: 2, AppointmentsWritten: 1}, write(t, planOf(later)))

			byName := householdsByName(t)
			must.MapLen(t, 3, byName, must.Sprint("no client written twice"))

			garcia := byName["García"]
			must.SliceLen(t, 1, garcia.Contacts)
			must.SliceLen(t, 2, garcia.Attendees, must.Sprint("Mateo joins his sister"))

			// Ana's extra lesson is a one-off at ten on Saturday, as long as
			// Acuity said.
			saturday := time.Date(2026, time.November, 14, 0, 0, 0, 0, central)

			booked, listErr := scheduleStore.ListOccurrencesInWindow(ctx, client.Reader(), scope, saturday, saturday.AddDate(0, 0, 1))
			must.NoError(t, listErr)

			var oneOff *schedule.Occurrence

			for _, o := range booked {
				if o.SeriesID == "" {
					oneOff = o
				}
			}

			must.NotNil(t, oneOff)
			test.True(t, oneOff.StartsAt.Equal(time.Date(2026, time.November, 14, 10, 0, 0, 0, central)))
			test.Eq(t, 45*time.Minute, oneOff.EndsAt.Sub(oneOff.StartsAt))
			test.Eq(t, []string{byName["Ana Ruiz"].Attendees[0].ID}, oneOff.Attendees)
			test.Eq(t, []string{studio.User.ID}, oneOff.Staff)

			// The owner cancels it here. Acuity still has it, and the last
			// run on cutover day must not put it back.
			must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
				_, cancelErr := scheduleStore.CancelOccurrence(ctx, tx, scope, oneOff.ID, schedule.CancelledByHousehold, false, "")

				return cancelErr
			}))

			test.Eq(t, importing.Result{HouseholdsOnRoster: 3}, write(t, planOf(later)))
		})

		t.Run("every run, and everything it wrote, is in the studio's audit log", func(t *testing.T) {
			reader, readerErr := audit.NewReader(dialect.Postgres)
			must.NoError(t, readerErr)

			entries := func(resourceType string) []*audit.Entry {
				found, listErr := reader.List(ctx, client.Reader(), scope, &audit.Query{ResourceType: resourceType}, nil)
				must.NoError(t, listErr)

				return found.Data
			}

			// Four runs: the first, the one that found nothing, the one that
			// added, and the last after the owner's cancellation.
			runs := entries(importing.ResourceImport)
			must.SliceLen(t, 4, runs)

			added := 0

			for _, run := range runs {
				test.Eq(t, studio.User.ID, run.Actor.ID)
				test.Eq(t, "acuity", run.Metadata[importing.KeySource])

				if run.Metadata[importing.KeyAppointmentsWritten] == "1" {
					added++
					test.Eq(t, "2", run.Metadata[importing.KeyHouseholdsUpdated])
				}
			}

			test.Eq(t, 1, added)

			// The roster's rows, through its hooks: the household typed in at
			// the start and the two the first run wrote, and every student.
			test.SliceLen(t, 3, entries(roster.ResourceHousehold))
			test.SliceLen(t, 4, entries(roster.ResourceAttendee), test.Sprint("Sofía, Ana, Already Here and Mateo"))

			// Ana's one-off says it was imported.
			oneOffs := 0

			for _, entry := range entries(schedule.ResourceOccurrence) {
				if entry.Metadata[schedule.KeyCommand] == schedule.CommandImport {
					oneOffs++
					test.Eq(t, audit.EventCreated, entry.EventType)
				}
			}

			test.Eq(t, 1, oneOffs)
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

		recorder, err := audit.NewRecorder(dialect.Postgres)
		must.NoError(t, err)

		writer, err := importing.NewWriter(rosterStore, scheduleStore,
			schedule.ZoneFunc(func(context.Context, string) (*time.Location, error) { return time.UTC, nil }), recorder, nil)
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

			result, writeErr = writer.Write(ctx, tx, scope, plan, &importing.Choices{
				Staff: []string{studio.User.ID}, SkipHouseholds: skipHouseholds, SkipLessons: skipLessons,
			})

			return writeErr
		}))

		test.Eq(t, importing.Result{HouseholdsWritten: 2, HouseholdsSkipped: 2, OfferingsWritten: 1, LessonsWritten: 1}, *result,
			test.Sprint("Already Here unticked, Gone Away inactive; Ana's lesson unticked, so Voice 60 is never needed"))
	})
}
