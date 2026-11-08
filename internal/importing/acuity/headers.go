package acuity

import (
	"strings"
	"unicode"
)

// The headers are a guess. Acuity's help center describes its exports in prose
// ("client first name", "start time and date", "appointment type") and third
// parties quote shorter headers from older exports ("First Name", "Start Time",
// "Type"), and nobody has handed Fijo a real file yet. So a field is found by
// any of several spellings, compared with case, spaces and punctuation
// ignored, and the plan reports which header it read as what. When a real
// export arrives, this table is the one place to correct.

// field is a thing the importer reads out of a row.
type field string

// The fields.
const (
	fieldName          field = "name"
	fieldFirstName     field = "first name"
	fieldLastName      field = "last name"
	fieldEmail         field = "email"
	fieldPhone         field = "phone"
	fieldNotes         field = "notes"
	fieldStart         field = "start"
	fieldDate          field = "date"
	fieldTime          field = "time"
	fieldEnd           field = "end"
	fieldDuration      field = "duration"
	fieldType          field = "appointment type"
	fieldPrice         field = "price"
	fieldCanceled      field = "canceled"
	fieldAppointmentID field = "appointment id"
)

// aliases are the spellings each field is looked for under, best first.
var aliases = map[field][]string{
	fieldName:          {"Client Name", "Name", "Full Name", "Client"},
	fieldFirstName:     {"First Name", "Client First Name", "First"},
	fieldLastName:      {"Last Name", "Client Last Name", "Last", "Surname"},
	fieldEmail:         {"Email", "Client Email", "Client Email Address", "Email Address", "E-mail"},
	fieldPhone:         {"Phone", "Client Phone", "Client Phone Number", "Phone Number", "Mobile", "Mobile Phone", "Cell", "Cell Phone"},
	fieldNotes:         {"Notes", "Notes on the Client File", "Client Notes", "File Notes"},
	fieldStart:         {"Start Time", "Start Time and Date", "Start Date and Time", "Start Date/Time", "Start", "Date and Time", "Appointment Date/Time", "Datetime"},
	fieldDate:          {"Date", "Appointment Date", "Start Date"},
	fieldTime:          {"Time", "Appointment Time"},
	fieldEnd:           {"End Time", "End Time and Date", "End Date and Time", "End Date/Time", "End"},
	fieldDuration:      {"Duration", "Duration (minutes)", "Minutes", "Length"},
	fieldType:          {"Type", "Appointment Type", "Service", "Appointment"},
	fieldPrice:         {"Appointment Price", "Price", "Amount"},
	fieldCanceled:      {"Canceled", "Cancelled", "Canceled?", "Status"},
	fieldAppointmentID: {"Appointment ID", "ID", "Appointment Id"},
}

// clientFields and appointmentFields are what each file is read for, in the
// order a header is claimed: a header goes to the first field that wants it.
var (
	clientFields = []field{
		fieldFirstName, fieldLastName, fieldName, fieldEmail, fieldPhone, fieldNotes,
	}
	appointmentFields = []field{
		fieldStart, fieldEnd, fieldDate, fieldTime, fieldDuration, fieldFirstName, fieldLastName, fieldName,
		fieldEmail, fieldPhone, fieldType, fieldPrice, fieldCanceled, fieldAppointmentID, fieldNotes,
	}
)

// normalizeHeader is a header with case, spaces and punctuation dropped, so
// "Client E-mail" and "client email" are one header.
func normalizeHeader(header string) string {
	var b strings.Builder

	for _, r := range strings.ToLower(header) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}

	return b.String()
}

// columns is which column of a file each field was found in.
type columns struct {
	index   map[field]int
	headers []string
}

// matchColumns finds each wanted field's column, and reports the headers no
// field claimed (an intake form's questions, Acuity's payment columns).
func matchColumns(headers []string, wanted []field) (found *columns, unread []string) {
	byName := map[string]int{}

	for i, header := range headers {
		key := normalizeHeader(header)
		if _, seen := byName[key]; !seen && key != "" {
			byName[key] = i
		}
	}

	found = &columns{index: map[field]int{}, headers: headers}
	claimed := map[int]bool{}

	for _, f := range wanted {
		for _, alias := range aliases[f] {
			i, ok := byName[normalizeHeader(alias)]
			if ok && !claimed[i] {
				found.index[f] = i
				claimed[i] = true

				break
			}
		}
	}

	for i, header := range headers {
		if !claimed[i] && strings.TrimSpace(header) != "" {
			unread = append(unread, strings.TrimSpace(header))
		}
	}

	return found, unread
}

// has reports whether the file has the field.
func (c *columns) has(f field) bool {
	_, ok := c.index[f]

	return ok
}

// header is the file's own spelling of the field, or empty.
func (c *columns) header(f field) string {
	if i, ok := c.index[f]; ok {
		return strings.TrimSpace(c.headers[i])
	}

	return ""
}

// get is the field's value in a row, trimmed, or empty.
func (c *columns) get(row []string, f field) string {
	i, ok := c.index[f]
	if !ok || i >= len(row) {
		return ""
	}

	return strings.TrimSpace(row[i])
}
