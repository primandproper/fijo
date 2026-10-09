// Package roster is who a studio teaches: households, the contacts on them who
// get the texts and pay, and the attendees who take the lessons.
//
// None of them are users. A client never has a password; the owner writes all
// of this, and a client's only door (in phase 3) is the phone number on a
// contact. A child is an attendee with no contact. An adult student is one
// person who is both: an attendee whose ContactID names their own contact.
//
// Guardianship is implicit. Every contact on a household is treated as a
// guardian of every attendee on it, until a case (separated parents with
// different children) needs a table that says otherwise.
package roster

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

// Language is which of Fijo's two languages a person reads.
type Language string

// The languages. Every client-facing string ships in both.
const (
	English Language = "en"
	Spanish Language = "es"
)

// Valid reports whether the language is one Fijo speaks.
func (l Language) Valid() bool {
	return l == English || l == Spanish
}

// Relationship is what a contact is to the household's attendees.
type Relationship string

// The relationships.
const (
	// Guardian is a parent or anybody else who speaks for the attendees.
	Guardian Relationship = "guardian"

	// Self is an adult who takes the lessons, and is also an attendee.
	Self Relationship = "self"
)

// Valid reports whether the relationship is one this package knows.
func (r Relationship) Valid() bool {
	return r == Guardian || r == Self
}

// The refusals.
var (
	ErrNilExecutor = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil query executor")
	ErrNilInput    = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil input")
	ErrEmptyID     = platformerrors.Wrap(platformerrors.ErrEmptyInputParameter, "empty identifier")

	// ErrNotFound is any row this package was asked for and does not have,
	// within the scope it was asked under.
	ErrNotFound = platformerrors.New("not found")

	// ErrNotStudioScoped is a read or a write handed the global scope.
	ErrNotStudioScoped = platformerrors.New("the roster is studio-scoped and the scope names no studio")

	// ErrInvalidHousehold, ErrInvalidContact and ErrInvalidAttendee are the
	// shapes this package refuses before the database sees them.
	ErrInvalidHousehold = platformerrors.Wrap(platformerrors.ErrUnrecognizedInputValue, "invalid household")
	ErrInvalidContact   = platformerrors.Wrap(platformerrors.ErrUnrecognizedInputValue, "invalid contact")
	ErrInvalidAttendee  = platformerrors.Wrap(platformerrors.ErrUnrecognizedInputValue, "invalid attendee")

	// ErrInvalidPhone is a number NormalizePhone could not make E.164 of.
	ErrInvalidPhone = platformerrors.Wrap(ErrInvalidContact, "not a phone number")

	// ErrPhoneTaken is a number already on another of the studio's live
	// contacts. An inbound text is filed by the number it came from, so a
	// number may belong to one contact at a time.
	ErrPhoneTaken = platformerrors.New("another contact already has that phone number")
)

// Household is the client account.
type Household struct {
	_ struct{} `json:"-"`

	CreatedAt     time.Time  `json:"createdAt"`
	LastUpdatedAt *time.Time `json:"lastUpdatedAt,omitempty"`
	ArchivedAt    *time.Time `json:"archivedAt,omitempty"`

	ID       string `json:"id"`
	StudioID string `json:"studioID"`

	// Name is how the owner finds them: "García" or "Ana Ruiz".
	Name string `json:"name"`

	// Language is what a contact or attendee added to the household starts
	// with.
	Language Language `json:"language"`

	Notes string `json:"notes"`

	// Contacts and Attendees are the live people on the household, filled by
	// the reads that say so.
	Contacts  []*Contact  `json:"contacts"`
	Attendees []*Attendee `json:"attendees"`
}

// Validate is the shape this package refuses before the database does.
func (h *Household) Validate() error {
	switch {
	case h == nil:
		return ErrNilInput
	case h.StudioID == "":
		return platformerrors.Wrap(ErrInvalidHousehold, "no studio")
	case strings.TrimSpace(h.Name) == "":
		return platformerrors.Wrap(ErrInvalidHousehold, "a household needs a name")
	case !h.Language.Valid():
		return platformerrors.Wrapf(ErrInvalidHousehold, "unknown language %q", h.Language)
	}

	return nil
}

// Contact is a person with a phone.
type Contact struct {
	_ struct{} `json:"-"`

	CreatedAt     time.Time  `json:"createdAt"`
	LastUpdatedAt *time.Time `json:"lastUpdatedAt,omitempty"`
	ArchivedAt    *time.Time `json:"archivedAt,omitempty"`
	VerifiedAt    *time.Time `json:"verifiedAt,omitempty"`

	ID          string `json:"id"`
	StudioID    string `json:"studioID"`
	HouseholdID string `json:"householdID"`
	Name        string `json:"name"`

	// Phone is E.164. A write normalizes what it is given first.
	Phone string `json:"phone"`

	Language     Language     `json:"language"`
	Relationship Relationship `json:"relationship"`

	// OptedOut is the carrier saying this number sent STOP. Nothing writes it
	// until the sms package exists.
	OptedOut bool `json:"optedOut"`
}

// Validate is the shape this package refuses before the database does. It
// expects a phone NormalizePhone has already been through.
func (c *Contact) Validate() error {
	switch {
	case c == nil:
		return ErrNilInput
	case c.StudioID == "":
		return platformerrors.Wrap(ErrInvalidContact, "no studio")
	case c.HouseholdID == "":
		return platformerrors.Wrap(ErrInvalidContact, "no household")
	case strings.TrimSpace(c.Name) == "":
		return platformerrors.Wrap(ErrInvalidContact, "a contact needs a name")
	case !c.Language.Valid():
		return platformerrors.Wrapf(ErrInvalidContact, "unknown language %q", c.Language)
	case !c.Relationship.Valid():
		return platformerrors.Wrapf(ErrInvalidContact, "unknown relationship %q", c.Relationship)
	}

	if _, err := NormalizePhone(c.Phone); err != nil {
		return err
	}

	return nil
}

// Attendee is a person who takes lessons.
type Attendee struct {
	_ struct{} `json:"-"`

	CreatedAt     time.Time  `json:"createdAt"`
	LastUpdatedAt *time.Time `json:"lastUpdatedAt,omitempty"`
	ArchivedAt    *time.Time `json:"archivedAt,omitempty"`

	ID          string   `json:"id"`
	StudioID    string   `json:"studioID"`
	HouseholdID string   `json:"householdID"`
	Name        string   `json:"name"`
	Language    Language `json:"language"`

	// ContactID is set for an adult who carries their own phone, and names
	// their contact on the same household.
	ContactID string `json:"contactID,omitempty"`
}

// Validate is the shape this package refuses before the database does.
func (a *Attendee) Validate() error {
	switch {
	case a == nil:
		return ErrNilInput
	case a.StudioID == "":
		return platformerrors.Wrap(ErrInvalidAttendee, "no studio")
	case a.HouseholdID == "":
		return platformerrors.Wrap(ErrInvalidAttendee, "no household")
	case strings.TrimSpace(a.Name) == "":
		return platformerrors.Wrap(ErrInvalidAttendee, "an attendee needs a name")
	case !a.Language.Valid():
		return platformerrors.Wrapf(ErrInvalidAttendee, "unknown language %q", a.Language)
	}

	return nil
}

// NormalizePhone turns what an owner typed into E.164.
//
// Punctuation and spaces are dropped. A number with no country code is read as
// North American when it has ten digits, or eleven starting with 1, because
// that is where Fijo's studios are; anything else has to say its country with
// a leading +.
func NormalizePhone(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	international := strings.HasPrefix(trimmed, "+")

	digits := make([]rune, 0, len(trimmed))

	for _, r := range trimmed {
		switch {
		case unicode.IsDigit(r):
			digits = append(digits, r)
		case r == '+' || r == ' ' || r == '-' || r == '.' || r == '(' || r == ')':
		default:
			return "", platformerrors.Wrapf(ErrInvalidPhone, "%q", raw)
		}
	}

	number := string(digits)

	switch {
	case international:
	case len(number) == 10:
		number = "1" + number
	case len(number) == 11 && number[0] == '1':
	default:
		return "", platformerrors.Wrapf(ErrInvalidPhone, "%q has no country code", raw)
	}

	if len(number) < 7 || len(number) > 15 || number[0] == '0' {
		return "", platformerrors.Wrapf(ErrInvalidPhone, "%q", raw)
	}

	return "+" + number, nil
}

// Store reads and writes the roster. Writes take a Tx and reads take an
// executor. Every method is scoped to a studio, and a row in another studio is
// ErrNotFound.
type Store interface {
	// CreateHousehold writes a household with nobody on it yet.
	CreateHousehold(ctx context.Context, tx database.Tx, scope tenancy.Scope, household *Household) (*Household, error)

	// GetHousehold reads one with its live contacts and attendees.
	GetHousehold(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, id string) (*Household, error)

	// ListHouseholds reads the studio's, each with its live people.
	ListHouseholds(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, includeArchived bool) ([]*Household, error)

	// UpdateHousehold rewrites the name, language and notes.
	UpdateHousehold(ctx context.Context, tx database.Tx, scope tenancy.Scope, household *Household) (*Household, error)

	// ArchiveHousehold retires the household and everybody on it.
	ArchiveHousehold(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string) error

	// AttendeesOfHousehold names the household's live attendees, which is what
	// a caller checks for standing lessons before archiving it.
	AttendeesOfHousehold(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, householdID string) ([]string, error)

	// CreateContact puts a contact on a live household.
	CreateContact(ctx context.Context, tx database.Tx, scope tenancy.Scope, contact *Contact) (*Contact, error)

	// GetContact reads one, or ErrNotFound.
	GetContact(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, id string) (*Contact, error)

	// UpdateContact rewrites the name, phone, language and relationship.
	UpdateContact(ctx context.Context, tx database.Tx, scope tenancy.Scope, contact *Contact) (*Contact, error)

	// ArchiveContact retires a contact, unlinking any attendee that was them.
	ArchiveContact(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string) error

	// CreateAttendee puts an attendee on a live household.
	CreateAttendee(ctx context.Context, tx database.Tx, scope tenancy.Scope, attendee *Attendee) (*Attendee, error)

	// GetAttendee reads one, or ErrNotFound.
	GetAttendee(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, id string) (*Attendee, error)

	// UpdateAttendee rewrites the name, language and contact link.
	UpdateAttendee(ctx context.Context, tx database.Tx, scope tenancy.Scope, attendee *Attendee) (*Attendee, error)

	// ArchiveAttendee retires one. Whether they still have standing lessons is
	// the caller's question to ask first.
	ArchiveAttendee(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string) error
}
