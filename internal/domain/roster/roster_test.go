package roster

import (
	"testing"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestNormalizePhone(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{
		"(512) 555-0100":   "+15125550100",
		"512.555.0100":     "+15125550100",
		"1 512 555 0100":   "+15125550100",
		"+1 512 555 0100":  "+15125550100",
		"+52 55 1234 5678": "+525512345678",
		" 5125550100 ":     "+15125550100",
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizePhone(raw)
			must.NoError(t, err)
			test.Eq(t, want, got)
		})
	}

	for _, raw := range []string{"", "555-0100", "call me", "+0 512 555 0100", "+1 512 555 0100 ext 4", "+1234567890123456"} {
		t.Run("refuses "+raw, func(t *testing.T) {
			t.Parallel()

			_, err := NormalizePhone(raw)
			test.ErrorIs(t, err, ErrInvalidPhone)
			test.ErrorIs(t, err, ErrInvalidContact)
		})
	}
}

func TestNormalizeEmail(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{
		"ana@example.com":          "ana@example.com",
		"  Ana.Ruiz@Example.COM  ": "ana.ruiz@example.com",
		"lucia+piano@example.mx":   "lucia+piano@example.mx",
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeEmail(raw)
			must.NoError(t, err)
			test.Eq(t, want, got)
		})
	}

	for _, raw := range []string{"", "ana", "ana@", "@example.com", "ana@example", "Ana Ruiz <ana@example.com>", "ana@@example.com", "ana ruiz@example.com"} {
		t.Run("refuses "+raw, func(t *testing.T) {
			t.Parallel()

			_, err := NormalizeEmail(raw)
			test.ErrorIs(t, err, ErrInvalidEmail)
			test.ErrorIs(t, err, ErrInvalidContact)
		})
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	t.Run("a household needs a name and a language", func(t *testing.T) {
		t.Parallel()

		test.NoError(t, (&Household{StudioID: "s", Name: "García", Language: Spanish}).Validate())
		test.ErrorIs(t, (&Household{StudioID: "s", Name: "  ", Language: Spanish}).Validate(), ErrInvalidHousehold)
		test.ErrorIs(t, (&Household{StudioID: "s", Name: "García", Language: "fr"}).Validate(), ErrInvalidHousehold)
	})

	t.Run("a contact needs an email or a phone, and a relationship", func(t *testing.T) {
		t.Parallel()

		valid := Contact{StudioID: "s", HouseholdID: "h", Name: "Lucía", Phone: "512 555 0100", Language: Spanish, Relationship: Guardian}
		test.NoError(t, valid.Validate())

		emailOnly := valid
		emailOnly.Phone = ""
		emailOnly.Email = "lucia@example.com"
		test.NoError(t, emailOnly.Validate())

		both := valid
		both.Email = "lucia@example.com"
		test.NoError(t, both.Validate())

		neither := valid
		neither.Phone = "  "
		test.ErrorIs(t, neither.Validate(), ErrUnreachable)
		test.ErrorIs(t, neither.Validate(), ErrInvalidContact)

		badEmail := valid
		badEmail.Email = "lucia at example"
		test.ErrorIs(t, badEmail.Validate(), ErrInvalidEmail)

		unknown := valid
		unknown.Relationship = "aunt"
		test.ErrorIs(t, unknown.Validate(), ErrInvalidContact)
	})

	t.Run("an attendee needs a household", func(t *testing.T) {
		t.Parallel()

		test.NoError(t, (&Attendee{StudioID: "s", HouseholdID: "h", Name: "Sofia", Language: English}).Validate())
		test.ErrorIs(t, (&Attendee{StudioID: "s", Name: "Sofia", Language: English}).Validate(), ErrInvalidAttendee)
	})
}
