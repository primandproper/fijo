package roster_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/primandproper/fijo/internal/bootstrap"
	"github.com/primandproper/fijo/internal/domain/roster"
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

func TestRecordingHooksAgainstPostgres(t *testing.T) {
	t.Parallel()

	pgtest.Run(t, func(ctx context.Context, pg *pgtest.Instance) {
		client := pgclient.Open(t, ctx, pg)

		casa := seed.Studio(t, ctx, seed.New(t, client), &bootstrap.Studio{
			Username: "maria", EmailAddress: "maria@example.com",
			Password: "a good long password", Name: "Casa de Musica",
		})
		scope := tenancy.Of(casa.Account.ID)

		recorder, err := audit.NewRecorder(dialect.Postgres)
		must.NoError(t, err)

		reader, err := audit.NewReader(dialect.Postgres)
		must.NoError(t, err)

		hooks, err := roster.NewRecordingHooks(recorder, func(context.Context) string { return casa.User.ID })
		must.NoError(t, err)

		store, err := roster.NewSQLStore(roster.WithHooks(hooks))
		must.NoError(t, err)

		write := func(t *testing.T, fn func(tx database.Tx) error) {
			t.Helper()
			must.NoError(t, client.WithTransaction(ctx, fn))
		}

		history := func(t *testing.T, resourceType, id string) []*audit.Entry {
			t.Helper()

			found, listErr := reader.List(ctx, client.Reader(), scope, &audit.Query{ResourceType: resourceType, ResourceID: id}, nil)
			must.NoError(t, listErr)

			return found.Data
		}

		var (
			household *roster.Household
			contact   *roster.Contact
			attendee  *roster.Attendee
		)

		write(t, func(tx database.Tx) error {
			var createErr error

			if household, createErr = store.CreateHousehold(ctx, tx, scope, &roster.Household{Name: "García", Language: roster.Spanish}); createErr != nil {
				return createErr
			}

			if contact, createErr = store.CreateContact(ctx, tx, scope, &roster.Contact{
				HouseholdID: household.ID, Name: "Lucía García", Email: "lucia@example.com",
				Language: roster.Spanish, Relationship: roster.Guardian,
			}); createErr != nil {
				return createErr
			}

			attendee, createErr = store.CreateAttendee(ctx, tx, scope, &roster.Attendee{
				HouseholdID: household.ID, Name: "Sofía García", Language: roster.Spanish,
			})

			return createErr
		})

		t.Run("a creation names who made it and the household it is on", func(t *testing.T) {
			entries := history(t, roster.ResourceContact, contact.ID)
			must.SliceLen(t, 1, entries)

			test.Eq(t, audit.EventCreated, entries[0].EventType)
			test.Eq(t, casa.User.ID, entries[0].Actor.ID)
			test.Eq(t, household.ID, entries[0].Metadata[roster.KeyHousehold])
			test.MapEmpty(t, entries[0].Changes)
		})

		t.Run("an update records what moved, and what is personal only as a digest", func(t *testing.T) {
			write(t, func(tx database.Tx) error {
				changed := *contact
				changed.Email = "lucia.garcia@example.com"
				changed.Language = roster.English

				_, updateErr := store.UpdateContact(ctx, tx, scope, &changed)

				return updateErr
			})

			entries := history(t, roster.ResourceContact, contact.ID)
			must.SliceLen(t, 2, entries)

			update := updateIn(entries)
			must.NotNil(t, update)
			must.MapContainsKeys(t, update.Changes, []string{"email", "language"})
			test.MapNotContainsKey(t, update.Changes, "lastUpdatedAt")

			for _, side := range []any{update.Changes["email"].Old, update.Changes["email"].New} {
				digest, ok := side.(string)
				must.True(t, ok)
				test.True(t, strings.HasPrefix(digest, "sha256:"), test.Sprintf("%q is the address as written", digest))
			}

			test.Eq(t, any(string(roster.English)), update.Changes["language"].New, test.Sprint("a language is not personal"))
		})

		t.Run("saving what did not change records nothing", func(t *testing.T) {
			write(t, func(tx database.Tx) error {
				_, updateErr := store.UpdateAttendee(ctx, tx, scope, attendee)

				return updateErr
			})

			test.SliceLen(t, 1, history(t, roster.ResourceAttendee, attendee.ID))
		})

		t.Run("a household's reminder times are an update to it", func(t *testing.T) {
			write(t, func(tx database.Tx) error {
				_, setErr := store.SetHouseholdReminders(ctx, tx, scope, household.ID, 2*time.Hour, 0)

				return setErr
			})

			entries := history(t, roster.ResourceHousehold, household.ID)
			must.SliceLen(t, 2, entries)

			update := updateIn(entries)
			must.NotNil(t, update)
			must.MapContainsKeys(t, update.Changes, []string{"firstReminder", "secondReminder"})
			test.MapNotContainsKey(t, update.Changes, "contacts", test.Sprint("a household's people have their own entries"))
		})

		t.Run("archiving records the archive, and a household's once", func(t *testing.T) {
			write(t, func(tx database.Tx) error {
				return store.ArchiveAttendee(ctx, tx, scope, attendee.ID)
			})

			entries := history(t, roster.ResourceAttendee, attendee.ID)
			must.SliceLen(t, 2, entries)

			write(t, func(tx database.Tx) error {
				return store.ArchiveHousehold(ctx, tx, scope, household.ID)
			})

			archived := 0

			for _, entry := range history(t, roster.ResourceHousehold, household.ID) {
				if entry.EventType == audit.EventArchived {
					archived++
				}
			}

			test.Eq(t, 1, archived)
			test.SliceLen(t, 2, history(t, roster.ResourceContact, contact.ID), test.Sprint("the contact went with the household, not on its own"))
		})

		t.Run("a write whose hook fails is not made", func(t *testing.T) {
			failing, hookErr := roster.NewRecordingHooks(failingRecorder{}, nil)
			must.NoError(t, hookErr)

			broken, storeErr := roster.NewSQLStore(roster.WithHooks(failing))
			must.NoError(t, storeErr)

			writeErr := client.WithTransaction(ctx, func(tx database.Tx) error {
				_, createErr := broken.CreateHousehold(ctx, tx, scope, &roster.Household{Name: "Never", Language: roster.English})

				return createErr
			})
			test.Error(t, writeErr)

			households, listErr := store.ListHouseholds(ctx, client.Reader(), scope, true)
			must.NoError(t, listErr)

			for _, h := range households {
				test.NotEq(t, "Never", h.Name)
			}
		})
	})
}

// updateIn is the update among a resource's entries.
func updateIn(entries []*audit.Entry) *audit.Entry {
	for _, entry := range entries {
		if entry.EventType == audit.EventUpdated {
			return entry
		}
	}

	return nil
}

// failingRecorder refuses every entry, as an audit log that cannot be written
// to would.
type failingRecorder struct{}

func (failingRecorder) Record(context.Context, database.Tx, tenancy.Scope, ...*audit.Entry) error {
	return audit.ErrEmptyActor
}
