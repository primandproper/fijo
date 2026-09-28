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
// --if-absent makes a second run naming the same owner a no-op rather than the
// registrar's refusal, so the local compose stack can seed on every start.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/primandproper/fijo/internal/bootstrap"
	"github.com/primandproper/fijo/internal/build/api"
	"github.com/primandproper/fijo/internal/config"
)

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

	return nil
}
