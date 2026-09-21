// Command devseed registers an owner and their studio against the configured
// database, for a developer who has just brought a local stack up and has
// nobody to sign in as.
//
// It is a development-time tool and is never deployed. The platform's sign-in
// surface registers a user only for an authenticated caller — registration is
// a registrar's act, not the visitor's — so on an empty database there is no
// door that can create the first owner, and this is the ladder over the wall.
// How a studio comes to exist in production (a self-serve sign-up page, an
// operator's invitation, or both) is a phase-1 decision; see docs/DESIGN.md.
//
// Usage:
//
//	go run ./cmd/tools/devseed --config config/localdev.json \
//	  --username maria --email maria@example.com --password '...' \
//	  --studio "Casa de Musica" --time-zone US/Central
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/primandproper/fijo/internal/authz/roles"
	"github.com/primandproper/fijo/internal/build/api"
	"github.com/primandproper/fijo/internal/config"

	"github.com/primandproper/platform-go/v14/authentication/signin"
	"github.com/primandproper/platform-go/v14/identity"
	"github.com/primandproper/primitives-go/v2/tenancy"

	"github.com/samber/do/v2"
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
	)

	flag.Parse()

	for name, value := range map[string]string{"config": *configPath, "username": *username, "email": *email, "password": *password, "studio": *studio} {
		if value == "" {
			log.Fatalf("--%s is required", name)
		}
	}

	if err := run(context.Background(), *configPath, &signin.Registration{
		User: &identity.User{
			Username:      *username,
			EmailAddress:  *email,
			FirstName:     *firstName,
			LastName:      *lastName,
			DisplayName:   *firstName,
			AccountStatus: identity.StatusGood,
		},
		Account:    &identity.Account{Name: *studio, TimeZone: *timeZone},
		Credential: signin.Password(*password),
		OwnerRoles: []string{roles.Owner},
	}); err != nil {
		log.Fatalf("seeding: %v", err)
	}
}

func run(ctx context.Context, configPath string, registration *signin.Registration) error {
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

	svc, err := do.Invoke[*signin.Service](i)
	if err != nil {
		return err
	}

	registered, err := svc.Register(ctx, tenancy.Global(), registration)
	if err != nil {
		return err
	}

	fmt.Printf("user:    %s (%s)\nstudio:  %s (%s)\n", registered.User.ID, registered.User.Username, registered.Account.ID, registered.Account.Name)

	return nil
}
