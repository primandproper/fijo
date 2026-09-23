// Package studios answers questions about a studio that live in the identity
// directory rather than in Fijo's own tables.
//
// A studio is an identity account, so its name and its time zone are identity's
// columns and a Fijo domain's statements cannot join them. Rather than copy
// them into Fijo's schema — two sources of truth for one fact — the domains
// that need one take a narrow interface and this package implements it over
// identity's store.
package studios

import (
	"context"
	"sync"
	"time"

	"github.com/primandproper/platform-go/v14/identity"
	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

// ErrNoTimeZone is a studio whose account names no zone. It is a refusal
// rather than a default, because materializing a grid an hour out is worse
// than materializing none.
var ErrNoTimeZone = platformerrors.New("the studio's account names no time zone")

// Zones reads a studio's time zone from its identity account.
//
// The directory is read in the global scope, because that is the scope the
// identity directory is: studios are accounts in one directory, and the studio
// scope is Fijo's own.
//
// Answers are cached for the life of the value. A studio's zone changes about
// as often as a studio moves house, and the reader is a worker loop that would
// otherwise ask once per studio per pass.
type Zones struct {
	accounts identity.Store
	reader   database.SQLQueryExecutor
	cached   map[string]*time.Location
	mu       sync.RWMutex
}

// NewZones builds one over identity's store and a read executor.
func NewZones(accounts identity.Store, reader database.SQLQueryExecutor) (*Zones, error) {
	switch {
	case accounts == nil:
		return nil, platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil identity store")
	case reader == nil:
		return nil, platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil read executor")
	}

	return &Zones{accounts: accounts, reader: reader, cached: map[string]*time.Location{}}, nil
}

// ZoneOf implements the zone lookup the schedule's materializer takes.
func (z *Zones) ZoneOf(ctx context.Context, studioID string) (*time.Location, error) {
	if studioID == "" {
		return nil, platformerrors.Wrap(platformerrors.ErrEmptyInputParameter, "empty studio")
	}

	z.mu.RLock()
	cached, known := z.cached[studioID]
	z.mu.RUnlock()

	if known {
		return cached, nil
	}

	account, err := z.accounts.GetAccount(ctx, z.reader, tenancy.Global(), studioID)
	if err != nil {
		return nil, platformerrors.Wrapf(err, "reading the studio %q", studioID)
	}

	if account.TimeZone == "" {
		return nil, platformerrors.Wrapf(ErrNoTimeZone, "studio %q", studioID)
	}

	loc, err := time.LoadLocation(account.TimeZone)
	if err != nil {
		return nil, platformerrors.Wrapf(err, "loading the studio %q time zone %q", studioID, account.TimeZone)
	}

	z.mu.Lock()
	z.cached[studioID] = loc
	z.mu.Unlock()

	return loc, nil
}

// Forget drops a cached answer, for a deployment that has just changed one.
func (z *Zones) Forget(studioID string) {
	z.mu.Lock()
	delete(z.cached, studioID)
	z.mu.Unlock()
}
