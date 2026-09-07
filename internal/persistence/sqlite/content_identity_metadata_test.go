package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/objects"
)

func TestMergeContentRowTxPersistsMergedMetadataAndAlias(t *testing.T) {
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	oldTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Hour)
	if _, err := db.db.Exec(`
		INSERT INTO drs_object (id, size, created_time, updated_time, name, version, description)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, "object-1", 0, oldTime, oldTime, "old.txt", "", ""); err != nil {
		t.Fatal(err)
	}

	name, version, description := "dir/new.txt", "2", "new"
	tx, err := db.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := mergeContentRowTx(context.Background(), tx, sqliteContentRow{
		id: "object-1", name: "old.txt", updated: oldTime,
	}, &objects.Record{
		Name: &name, Version: &version, Description: &description, Size: 9, UpdatedTime: &newTime,
	}, []string{"/organization/org/project/p"}, []string{"/organization/org/project/p"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var size int64
	var updated time.Time
	var gotName, gotVersion, gotDescription, alias string
	if err := db.db.QueryRow(`SELECT size, updated_time, name, version, description FROM drs_object WHERE id = ?`, "object-1").Scan(&size, &updated, &gotName, &gotVersion, &gotDescription); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow(`SELECT name_alias FROM drs_object_name_alias WHERE object_id = ?`, "object-1").Scan(&alias); err != nil {
		t.Fatal(err)
	}
	if size != 9 || !updated.Equal(newTime) || gotName != "new.txt" || gotVersion != "2" || gotDescription != "new" || alias != "old.txt" {
		t.Fatalf("merged metadata = size=%d updated=%v name=%q version=%q description=%q alias=%q", size, updated, gotName, gotVersion, gotDescription, alias)
	}
}
