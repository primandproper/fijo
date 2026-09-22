// Command bootstrap makes the operator of a deployment exist.
//
// It is the one act a new Fijo database has no door for. Granting
// service_admin is a call that needs identity.users.update_service_roles, and
// on a directory with no users in it nobody holds it; what authorizes this
// tool instead is the database credential it is run with. Everything after
// it goes through the front door: an operator who exists can register studios
// over identity's own RPCs.
//
// It is never deployed. An operator runs it once, from wherever they can
// reach the database, and it is safe to run again — an operator who already
// exists is not registered twice and one who already holds the role is not
// written again.
//
// Usage:
//
//	go run ./cmd/tools/bootstrap --config config/production.json \
//	  --username jeffrey --email jeffrey@example.com --password '...'
//
// The config is read the way every Fijo workload reads one, so FIJO_*
// variables overlay what the file says and the database password need not be
// written down.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/primandproper/fijo/internal/bootstrap"
	"github.com/primandproper/fijo/internal/build/api"
	"github.com/primandproper/fijo/internal/config"
)

func main() {
	var (
		configPath  = flag.String("config", os.Getenv("FIJO_CONFIG_FILEPATH"), "path to the JSON config naming the database")
		username    = flag.String("username", "", "the operator's username")
		email       = flag.String("email", "", "the operator's email address")
		password    = flag.String("password", "", "the operator's password, used only when they do not exist yet")
		firstName   = flag.String("first-name", "", "the operator's first name")
		lastName    = flag.String("last-name", "", "the operator's last name")
		accountName = flag.String("account-name", bootstrap.DefaultOperatorAccountName, "the account minted for them, which is not a studio anybody teaches in")
		timeZone    = flag.String("time-zone", bootstrap.DefaultTimeZone, "that account's IANA time zone")
	)

	flag.Parse()

	for name, value := range map[string]string{"config": *configPath, "username": *username} {
		if strings.TrimSpace(value) == "" {
			log.Fatalf("--%s is required", name)
		}
	}

	if err := run(context.Background(), *configPath, &bootstrap.Admin{
		Username:     *username,
		EmailAddress: *email,
		Password:     *password,
		FirstName:    *firstName,
		LastName:     *lastName,
		AccountName:  *accountName,
		TimeZone:     *timeZone,
	}); err != nil {
		log.Fatalf("bootstrapping: %v", err)
	}
}

func run(ctx context.Context, configPath string, admin *bootstrap.Admin) error {
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

	operator, err := seeder.EnsureServiceAdmin(ctx, admin)
	if err != nil {
		return err
	}

	fmt.Printf("operator: %s (%s)\nroles:    %s\n", operator.ID, operator.Username, strings.Join(operator.ServiceRoles, ", "))

	return nil
}
