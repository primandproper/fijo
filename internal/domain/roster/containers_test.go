package roster_test

import (
	"context"
	"testing"

	"github.com/primandproper/fijo/internal/bootstrap"
	"github.com/primandproper/fijo/internal/domain/roster"
	"github.com/primandproper/fijo/internal/testutils/pgclient"
	"github.com/primandproper/fijo/internal/testutils/seed"

	"github.com/primandproper/primitives-go/v2/database"
	"github.com/primandproper/primitives-go/v2/tenancy"
	"github.com/primandproper/primitives-go/v2/testutils/containers/pgtest"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestSQLStoreAgainstPostgres(t *testing.T) {
	t.Parallel()

	pgtest.Run(t, func(ctx context.Context, pg *pgtest.Instance) {
		client := pgclient.Open(t, ctx, pg)
		seeder := seed.New(t, client)

		casa := seed.Studio(t, ctx, seeder, &bootstrap.Studio{
			Username: "maria", EmailAddress: "maria@example.com",
			Password: "a good long password", Name: "Casa de Musica",
		})
		other := seed.Studio(t, ctx, seeder, &bootstrap.Studio{
			Username: "other", EmailAddress: "other@example.com",
			Password: "a good long password", Name: "Another Studio",
		})

		scope := tenancy.Of(casa.Account.ID)
		otherScope := tenancy.Of(other.Account.ID)

		store, err := roster.NewSQLStore()
		must.NoError(t, err)

		var (
			garcia *roster.Household
			lucia  *roster.Contact
			sofia  *roster.Attendee
		)

		t.Run("a household, its guardian and her daughter", func(t *testing.T) {
			must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
				var createErr error

				if garcia, createErr = store.CreateHousehold(ctx, tx, scope, &roster.Household{Name: " García ", Language: roster.Spanish}); createErr != nil {
					return createErr
				}

				if lucia, createErr = store.CreateContact(ctx, tx, scope, &roster.Contact{
					HouseholdID: garcia.ID, Name: "Lucía García", Phone: "(512) 555-0100",
					Language: roster.Spanish, Relationship: roster.Guardian,
				}); createErr != nil {
					return createErr
				}

				sofia, createErr = store.CreateAttendee(ctx, tx, scope, &roster.Attendee{
					HouseholdID: garcia.ID, Name: "Sofia", Language: roster.English,
				})

				return createErr
			}))

			test.Eq(t, "García", garcia.Name)
			test.Eq(t, "+15125550100", lucia.Phone)

			read, readErr := store.GetHousehold(ctx, client.Reader(), scope, garcia.ID)
			must.NoError(t, readErr)
			must.Len(t, 1, read.Contacts)
			must.Len(t, 1, read.Attendees)
			test.Eq(t, lucia.ID, read.Contacts[0].ID)
			test.Eq(t, sofia.ID, read.Attendees[0].ID)

			listed, listErr := store.ListHouseholds(ctx, client.Reader(), scope, false)
			must.NoError(t, listErr)
			must.Len(t, 1, listed)
			test.Len(t, 1, listed[0].Attendees)
		})

		t.Run("another studio sees none of it", func(t *testing.T) {
			_, readErr := store.GetHousehold(ctx, client.Reader(), otherScope, garcia.ID)
			test.ErrorIs(t, readErr, roster.ErrNotFound)

			listed, listErr := store.ListHouseholds(ctx, client.Reader(), otherScope, false)
			must.NoError(t, listErr)
			test.SliceEmpty(t, listed)

			refusal := client.WithTransaction(ctx, func(tx database.Tx) error {
				_, createErr := store.CreateAttendee(ctx, tx, otherScope, &roster.Attendee{
					HouseholdID: garcia.ID, Name: "Intruder", Language: roster.English,
				})

				return createErr
			})
			test.ErrorIs(t, refusal, roster.ErrNotFound)
		})

		t.Run("a phone belongs to one contact in a studio", func(t *testing.T) {
			refusal := client.WithTransaction(ctx, func(tx database.Tx) error {
				_, createErr := store.CreateContact(ctx, tx, scope, &roster.Contact{
					HouseholdID: garcia.ID, Name: "Somebody", Phone: "+1 512 555 0100",
					Language: roster.English, Relationship: roster.Guardian,
				})

				return createErr
			})
			test.ErrorIs(t, refusal, roster.ErrPhoneTaken)

			// The same number in another studio is that studio's business.
			must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
				theirs, createErr := store.CreateHousehold(ctx, tx, otherScope, &roster.Household{Name: "Elsewhere", Language: roster.English})
				if createErr != nil {
					return createErr
				}

				_, createErr = store.CreateContact(ctx, tx, otherScope, &roster.Contact{
					HouseholdID: theirs.ID, Name: "Lucía García", Phone: "+15125550100",
					Language: roster.Spanish, Relationship: roster.Guardian,
				})

				return createErr
			}))
		})

		t.Run("an adult student is a contact and an attendee", func(t *testing.T) {
			var ana *roster.Attendee

			must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
				household, createErr := store.CreateHousehold(ctx, tx, scope, &roster.Household{Name: "Ana Ruiz", Language: roster.English})
				if createErr != nil {
					return createErr
				}

				contact, createErr := store.CreateContact(ctx, tx, scope, &roster.Contact{
					HouseholdID: household.ID, Name: "Ana Ruiz", Phone: "512-555-0199",
					Language: roster.English, Relationship: roster.Self,
				})
				if createErr != nil {
					return createErr
				}

				ana, createErr = store.CreateAttendee(ctx, tx, scope, &roster.Attendee{
					HouseholdID: household.ID, Name: "Ana Ruiz", Language: roster.English, ContactID: contact.ID,
				})
				if createErr != nil {
					return createErr
				}

				// Linking somebody else's contact is refused.
				_, linkErr := store.CreateAttendee(ctx, tx, scope, &roster.Attendee{
					HouseholdID: household.ID, Name: "Not Lucía", Language: roster.English, ContactID: lucia.ID,
				})
				test.ErrorIs(t, linkErr, roster.ErrInvalidAttendee)

				// Archiving her phone leaves her an attendee nobody can text.
				if archiveErr := store.ArchiveContact(ctx, tx, scope, contact.ID); archiveErr != nil {
					return archiveErr
				}

				return nil
			}))

			read, readErr := store.GetAttendee(ctx, client.Reader(), scope, ana.ID)
			must.NoError(t, readErr)
			test.Eq(t, "", read.ContactID)
		})

		t.Run("archiving a household archives everybody on it", func(t *testing.T) {
			attendees, listErr := store.AttendeesOfHousehold(ctx, client.Reader(), scope, garcia.ID)
			must.NoError(t, listErr)
			test.Eq(t, []string{sofia.ID}, attendees)

			must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
				return store.ArchiveHousehold(ctx, tx, scope, garcia.ID)
			}))

			_, readErr := store.GetAttendee(ctx, client.Reader(), scope, sofia.ID)
			test.ErrorIs(t, readErr, roster.ErrNotFound)

			_, readErr = store.GetContact(ctx, client.Reader(), scope, lucia.ID)
			test.ErrorIs(t, readErr, roster.ErrNotFound)

			// Her number is free again.
			must.NoError(t, client.WithTransaction(ctx, func(tx database.Tx) error {
				household, createErr := store.CreateHousehold(ctx, tx, scope, &roster.Household{Name: "García (again)", Language: roster.Spanish})
				if createErr != nil {
					return createErr
				}

				_, createErr = store.CreateContact(ctx, tx, scope, &roster.Contact{
					HouseholdID: household.ID, Name: "Lucía García", Phone: "+15125550100",
					Language: roster.Spanish, Relationship: roster.Guardian,
				})

				return createErr
			}))
		})
	})
}
