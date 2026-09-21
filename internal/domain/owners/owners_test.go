package owners

import (
	"testing"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestLoadTimeZone(t *testing.T) {
	t.Parallel()

	t.Run("loads an IANA zone", func(t *testing.T) {
		t.Parallel()

		loc, err := LoadTimeZone("America/Chicago")
		must.NoError(t, err)
		test.Eq(t, "America/Chicago", loc.String())
	})

	t.Run("refuses Local, empty and nonsense", func(t *testing.T) {
		t.Parallel()

		for _, name := range []string{"Local", "", "Mars/Olympus_Mons"} {
			_, err := LoadTimeZone(name)
			must.ErrorIs(t, err, ErrInvalidTimeZone, must.Sprintf("zone %q", name))
		}
	})
}

func TestPreferencesLocation(t *testing.T) {
	t.Parallel()

	var none *Preferences

	loc, err := none.Location()
	must.NoError(t, err)
	test.Eq(t, "UTC", loc.String())

	loc, err = (&Preferences{TimeZone: "US/Central"}).Location()
	must.NoError(t, err)
	test.Eq(t, "US/Central", loc.String())
}
