// Package ownersmock is a hand-written stand-in for owners.Store, for tests
// that need one without a database. The domains that arrive in phase 1 bring
// moq, and this package regenerates from a directive then.
package ownersmock

import (
	"context"

	"github.com/primandproper/fijo/internal/domain/owners"

	"github.com/primandproper/primitives-go/v2/database"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

// StoreMock implements owners.Store with per-method functions.
type StoreMock struct {
	GetPreferencesFunc func(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, userID string) (*owners.Preferences, error)
	SetTimeZoneFunc    func(ctx context.Context, tx database.Tx, scope tenancy.Scope, userID, timeZone string) (*owners.Preferences, error)
}

var _ owners.Store = (*StoreMock)(nil)

// GetPreferences calls GetPreferencesFunc.
func (m *StoreMock) GetPreferences(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, userID string) (*owners.Preferences, error) {
	if m.GetPreferencesFunc == nil {
		panic("StoreMock.GetPreferencesFunc is unset")
	}

	return m.GetPreferencesFunc(ctx, q, scope, userID)
}

// SetTimeZone calls SetTimeZoneFunc.
func (m *StoreMock) SetTimeZone(ctx context.Context, tx database.Tx, scope tenancy.Scope, userID, timeZone string) (*owners.Preferences, error) {
	if m.SetTimeZoneFunc == nil {
		panic("StoreMock.SetTimeZoneFunc is unset")
	}

	return m.SetTimeZoneFunc(ctx, tx, scope, userID, timeZone)
}
