package messaging

import (
	"strings"
	"testing"
	"time"

	"github.com/primandproper/fijo/internal/domain/roster"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestRender(t *testing.T) {
	t.Parallel()

	central, err := time.LoadLocation("US/Central")
	must.NoError(t, err)

	// Monday the ninth of November, ten in the morning.
	now := time.Date(2026, time.November, 9, 10, 0, 0, 0, central)
	tomorrow := time.Date(2026, time.November, 10, 16, 0, 0, 0, central)

	lesson := func(language roster.Language, startsAt time.Time, others ...string) *Lesson {
		return &Lesson{
			StartsAt:     startsAt,
			Now:          now,
			StudioName:   "Casa de Musica",
			ContactName:  "Lucía García",
			OfferingName: "Piano 30",
			ReplyTo:      "maria@example.com",
			Language:     language,
			Others:       others,
		}
	}

	t.Run("a parent, in English, the day before", func(t *testing.T) {
		t.Parallel()

		r := Render(lesson(roster.English, tomorrow, "Sofía García"))

		test.Eq(t, "Reminder: Sofía García, Piano 30, tomorrow at 4:00 PM", r.Subject)
		test.StrContains(t, r.HTML, "Hi Lucía,")
		test.StrContains(t, r.HTML, "This is a reminder that Sofía García has Piano 30 with Casa de Musica tomorrow, Tuesday, November 10, at 4:00 PM.")
		test.StrContains(t, r.HTML, `<a href="mailto:maria@example.com">maria@example.com</a>`)
		test.StrContains(t, r.HTML, `lang="en"`)
	})

	t.Run("a parent, in Spanish, the day before", func(t *testing.T) {
		t.Parallel()

		r := Render(lesson(roster.Spanish, tomorrow, "Sofía García"))

		test.Eq(t, "Recordatorio: Sofía García, Piano 30, mañana a las 4:00 p. m.", r.Subject)
		test.StrContains(t, r.HTML, "Hola, Lucía:")
		test.StrContains(t, r.HTML, "Le recordamos que Sofía García tiene Piano 30 con Casa de Musica mañana, martes 10 de noviembre, a las 4:00 p. m.")
		test.StrContains(t, r.HTML, "Para cambiar o cancelar, escriba a")
		test.StrContains(t, r.HTML, `lang="es"`)
	})

	t.Run("an adult student, about their own lesson", func(t *testing.T) {
		t.Parallel()

		today := time.Date(2026, time.November, 9, 13, 30, 0, 0, central)

		en := Render(lesson(roster.English, today))
		test.Eq(t, "Reminder: Piano 30, today at 1:30 PM", en.Subject)
		test.StrContains(t, en.HTML, "This is a reminder that you have Piano 30 with Casa de Musica today, Monday, November 9, at 1:30 PM.")

		es := Render(lesson(roster.Spanish, today))
		test.Eq(t, "Recordatorio: Piano 30, hoy a la 1:30 p. m.", es.Subject, test.Sprint("one o'clock is singular"))
		test.StrContains(t, es.HTML, "Le recordamos que tiene Piano 30 con Casa de Musica hoy, lunes 9 de noviembre, a la 1:30 p. m.")
	})

	t.Run("two children, a week out", func(t *testing.T) {
		t.Parallel()

		later := time.Date(2026, time.November, 14, 9, 0, 0, 0, central)

		en := Render(lesson(roster.English, later, "Mateo García", "Sofía García"))
		test.Eq(t, "Reminder: Mateo García and Sofía García, Piano 30, Saturday, November 14 at 9:00 AM", en.Subject)
		test.StrContains(t, en.HTML, "Mateo García and Sofía García have Piano 30 with Casa de Musica on Saturday, November 14, at 9:00 AM.")

		es := Render(lesson(roster.Spanish, later, "Mateo García", "Sofía García"))
		test.StrContains(t, es.HTML, "Mateo García y Sofía García tienen Piano 30 con Casa de Musica el sábado 14 de noviembre a las 9:00 a. m.")
	})

	t.Run("a parent who takes the lesson with their child", func(t *testing.T) {
		t.Parallel()

		l := lesson(roster.English, tomorrow, "Sofía García")
		l.ContactAttends = true

		test.StrContains(t, Render(l).HTML, "you and Sofía García have Piano 30")

		l.Language = roster.Spanish
		test.StrContains(t, Render(l).HTML, "usted y Sofía García tienen Piano 30")
	})

	t.Run("a name the import guessed is greeted whole", func(t *testing.T) {
		t.Parallel()

		l := lesson(roster.English, tomorrow, "Sofía Pérez")
		l.ContactName = "Pérez family"

		test.StrContains(t, Render(l).HTML, "Hi Pérez family,")
	})

	t.Run("names are escaped in the body and left alone in the subject", func(t *testing.T) {
		t.Parallel()

		l := lesson(roster.English, tomorrow, "<b>Sofía</b>")
		l.StudioName = "Smith & Daughters"

		r := Render(l)

		test.StrContains(t, r.Subject, "<b>Sofía</b>")
		test.False(t, strings.Contains(r.HTML, "<b>Sofía</b>"))
		test.StrContains(t, r.HTML, "&lt;b&gt;Sofía&lt;/b&gt;")
		test.StrContains(t, r.HTML, "Smith &amp; Daughters")
	})

	t.Run("midnight and noon", func(t *testing.T) {
		t.Parallel()

		noon := time.Date(2026, time.November, 10, 12, 0, 0, 0, central)
		test.StrContains(t, Render(lesson(roster.English, noon)).Subject, "tomorrow at 12:00 PM")
		test.StrContains(t, Render(lesson(roster.Spanish, noon)).Subject, "mañana a las 12:00 p. m.")

		midnight := time.Date(2026, time.November, 11, 0, 15, 0, 0, central)
		test.StrContains(t, Render(lesson(roster.Spanish, midnight)).Subject, "a las 12:15 a. m.")
	})
}

func TestGreetingName(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{
		"Lucía García":   "Lucía",
		"Ana":            "Ana",
		"Pérez family":   "Pérez family",
		"familia Pérez":  "familia Pérez",
		"  Wei   Chen  ": "Wei",
		"":               "",
	} {
		test.Eq(t, want, greetingName(name), test.Sprintf("%q", name))
	}
}
