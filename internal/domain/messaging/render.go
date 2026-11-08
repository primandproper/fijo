package messaging

import (
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/primandproper/fijo/internal/domain/roster"
)

// Lesson is everything a reminder is rendered from.
type Lesson struct {
	// StartsAt and Now are read in the studio's zone, which is the zone the
	// reminder speaks in: "tomorrow at 4:00 PM" is the studio's tomorrow.
	StartsAt time.Time
	Now      time.Time

	StudioName   string
	ContactName  string
	OfferingName string
	ReplyTo      string
	Language     roster.Language

	// Others are the household's attendees on the lesson other than the
	// contact; ContactAttends is the contact being one too.
	Others         []string
	ContactAttends bool
}

// Rendered is a reminder's subject and body.
type Rendered struct {
	Subject string
	HTML    string
}

// Render writes the reminder in the contact's language.
//
// The words are deliberately few: who, what, when, and where to write to
// change it. "Where to write" is the owner's address in the body as well as
// the Reply-To, so a client who forwards it, or writes fresh rather than
// replying, still knows who to tell.
func Render(l *Lesson) Rendered {
	if l.Language == roster.Spanish {
		return renderES(l)
	}

	return renderEN(l)
}

func renderEN(l *Lesson) Rendered {
	when := whenEN(l.StartsAt, l.Now)
	e := escaped(l)
	who, verb := whoEN(e.Others, e.ContactAttends)

	subject := fmt.Sprintf("Reminder: %s, %s", l.OfferingName, when)
	if len(l.Others) > 0 {
		subject = fmt.Sprintf("Reminder: %s, %s, %s", joinEN(l.Others), l.OfferingName, when)
	}

	body := &strings.Builder{}
	paragraph(body, "Hi "+greetingName(e.ContactName)+",")
	paragraph(body, fmt.Sprintf("This is a reminder that %s %s %s with %s %s.",
		who, verb, e.OfferingName, e.StudioName, fullWhenEN(l.StartsAt, l.Now)))
	paragraph(body, "To change or cancel, write to "+mailto(l.ReplyTo)+", or reply to this email.")
	paragraph(body, "— "+e.StudioName)

	return Rendered{Subject: subject, HTML: wrap(body.String(), "en")}
}

func renderES(l *Lesson) Rendered {
	when := whenES(l.StartsAt, l.Now)
	e := escaped(l)
	who, verb := whoES(e.Others, e.ContactAttends)

	subject := fmt.Sprintf("Recordatorio: %s, %s", l.OfferingName, when)
	if len(l.Others) > 0 {
		subject = fmt.Sprintf("Recordatorio: %s, %s, %s", joinES(l.Others), l.OfferingName, when)
	}

	body := &strings.Builder{}
	paragraph(body, "Hola, "+greetingName(e.ContactName)+":")

	sentence := fmt.Sprintf("Le recordamos que %s %s %s con %s %s.", who, verb, e.OfferingName, e.StudioName, fullWhenES(l.StartsAt, l.Now))
	if who == "" {
		sentence = fmt.Sprintf("Le recordamos que %s %s con %s %s.", verb, e.OfferingName, e.StudioName, fullWhenES(l.StartsAt, l.Now))
	}

	paragraph(body, sentence)
	paragraph(body, "Para cambiar o cancelar, escriba a "+mailto(l.ReplyTo)+" o responda a este correo.")
	paragraph(body, "— "+e.StudioName)

	return Rendered{Subject: subject, HTML: wrap(body.String(), "es")}
}

// escaped is the lesson with every name in it made safe for markup. The
// subject is plain text and is rendered from the lesson as it is.
func escaped(l *Lesson) *Lesson {
	e := *l
	e.StudioName = html.EscapeString(l.StudioName)
	e.ContactName = html.EscapeString(l.ContactName)
	e.OfferingName = html.EscapeString(l.OfferingName)
	e.Others = make([]string, 0, len(l.Others))

	for _, name := range l.Others {
		e.Others = append(e.Others, html.EscapeString(name))
	}

	return &e
}

// verbHave is the plural of what a person does with a lesson.
const verbHave = "have"

// whoEN is who has the lesson, and the verb that agrees with it.
func whoEN(others []string, contactAttends bool) (who, verb string) {
	switch {
	case len(others) == 0:
		return "you", verbHave
	case contactAttends:
		return "you and " + joinEN(others), verbHave
	case len(others) == 1:
		return others[0], "has"
	default:
		return joinEN(others), verbHave
	}
}

// whoES is who has the lesson, and the verb that agrees with it. Spanish
// drops the subject for "usted", so the first case has none.
func whoES(others []string, contactAttends bool) (who, verb string) {
	switch {
	case len(others) == 0:
		return "", "tiene"
	case contactAttends:
		return "usted y " + joinES(others), "tienen"
	case len(others) == 1:
		return others[0], "tiene"
	default:
		return joinES(others), "tienen"
	}
}

func joinEN(names []string) string {
	return join(names, "and")
}

func joinES(names []string) string {
	return join(names, "y")
}

func join(names []string, and string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " " + and + " " + names[len(names)-1]
	}
}

// daysAhead is how many of the studio's midnights lie between now and the
// lesson: zero for today, one for tomorrow.
func daysAhead(startsAt, now time.Time) int {
	lesson := time.Date(startsAt.Year(), startsAt.Month(), startsAt.Day(), 0, 0, 0, 0, time.UTC)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	return int(lesson.Sub(today).Hours() / 24)
}

// whenEN is the short form, for a subject: "tomorrow at 4:00 PM".
func whenEN(startsAt, now time.Time) string {
	switch daysAhead(startsAt, now) {
	case 0:
		return "today at " + clockEN(startsAt)
	case 1:
		return "tomorrow at " + clockEN(startsAt)
	default:
		return startsAt.Format("Monday, January 2") + " at " + clockEN(startsAt)
	}
}

// fullWhenEN always carries the date, for the body: "tomorrow, Tuesday,
// November 10, at 4:00 PM".
func fullWhenEN(startsAt, now time.Time) string {
	date := startsAt.Format("Monday, January 2")

	switch daysAhead(startsAt, now) {
	case 0:
		return "today, " + date + ", at " + clockEN(startsAt)
	case 1:
		return "tomorrow, " + date + ", at " + clockEN(startsAt)
	default:
		return "on " + date + ", at " + clockEN(startsAt)
	}
}

func clockEN(t time.Time) string {
	return t.Format("3:04 PM")
}

var (
	weekdaysES = [...]string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}
	monthsES   = [...]string{
		"enero", "febrero", "marzo", "abril", "mayo", "junio",
		"julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre",
	}
)

func dateES(t time.Time) string {
	return fmt.Sprintf("%s %d de %s", weekdaysES[t.Weekday()], t.Day(), monthsES[t.Month()-1])
}

// atES is "a las 4:00 p. m.", or "a la 1:00 p. m.": one o'clock is singular.
func atES(t time.Time) string {
	hour := t.Hour() % 12
	if hour == 0 {
		hour = 12
	}

	meridiem := "a. m."
	if t.Hour() >= 12 {
		meridiem = "p. m."
	}

	article := "las"
	if hour == 1 {
		article = "la"
	}

	return fmt.Sprintf("a %s %d:%02d %s", article, hour, t.Minute(), meridiem)
}

func whenES(startsAt, now time.Time) string {
	switch daysAhead(startsAt, now) {
	case 0:
		return "hoy " + atES(startsAt)
	case 1:
		return "mañana " + atES(startsAt)
	default:
		return "el " + dateES(startsAt) + " " + atES(startsAt)
	}
}

func fullWhenES(startsAt, now time.Time) string {
	switch daysAhead(startsAt, now) {
	case 0:
		return "hoy, " + dateES(startsAt) + ", " + atES(startsAt)
	case 1:
		return "mañana, " + dateES(startsAt) + ", " + atES(startsAt)
	default:
		return "el " + dateES(startsAt) + " " + atES(startsAt)
	}
}

// greetingName is a contact's first name, or the whole of a name that is a
// family's ("García family", "familia García"), which the import writes for a
// parent Acuity never had a name for.
func greetingName(name string) string {
	fields := strings.Fields(name)

	switch {
	case len(fields) == 0:
		return ""
	case len(fields) > 1 && (strings.EqualFold(fields[len(fields)-1], "family") || strings.EqualFold(fields[0], "familia")):
		return strings.Join(fields, " ")
	default:
		return fields[0]
	}
}

func paragraph(b *strings.Builder, text string) {
	b.WriteString(`<p style="margin:0 0 1em">`)
	b.WriteString(text)
	b.WriteString("</p>\n")
}

// mailto is an address as a link. The address was validated when it was
// saved; it is escaped anyway, because it is going into markup.
func mailto(address string) string {
	escaped := html.EscapeString(address)

	return `<a href="mailto:` + escaped + `">` + escaped + `</a>`
}

func wrap(body, lang string) string {
	return `<!doctype html><html lang="` + lang + `"><body style="font-family:-apple-system,Segoe UI,Helvetica,Arial,sans-serif;font-size:16px;line-height:1.5;color:#1a1a1a">` +
		"\n" + body + "</body></html>"
}
