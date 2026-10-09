// Package owners is the first of Fijo's own domains, and the smallest: what
// Fijo knows about a person that the identity directory does not.
//
// Today that is one fact, the time zone a user reads the grid in. identity's
// documentation puts a consumer's own columns in a side table keyed by user
// ID rather than on the user, and this package is that table with a Store
// over it — DDB's shape, one store interface, one Postgres implementation, one
// mock, so the roster and the schedule can follow the same outline in phase 1.
//
// The studio's time zone is not here. It is identity.Account.TimeZone, written
// at registration and edited through identity's own UpdateAccount, and Fijo
// reads it off the account rather than keeping a second copy.
package owners

import (
	"context"
	"time"

	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

// The refusals.
var (
	ErrNilExecutor = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil query executor")
	ErrEmptyUserID = platformerrors.Wrap(platformerrors.ErrEmptyInputParameter, "empty user ID")

	// ErrPreferencesNotFound is a user who has never set anything, which is
	// every user until they do.
	ErrPreferencesNotFound = platformerrors.New("user preferences not found")

	// ErrInvalidTimeZone is a zone name the runtime cannot load, or "Local",
	// which names whatever host reads it rather than a zone.
	ErrInvalidTimeZone = platformerrors.Wrap(platformerrors.ErrUnrecognizedInputValue, "invalid time zone")
)

// Preferences is a user's row.
type Preferences struct {
	_ struct{} `json:"-"`

	CreatedAt     time.Time     `json:"createdAt"`
	LastUpdatedAt *time.Time    `json:"lastUpdatedAt,omitempty"`
	UserID        string        `json:"userID"`
	TimeZone      string        `json:"timeZone"`
	Scope         tenancy.Scope `json:"scope"`
}

// Location loads the preference's zone.
func (p *Preferences) Location() (*time.Location, error) {
	if p == nil || p.TimeZone == "" {
		return time.UTC, nil
	}

	return LoadTimeZone(p.TimeZone)
}

// LoadTimeZone is the one rule about what a time zone may be: an IANA name the
// runtime can load, and never "Local". It is the same rule identity applies to
// an account's zone, spelled here because the two packages are siblings.
func LoadTimeZone(name string) (*time.Location, error) {
	if name == "" {
		return nil, platformerrors.Wrap(ErrInvalidTimeZone, "empty time zone")
	}

	if name == "Local" {
		return nil, platformerrors.Wrap(ErrInvalidTimeZone, `"Local" names the reader's host rather than a zone`)
	}

	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, platformerrors.Wrapf(ErrInvalidTimeZone, "%q: %v", name, err)
	}

	return loc, nil
}

// Store reads and writes preferences. Writes take a Tx and reads take an
// executor, as every store on this stack does.
type Store interface {
	// GetPreferences returns a user's row, or ErrPreferencesNotFound.
	GetPreferences(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, userID string) (*Preferences, error)

	// SetTimeZone writes a user's zone, creating the row on first use, and
	// returns the row as written. An empty zone clears the preference, so the
	// user reads the grid in the studio's zone again; anything else LoadTimeZone
	// refuses is refused.
	SetTimeZone(ctx context.Context, tx database.Tx, scope tenancy.Scope, userID, timeZone string) (*Preferences, error)
}
