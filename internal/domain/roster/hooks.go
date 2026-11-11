package roster

import (
	"context"

	"github.com/primandproper/primitives-go/v2/database"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

// Hooks are told about every write the store makes, on the write's own
// transaction, after it succeeded. An error from a hook fails the write, so a
// hook that records the write cannot leave it unrecorded.
//
// An update's hook gets the row before and after. Both are the row alone: a
// household's Contacts and Attendees are not filled, because the write did not
// change them and each has its own hooks.
//
// The store calls every method, so a write added to Store later is a method
// added here, and RecordingHooks fails to compile until somebody decides what
// it records.
type Hooks interface {
	AfterCreateHousehold(ctx context.Context, tx database.Tx, scope tenancy.Scope, household *Household) error
	AfterUpdateHousehold(ctx context.Context, tx database.Tx, scope tenancy.Scope, before, after *Household) error
	AfterArchiveHousehold(ctx context.Context, tx database.Tx, scope tenancy.Scope, id string) error

	AfterCreateContact(ctx context.Context, tx database.Tx, scope tenancy.Scope, contact *Contact) error
	AfterUpdateContact(ctx context.Context, tx database.Tx, scope tenancy.Scope, before, after *Contact) error
	AfterArchiveContact(ctx context.Context, tx database.Tx, scope tenancy.Scope, contact *Contact) error

	AfterCreateAttendee(ctx context.Context, tx database.Tx, scope tenancy.Scope, attendee *Attendee) error
	AfterUpdateAttendee(ctx context.Context, tx database.Tx, scope tenancy.Scope, before, after *Attendee) error
	AfterArchiveAttendee(ctx context.Context, tx database.Tx, scope tenancy.Scope, attendee *Attendee) error
}

// NoopHooks are the Hooks of a store built without any: they do nothing.
type NoopHooks struct{}

var _ Hooks = NoopHooks{}

// AfterCreateHousehold implements Hooks.
func (NoopHooks) AfterCreateHousehold(context.Context, database.Tx, tenancy.Scope, *Household) error {
	return nil
}

// AfterUpdateHousehold implements Hooks.
func (NoopHooks) AfterUpdateHousehold(context.Context, database.Tx, tenancy.Scope, *Household, *Household) error {
	return nil
}

// AfterArchiveHousehold implements Hooks.
func (NoopHooks) AfterArchiveHousehold(context.Context, database.Tx, tenancy.Scope, string) error {
	return nil
}

// AfterCreateContact implements Hooks.
func (NoopHooks) AfterCreateContact(context.Context, database.Tx, tenancy.Scope, *Contact) error {
	return nil
}

// AfterUpdateContact implements Hooks.
func (NoopHooks) AfterUpdateContact(context.Context, database.Tx, tenancy.Scope, *Contact, *Contact) error {
	return nil
}

// AfterArchiveContact implements Hooks.
func (NoopHooks) AfterArchiveContact(context.Context, database.Tx, tenancy.Scope, *Contact) error {
	return nil
}

// AfterCreateAttendee implements Hooks.
func (NoopHooks) AfterCreateAttendee(context.Context, database.Tx, tenancy.Scope, *Attendee) error {
	return nil
}

// AfterUpdateAttendee implements Hooks.
func (NoopHooks) AfterUpdateAttendee(context.Context, database.Tx, tenancy.Scope, *Attendee, *Attendee) error {
	return nil
}

// AfterArchiveAttendee implements Hooks.
func (NoopHooks) AfterArchiveAttendee(context.Context, database.Tx, tenancy.Scope, *Attendee) error {
	return nil
}

// WithHooks installs the hooks every write calls. A nil leaves NoopHooks.
func WithHooks(hooks Hooks) Option {
	return func(s *SQLStore) {
		if hooks != nil {
			s.hooks = hooks
		}
	}
}
