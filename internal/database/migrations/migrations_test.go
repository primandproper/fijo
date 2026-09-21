package migrations

import (
	"testing"

	"github.com/shoenig/test/must"
)

// TestNewMigrator is the check that needs no database: every platform schema
// renders, every generated body annotates, and no version collides with a
// file on disk. It is what turns a platform package renaming a table into a
// failed unit test rather than a failed deploy.
func TestNewMigrator(t *testing.T) {
	t.Parallel()

	migrator, err := NewMigrator(nil)
	must.NoError(t, err)
	must.NotNil(t, migrator)
}
