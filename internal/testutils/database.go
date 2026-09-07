package testutils

import (
	"github.com/calypr/syfon/internal/db"
	"github.com/calypr/syfon/internal/db/sqlite"
)

// NewInMemoryDB returns a new database interface backed by an in-memory SQLite database.
// This is used primarily for testing.
func NewInMemoryDB() db.DatabaseInterface {
	// Use SQLite in-memory mode.
	database, err := sqlite.NewSqliteDB(":memory:")
	if err != nil {
		panic("failed to create in-memory sqlite db: " + err.Error())
	}
	return database
}
