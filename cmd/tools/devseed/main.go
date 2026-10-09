// Command devseed registers an owner and their studio against the configured
// database, for a developer who has just brought a local stack up and has
// nobody to sign in as.
//
// It is a development-time tool and is never deployed. The work is
// internal/bootstrap's, which container tests call too; this is the flag
// parsing around it. The operator's half — making the first service_admin on
// a real deployment — is cmd/tools/bootstrap.
//
// Usage:
//
//	go run ./cmd/tools/devseed --config config/localdev.json \
//	  --username maria --email maria@example.com --password '...' \
//	  --studio "Casa de Musica" --time-zone US/Central
//
// A studio it provisions also gets the samples below — offerings, a few client
// households and their standing lessons — so the owner app has a week to look
// at on first sign-in.
//
// --if-absent makes a second run naming the same owner a no-op rather than the
// registrar's refusal, so the local compose stack can seed on every start.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/primandproper/fijo/internal/bootstrap"
	"github.com/primandproper/fijo/internal/build/api"
	"github.com/primandproper/fijo/internal/config"
	"github.com/primandproper/fijo/internal/domain/roster"
	"github.com/primandproper/fijo/internal/domain/schedule"

	"github.com/primandproper/primitives-go/v2/database"
	"github.com/primandproper/primitives-go/v2/tenancy"

	"github.com/samber/do/v2"
)

const day = 24 * time.Hour

// sampleOfferings is one of each kind, priced in cents.
func sampleOfferings() []*schedule.Offering {
	return []*schedule.Offering{
		{
			NameEN: "30-minute private lesson", NameES: "Clase particular de 30 minutos",
			Kind: schedule.KindSingle, Duration: 30 * time.Minute, ListPrice: 4000,
		},
		{
			NameEN: "45-minute private lesson", NameES: "Clase particular de 45 minutos",
			Kind: schedule.KindSingle, Duration: 45 * time.Minute, ListPrice: 5500,
		},
		{
			NameEN: "10-lesson pack", NameES: "Paquete de 10 clases",
			Kind: schedule.KindPackage, Duration: 30 * time.Minute, ListPrice: 38000,
			LessonCount: 10, ExpiresAfter: 120 * day, Shareable: true,
		},
		{
			NameEN: "Weekly lessons, monthly", NameES: "Clases semanales, mensual",
			Kind: schedule.KindRecurring, Duration: 30 * time.Minute, ListPrice: 15000,
		},
	}
}

func main() {
	var (
		configPath = flag.String("config", os.Getenv("FIJO_CONFIG_FILEPATH"), "path to the JSON config naming the database")
		username   = flag.String("username", "", "the owner's username")
		email      = flag.String("email", "", "the owner's email address")
		password   = flag.String("password", "", "the owner's password")
		firstName  = flag.String("first-name", "", "the owner's first name")
		lastName   = flag.String("last-name", "", "the owner's last name")
		studio     = flag.String("studio", "", "the studio's name")
		timeZone   = flag.String("time-zone", "US/Central", "the studio's IANA time zone")
		ifAbsent   = flag.Bool("if-absent", false, "do nothing when the directory already holds --username")
	)

	flag.Parse()

	if *configPath == "" {
		log.Fatal("--config is required")
	}

	if err := run(context.Background(), *configPath, *ifAbsent, &bootstrap.Studio{
		Username:     *username,
		EmailAddress: *email,
		Password:     *password,
		FirstName:    *firstName,
		LastName:     *lastName,
		Name:         *studio,
		TimeZone:     *timeZone,
	}); err != nil {
		log.Fatalf("seeding: %v", err)
	}
}

func run(ctx context.Context, configPath string, ifAbsent bool, studio *bootstrap.Studio) error {
	cfg, err := config.LoadFromFile(ctx, configPath)
	if err != nil {
		return err
	}

	i, err := api.BuildInjector(ctx, cfg)
	if err != nil {
		return err
	}

	defer func() {
		if report := i.ShutdownWithContext(ctx); report != nil && !report.Succeed {
			fmt.Fprintln(os.Stderr, "shutting down the injector reported errors:", report.Error())
		}
	}()

	seeder, err := bootstrap.FromInjector(i)
	if err != nil {
		return err
	}

	if ifAbsent {
		exists, existsErr := seeder.UserExists(ctx, studio.Username)
		if existsErr != nil {
			return existsErr
		}

		if exists {
			fmt.Printf("user:    %s already exists; nothing to seed\n", studio.Username)

			return nil
		}
	}

	registered, err := seeder.ProvisionStudio(ctx, studio)
	if err != nil {
		return err
	}

	fmt.Printf("user:    %s (%s)\nstudio:  %s (%s)\n", registered.User.ID, registered.User.Username, registered.Account.ID, registered.Account.Name)

	return seedSamples(ctx, i, tenancy.Of(registered.Account.ID), registered.User.ID)
}

// sampleClient is a household for the sample studio and the standing lessons
// its students take.
type sampleClient struct {
	household roster.Household
	contact   roster.Contact
	children  []string
	lessons   []*sampleLesson
	// attends makes the contact a student too: an adult who takes lessons.
	attends bool
}

// sampleLesson is a standing slot, naming its student by name and its offering
// by its place in sampleOfferings.
type sampleLesson struct {
	student  string
	offering int
	weekday  time.Weekday
	at       schedule.ClockTime
	interval int
}

// sampleClients is a family with two children, an adult student, and a family
// on alternate weeks. The numbers are in the 555-01xx range, which is reserved
// for fiction.
func sampleClients() []*sampleClient {
	const ana = "Ana Ruiz"

	return []*sampleClient{
		{
			household: roster.Household{Name: "García", Language: roster.Spanish, Notes: "Pickup is grandma on Thursdays."},
			contact:   roster.Contact{Name: "Lucía García", Phone: "+1 512 555 0101", Language: roster.Spanish, Relationship: roster.Guardian},
			children:  []string{"Sofía García", "Mateo García"},
			lessons: []*sampleLesson{
				{student: "Sofía García", offering: 0, weekday: time.Tuesday, at: schedule.ClockTime{Hour: 16}, interval: 1},
				{student: "Mateo García", offering: 2, weekday: time.Tuesday, at: schedule.ClockTime{Hour: 16, Minute: 30}, interval: 1},
			},
		},
		{
			household: roster.Household{Name: ana, Language: roster.English},
			contact:   roster.Contact{Name: ana, Phone: "+1 512 555 0102", Language: roster.English, Relationship: roster.Self},
			attends:   true,
			lessons: []*sampleLesson{
				{student: ana, offering: 1, weekday: time.Thursday, at: schedule.ClockTime{Hour: 18}, interval: 1},
			},
		},
		{
			household: roster.Household{Name: "Chen", Language: roster.English},
			contact:   roster.Contact{Name: "Wei Chen", Phone: "+1 512 555 0103", Language: roster.English, Relationship: roster.Guardian},
			children:  []string{"Lily Chen"},
			lessons: []*sampleLesson{
				{student: "Lily Chen", offering: 3, weekday: time.Saturday, at: schedule.ClockTime{Hour: 10}, interval: 2},
			},
		},
	}
}

// seedSamples writes the sample offerings, clients and their standing lessons
// on one transaction, and materializes the grid on it so the week view is not
// empty while the worker catches up.
func seedSamples(ctx context.Context, i do.Injector, scope tenancy.Scope, teacher string) error {
	client, err := do.Invoke[database.Client](i)
	if err != nil {
		return err
	}

	grid, err := do.Invoke[schedule.Store](i)
	if err != nil {
		return err
	}

	clients, err := do.Invoke[roster.Store](i)
	if err != nil {
		return err
	}

	materializer, err := do.Invoke[*schedule.Materializer](i)
	if err != nil {
		return err
	}

	zones, err := do.Invoke[schedule.Zones](i)
	if err != nil {
		return err
	}

	zone, err := zones.ZoneOf(ctx, scope.Owner())
	if err != nil {
		return err
	}

	today := schedule.Today(time.Now(), zone)

	return client.WithTransaction(ctx, func(tx database.Tx) error {
		var offerings []*schedule.Offering

		for _, offering := range sampleOfferings() {
			created, createErr := grid.CreateOffering(ctx, tx, scope, offering)
			if createErr != nil {
				return fmt.Errorf("seeding the offering %q: %w", offering.NameEN, createErr)
			}

			offerings = append(offerings, created)
			fmt.Printf("offering: %s (%s)\n", created.ID, created.NameEN)
		}

		for _, sample := range sampleClients() {
			students, createErr := seedClient(ctx, tx, clients, scope, sample)
			if createErr != nil {
				return fmt.Errorf("seeding the client %q: %w", sample.household.Name, createErr)
			}

			for _, lesson := range sample.lessons {
				series, seriesErr := grid.CreateSeries(ctx, tx, scope, &schedule.Series{
					OfferingID:    offerings[lesson.offering].ID,
					Weekday:       lesson.weekday,
					StartTime:     lesson.at,
					IntervalWeeks: lesson.interval,
					StartsOn:      today,
					Staff:         []string{teacher},
					Attendees:     []string{students[lesson.student]},
				})
				if seriesErr != nil {
					return fmt.Errorf("seeding %s's standing lesson: %w", lesson.student, seriesErr)
				}

				fmt.Printf("lesson:  %s (%s, %ss at %s)\n", series.ID, lesson.student, lesson.weekday, lesson.at)
			}
		}

		written, syncErr := materializer.SyncStudio(ctx, tx, scope)
		if syncErr != nil {
			return syncErr
		}

		fmt.Printf("grid:    %d appointments\n", written)

		return nil
	})
}

// seedClient writes one household and its people, and answers each student's
// attendee identifier by name.
func seedClient(ctx context.Context, tx database.Tx, store roster.Store, scope tenancy.Scope, sample *sampleClient) (map[string]string, error) {
	household, err := store.CreateHousehold(ctx, tx, scope, &sample.household)
	if err != nil {
		return nil, err
	}

	fmt.Printf("client:  %s (%s)\n", household.ID, household.Name)

	contact := sample.contact
	contact.HouseholdID = household.ID

	written, err := store.CreateContact(ctx, tx, scope, &contact)
	if err != nil {
		return nil, err
	}

	students := map[string]string{}

	if sample.attends {
		attendee, createErr := store.CreateAttendee(ctx, tx, scope, &roster.Attendee{
			HouseholdID: household.ID, Name: written.Name, Language: written.Language, ContactID: written.ID,
		})
		if createErr != nil {
			return nil, createErr
		}

		students[attendee.Name] = attendee.ID
	}

	for _, child := range sample.children {
		attendee, createErr := store.CreateAttendee(ctx, tx, scope, &roster.Attendee{
			HouseholdID: household.ID, Name: child, Language: household.Language,
		})
		if createErr != nil {
			return nil, createErr
		}

		students[attendee.Name] = attendee.ID
	}

	return students, nil
}
