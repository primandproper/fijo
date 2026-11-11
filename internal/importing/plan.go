// Package importing writes a studio's clients, standing lessons and one-off
// appointments that came from another system. A source (acuity, today) reads
// its own files into a Plan; this package says which of the plan is already
// on the roster and writes the rest on one transaction.
//
// Running an import again is how a studio switches: a trial run while the old
// system is still booking, and a last one on the day it stops. So a client
// already here is matched (by any of their emails or phones) rather than
// written twice, and only what they are missing is added: a person by a name
// the household does not have, a contact whose email and phone are both new,
// a standing lesson at a weekday and time the household has none, an
// appointment at a moment it has none. Nothing already here is changed.
//
// A plan is recomputed from the files every time rather than stored between
// the preview and the write: the owner looks at a preview, unticks what is
// wrong, and the write reads the same files again with those keys skipped.
// So every key in a plan must come out the same from the same files.
package importing

import (
	"time"

	"github.com/primandproper/fijo/internal/domain/roster"
	"github.com/primandproper/fijo/internal/domain/schedule"
)

// Plan is what an import would write.
type Plan struct {
	// Columns says which header in which file was read as which field, and
	// which fields no header matched. It is how the owner (and the next
	// person to fix the header table) sees what the importer understood.
	Columns []*Column

	Households []*Household
	Offerings  []*Offering
	Lessons    []*Lesson

	// Appointments are what the plan does not read as a standing lesson,
	// each written as a one-off with the reason it is not one.
	Appointments []*Appointment

	// Warnings are about the files as a whole: rows dropped, columns not
	// read, counts of what was left out.
	Warnings []string
}

// Column is one field the source looks for.
type Column struct {
	File  string
	Field string

	// Header is the file's header that was read as Field, or empty when no
	// header matched.
	Header string
}

// Household is a client the plan would add.
type Household struct {
	Key      string
	Name     string
	Language roster.Language
	Notes    string

	// ExistingID and ExistingName are set by Annotate when one of the
	// contacts' emails or phones is already on a live contact: a re-run of an
	// import, or a client the owner already typed in. Such a household is not
	// written again; what it is missing is added to the one already here.
	ExistingID   string
	ExistingName string

	Contacts  []*Contact
	Attendees []*Attendee
	Warnings  []string

	// Active is whether anybody on the household has an appointment ahead.
	// A client list holds everybody who ever booked; an import leaves the
	// inactive ones out unless asked.
	Active bool
}

// OnRoster reports whether the household is one the studio already has.
func (h *Household) OnRoster() bool { return h != nil && h.ExistingID != "" }

// Contact is a person on a planned household who would get the reminders.
type Contact struct {
	Name         string
	Email        string
	Phone        string
	Relationship roster.Relationship

	// AttendeeKey names the household's attendee who is this same person,
	// for an adult who takes the lessons.
	AttendeeKey string

	// ExistingID is set by Annotate when the email or the phone is already on
	// a live contact, which is then not written.
	ExistingID string

	// GuessedName is set when the source had no name for whoever owns the
	// email or phone (a parent who booked under the children's names), so
	// the plan made one up for the owner to correct.
	GuessedName bool
}

// OnRoster reports whether the contact is one the studio already has.
func (c *Contact) OnRoster() bool { return c != nil && c.ExistingID != "" }

// Attendee is a person on a planned household who takes lessons.
type Attendee struct {
	Key  string
	Name string

	// ExistingID is set by Annotate when the household is already here and
	// has this person: somebody by the same name, or the attendee who is the
	// contact this person is.
	ExistingID string
}

// OnRoster reports whether the attendee is one the studio already has.
func (a *Attendee) OnRoster() bool { return a != nil && a.ExistingID != "" }

// Offering is what a planned lesson is sold as.
type Offering struct {
	Key  string
	Name string

	// ExistingID is set by Annotate when the studio already sells something
	// by this name, which the lessons then use instead of a new one.
	ExistingID string

	Duration  time.Duration
	ListPrice int64
}

// Lesson is a standing slot the plan would add.
type Lesson struct {
	Key           string
	HouseholdKey  string
	AttendeeKey   string
	AttendeeName  string
	OfferingKey   string
	Warnings      []string
	StartsOn      schedule.Date
	StartTime     schedule.ClockTime
	Weekday       time.Weekday
	IntervalWeeks int

	// Price is what the source charged for each of these lessons in the
	// smallest currency unit, or -1 when it did not say. A price unlike the
	// offering's list price becomes the slot's override.
	Price int64

	// Seen is how many upcoming appointments the lesson was read from.
	Seen int

	// OnRoster is set by Annotate when the household already has a standing
	// lesson, not ended before this one starts, on the same weekday at the
	// same time.
	OnRoster bool
}

// Appointment is one appointment that is not part of a standing lesson,
// which the plan would book as a one-off.
type Appointment struct {
	Key          string
	HouseholdKey string
	AttendeeKey  string
	AttendeeName string
	OfferingKey  string

	// Reason is why it is not a standing lesson.
	Reason string

	On        schedule.Date
	StartTime schedule.ClockTime

	// Duration is how long the source said it runs, in whole minutes, or
	// zero for the offering's length.
	Duration time.Duration

	// OnRoster is set by Annotate when the household already has a lesson,
	// in any state, starting at that moment.
	OnRoster bool
}
