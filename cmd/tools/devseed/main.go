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
// A studio it provisions also gets the sample offerings below, so the owner app
// has something to put in a slot on first sign-in.
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

	offerings, err := seedOfferings(ctx, i, tenancy.Of(registered.Account.ID))
	if err != nil {
		return err
	}

	for _, offering := range offerings {
		fmt.Printf("offering: %s (%s)\n", offering.ID, offering.NameEN)
	}

	return nil
}

// seedOfferings writes the sample offerings into the studio on one transaction.
func seedOfferings(ctx context.Context, i do.Injector, scope tenancy.Scope) ([]*schedule.Offering, error) {
	client, err := do.Invoke[database.Client](i)
	if err != nil {
		return nil, err
	}

	store, err := do.Invoke[schedule.Store](i)
	if err != nil {
		return nil, err
	}

	var written []*schedule.Offering

	if err = client.WithTransaction(ctx, func(tx database.Tx) error {
		for _, offering := range sampleOfferings() {
			created, createErr := store.CreateOffering(ctx, tx, scope, offering)
			if createErr != nil {
				return fmt.Errorf("seeding the offering %q: %w", offering.NameEN, createErr)
			}

			written = append(written, created)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return written, nil
}
