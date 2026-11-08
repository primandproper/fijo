package acuity

import (
	"encoding/csv"
	"errors"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/primandproper/fijo/internal/domain/schedule"

	platformerrors "github.com/primandproper/primitives-go/v2/errors"
)

// ErrUnreadable is a file the importer cannot use at all: not CSV, no rows, or
// no column it can read a person's name from. What it wraps is a sentence for
// the owner saying which.
var ErrUnreadable = platformerrors.Wrap(platformerrors.ErrUnrecognizedInputValue, "unreadable export")

// record is one row of either file: somebody, how to reach them, and for an
// appointment, when and what.
//
// kind, id, date, start, price, duration, canceled and scheduled are an
// appointment's, and zero on a client list's row. scheduled is whether the
// row's time could be read; line is the row's line in its file.
type record struct {
	kind      string
	last      string
	email     string
	phone     string
	notes     string
	name      string
	id        string
	date      schedule.Date
	start     schedule.ClockTime
	price     int64
	duration  time.Duration
	line      int
	canceled  bool
	scheduled bool
}

// byteOrderMark is what Excel writes at the head of a UTF-8 file.
const byteOrderMark = "\uFEFF"

// readCSV splits a file into its header and rows. Excel's byte-order mark is
// dropped, rows may be ragged, and a stray quote does not fail the file. file
// is how a refusal names it to the owner.
func readCSV(file, text string) (header []string, body [][]string, err error) {
	text = strings.TrimPrefix(text, byteOrderMark)

	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	var rows [][]string

	for {
		row, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}

		if readErr != nil {
			return nil, nil, platformerrors.Wrapf(ErrUnreadable, "%s is not a CSV file we can read (%v)", file, readErr)
		}

		if blank(row) {
			continue
		}

		rows = append(rows, row)
	}

	if len(rows) == 0 {
		return nil, nil, platformerrors.Wrapf(ErrUnreadable, "%s is empty", file)
	}

	return rows[0], rows[1:], nil
}

func blank(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}

	return true
}

// nameOf is a row's person: "First Last" when the file splits the name, the
// whole-name column otherwise. The last name is the split column's when there
// is one and the last word of the whole name when not.
func nameOf(c *columns, row []string) (name, last string) {
	first, last := c.get(row, fieldFirstName), c.get(row, fieldLastName)
	name = strings.Join(strings.Fields(first+" "+last), " ")

	if name == "" {
		name = strings.Join(strings.Fields(c.get(row, fieldName)), " ")
		last = ""
	}

	if last == "" {
		if words := strings.Fields(name); len(words) > 1 {
			last = words[len(words)-1]
		}
	}

	return name, last
}

// hasName reports whether a file has a column a name can come from.
func hasName(c *columns) bool {
	return c.has(fieldName) || c.has(fieldFirstName) || c.has(fieldLastName)
}

// The layouts a date and time are tried in. Acuity's own format is not known
// yet; these are what it and spreadsheets re-saving it plausibly write. A
// value is upper-cased and has a space put before AM/PM first, and Go's month
// names match without regard to case.
var (
	dateTimeLayouts = []string{
		"January 2, 2006 3:04 PM",
		"January 2, 2006 15:04",
		"January 2 2006 3:04 PM",
		"Jan 2, 2006 3:04 PM",
		"Jan 2, 2006 15:04",
		"Monday, January 2, 2006 3:04 PM",
		"Monday, January 2, 2006 15:04",
		"Mon, Jan 2, 2006 3:04 PM",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02 3:04 PM",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"1/2/2006 3:04 PM",
		"1/2/2006 3:04:05 PM",
		"1/2/2006 15:04",
		"1/2/2006 15:04:05",
		"1/2/06 3:04 PM",
		"1/2/06 15:04",
	}
	offsetLayouts = []string{time.RFC3339, "2006-01-02T15:04:05Z0700", "2006-01-02 15:04:05 -0700", "2006-01-02 15:04:05-07:00"}
	dateLayouts   = []string{"January 2, 2006", "January 2 2006", "Jan 2, 2006", "Monday, January 2, 2006", "Mon, Jan 2, 2006", "2006-01-02", "1/2/2006", "1/2/06"}
	timeLayouts   = []string{"3:04 PM", "3:04:05 PM", "3 PM", "15:04", "15:04:05"}

	meridiem = regexp.MustCompile(`(\d)(AM|PM)\b`)
)

func tidyClock(value string) string {
	value = strings.Join(strings.Fields(strings.ToUpper(value)), " ")
	value = strings.ReplaceAll(value, "A.M.", "AM")
	value = strings.ReplaceAll(value, "P.M.", "PM")

	return meridiem.ReplaceAllString(value, "$1 $2")
}

func parseIn(value string, layouts []string) (time.Time, bool) {
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t, true
		}
	}

	return time.Time{}, false
}

// parseDateTime reads a wall clock: the date and time as written, in whatever
// zone the export was written in, which is taken to be the studio's. An
// offset, when a value carries one, is ignored for the same reason: a standing
// lesson is a wall clock, not an instant.
func parseDateTime(value string) (schedule.Date, schedule.ClockTime, bool) {
	value = tidyClock(value)
	if value == "" {
		return schedule.Date{}, schedule.ClockTime{}, false
	}

	t, ok := parseIn(value, dateTimeLayouts)
	if !ok {
		t, ok = parseIn(value, offsetLayouts)
	}

	if !ok {
		return schedule.Date{}, schedule.ClockTime{}, false
	}

	return schedule.Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}, schedule.ClockTime{Hour: t.Hour(), Minute: t.Minute()}, true
}

func parseDate(value string) (schedule.Date, bool) {
	t, ok := parseIn(tidyClock(value), dateLayouts)
	if !ok {
		return schedule.Date{}, false
	}

	return schedule.Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}, true
}

func parseClock(value string) (schedule.ClockTime, bool) {
	t, ok := parseIn(tidyClock(value), timeLayouts)
	if !ok {
		return schedule.ClockTime{}, false
	}

	return schedule.ClockTime{Hour: t.Hour(), Minute: t.Minute()}, true
}

// parsePrice reads "$45.00", "45" or "1,200.50" as the smallest currency
// unit, or -1 for a blank or anything else.
func parsePrice(value string) int64 {
	cleaned := strings.NewReplacer("$", "", ",", "", " ", "", "USD", "", "usd", "").Replace(value)
	if cleaned == "" {
		return -1
	}

	amount, err := strconv.ParseFloat(cleaned, 64)
	if err != nil || amount < 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return -1
	}

	return int64(math.Round(amount * 100))
}

// parseMinutes reads a duration column: "30", "30 minutes", "1:30" or "1.5
// hours" would all be plausible, and the first two are what is handled.
func parseMinutes(value string) time.Duration {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return 0
	}

	minutes, err := strconv.Atoi(fields[0])
	if err != nil || minutes <= 0 {
		return 0
	}

	return time.Duration(minutes) * time.Minute
}

// isCanceled reads a canceled column, or a status column that says so.
func isCanceled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "yes", "y", "true", "1", "x", "canceled", "cancelled":
		return true
	default:
		return false
	}
}

// parseClients reads the client list.
func parseClients(text string) ([]*record, *columns, []string, error) {
	headers, rows, err := readCSV("The client list", text)
	if err != nil {
		return nil, nil, nil, err
	}

	c, unread := matchColumns(headers, clientFields)
	if !hasName(c) {
		return nil, nil, nil, platformerrors.Wrapf(ErrUnreadable, "The client list has no name column we recognize. Its columns are %s", strings.Join(headers, ", "))
	}

	records := make([]*record, 0, len(rows))

	for i, row := range rows {
		name, last := nameOf(c, row)
		if name == "" {
			continue
		}

		records = append(records, &record{
			name:  name,
			last:  last,
			email: c.get(row, fieldEmail),
			phone: c.get(row, fieldPhone),
			notes: c.get(row, fieldNotes),
			line:  i + 2,
		})
	}

	return records, c, unread, nil
}

// parseAppointments reads the appointments report. A row whose time cannot be
// read is kept for its person and reach, without a date, and counted.
func parseAppointments(text string) (records []*record, c *columns, unread []string, unparsed int, err error) {
	headers, rows, err := readCSV("The appointments report", text)
	if err != nil {
		return nil, nil, nil, 0, err
	}

	c, unread = matchColumns(headers, appointmentFields)

	switch {
	case !hasName(c):
		return nil, nil, nil, 0, platformerrors.Wrapf(ErrUnreadable, "The appointments report has no name column we recognize. Its columns are %s", strings.Join(headers, ", "))
	case !c.has(fieldStart) && !c.has(fieldDate):
		return nil, nil, nil, 0, platformerrors.Wrapf(ErrUnreadable, "The appointments report has no start time column we recognize. Its columns are %s", strings.Join(headers, ", "))
	}

	records = make([]*record, 0, len(rows))

	for i, row := range rows {
		name, last := nameOf(c, row)
		if name == "" {
			continue
		}

		r := &record{
			name:     name,
			last:     last,
			email:    c.get(row, fieldEmail),
			phone:    c.get(row, fieldPhone),
			kind:     strings.Join(strings.Fields(c.get(row, fieldType)), " "),
			price:    parsePrice(c.get(row, fieldPrice)),
			canceled: isCanceled(c.get(row, fieldCanceled)),
			id:       c.get(row, fieldAppointmentID),
			line:     i + 2,
		}

		r.scheduled = readWhen(c, row, r)
		if !r.scheduled {
			unparsed++
		}

		records = append(records, r)
	}

	return records, c, unread, unparsed, nil
}

// readWhen fills an appointment's date, start and length from whichever of
// the time columns the file has.
func readWhen(c *columns, row []string, r *record) bool {
	var ok bool

	if c.has(fieldStart) {
		r.date, r.start, ok = parseDateTime(c.get(row, fieldStart))
	}

	if !ok && c.has(fieldDate) {
		// A date column on its own, with the time beside it or inside it.
		if r.date, r.start, ok = parseDateTime(c.get(row, fieldDate) + " " + c.get(row, fieldTime)); !ok {
			var clockOK bool

			r.date, ok = parseDate(c.get(row, fieldDate))
			r.start, clockOK = parseClock(c.get(row, fieldTime))
			ok = ok && clockOK
		}
	}

	if !ok {
		return false
	}

	if end := c.get(row, fieldEnd); end != "" {
		endDate, endClock, endOK := parseDateTime(end)
		if !endOK {
			endDate = r.date
			endClock, endOK = parseClock(end)
		}

		if endOK {
			length := endDate.At(endClock, time.UTC).Sub(r.date.At(r.start, time.UTC))
			if length > 0 && length < 24*time.Hour {
				r.duration = length
			}
		}
	}

	if r.duration == 0 {
		r.duration = parseMinutes(c.get(row, fieldDuration))
	}

	return true
}
