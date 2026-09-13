package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/store"
)

const canonicalRepairSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestCanonicalRepairRollsBackWhenAliasCreationFails(t *testing.T) {
	db, canonicalID, duplicateID := seedLegacyCanonicalRepair(t)
	ctx := context.Background()
	if _, err := db.DB().ExecContext(ctx, `CREATE TRIGGER fail_canonical_repair_alias BEFORE INSERT ON drs_object_alias BEGIN SELECT RAISE(ABORT, 'blocked alias'); END`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.DB().ExecContext(ctx, `DROP TRIGGER fail_canonical_repair_alias`) })

	err := db.RepairCanonicalDuplicates(ctx, []objects.CanonicalRepair{canonicalRepair(t, db, canonicalID, duplicateID)})
	if err == nil {
		t.Fatal("RepairCanonicalDuplicates succeeded, want alias trigger failure")
	}
	assertLegacyCanonicalRepairUnchanged(t, db, canonicalID, duplicateID)
}

func TestCanonicalRepairRollsBackWhenDuplicateDeletionFails(t *testing.T) {
	db, canonicalID, duplicateID := seedLegacyCanonicalRepair(t)
	ctx := context.Background()
	if _, err := db.DB().ExecContext(ctx, `CREATE TRIGGER fail_canonical_repair_delete BEFORE DELETE ON drs_object BEGIN SELECT RAISE(ABORT, 'blocked delete'); END`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.DB().ExecContext(ctx, `DROP TRIGGER fail_canonical_repair_delete`) })

	err := db.RepairCanonicalDuplicates(ctx, []objects.CanonicalRepair{canonicalRepair(t, db, canonicalID, duplicateID)})
	if err == nil {
		t.Fatal("RepairCanonicalDuplicates succeeded, want deletion trigger failure")
	}
	assertLegacyCanonicalRepairUnchanged(t, db, canonicalID, duplicateID)
}

func TestCanonicalRepairSuccessAndPublicRetryAreIdempotent(t *testing.T) {
	db, canonicalID, duplicateID := seedLegacyCanonicalRepair(t)
	ctx := context.Background()
	service := objects.NewService(db)
	if collapsed, err := service.CollapseProjectChecksumDuplicates(ctx, "org", "project"); err != nil {
		t.Fatalf("CollapseProjectChecksumDuplicates() error = %v", err)
	} else if collapsed != 1 {
		t.Fatalf("CollapseProjectChecksumDuplicates() collapsed %d records, want 1", collapsed)
	}

	var physicalRows int
	if err := db.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM drs_object`).Scan(&physicalRows); err != nil {
		t.Fatal(err)
	}
	if physicalRows != 1 {
		t.Fatalf("physical row count = %d, want 1", physicalRows)
	}
	resolved, err := db.ResolveObjectAlias(ctx, duplicateID)
	if err != nil || resolved != canonicalID {
		t.Fatalf("duplicate alias = %q, err=%v, want %q", resolved, err, canonicalID)
	}
	merged, err := db.GetObject(ctx, canonicalID)
	if err != nil {
		t.Fatal(err)
	}
	if merged.AccessMethods == nil || len(*merged.AccessMethods) != 2 {
		t.Fatalf("merged access methods = %+v, want both physical methods", merged.AccessMethods)
	}

	if collapsed, err := service.CollapseProjectChecksumDuplicates(ctx, "org", "project"); err != nil {
		t.Fatalf("public repair retry error = %v", err)
	} else if collapsed != 0 {
		t.Fatalf("public repair retry collapsed %d records, want 0", collapsed)
	}
}

func seedLegacyCanonicalRepair(t *testing.T) (*store.Store, string, string) {
	t.Helper()
	db, err := NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	now := time.Now().UTC()
	canonicalID := "canonical-repair-a"
	duplicateID := "canonical-repair-b"
	resource := "/organization/org/project/project"
	if err := db.RegisterObjects(ctx, []drs.DrsObject{
		{Id: canonicalID, CreatedTime: now, UpdatedTime: &now, AccessMethods: &[]drs.AccessMethod{{Type: "s3", AccessUrl: &drs.AccessURL{Url: "s3://bucket/a"}}}},
		{Id: duplicateID, CreatedTime: now.Add(time.Second), UpdatedTime: ptrTime(now.Add(time.Second)), AccessMethods: &[]drs.AccessMethod{{Type: "s3", AccessUrl: &drs.AccessURL{Url: "s3://bucket/b"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
		INSERT INTO drs_object_checksum (object_id, type, checksum) VALUES (?, 'sha256', ?), (?, 'sha256', ?);
		INSERT INTO drs_object_controlled_access (object_id, resource) VALUES (?, ?), (?, ?)
	`, canonicalID, canonicalRepairSHA, duplicateID, canonicalRepairSHA, canonicalID, resource, duplicateID, resource); err != nil {
		t.Fatal(err)
	}
	return db, canonicalID, duplicateID
}

func canonicalRepair(t *testing.T, db *store.Store, canonicalID, duplicateID string) objects.CanonicalRepair {
	t.Helper()
	canonical, err := db.GetObject(context.Background(), canonicalID)
	if err != nil {
		t.Fatal(err)
	}
	methods := append([]drs.AccessMethod(nil), (*canonical.AccessMethods)...)
	methods = append(methods, drs.AccessMethod{Type: "s3", AccessUrl: &drs.AccessURL{Url: "s3://bucket/b"}})
	canonical.AccessMethods = &methods
	canonical.Checksums = []drs.Checksum{{Type: "sha256", Checksum: canonicalRepairSHA}}
	canonical.ControlledAccess = &[]string{"/organization/org/project/project"}
	return objects.CanonicalRepair{Canonical: *canonical, DuplicateIDs: []string{duplicateID}}
}

func assertLegacyCanonicalRepairUnchanged(t *testing.T, db *store.Store, canonicalID, duplicateID string) {
	t.Helper()
	var physicalRows int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM drs_object`).Scan(&physicalRows); err != nil {
		t.Fatal(err)
	}
	if physicalRows != 2 {
		t.Fatalf("physical row count after rollback = %d, want 2", physicalRows)
	}
	if _, err := db.ResolveObjectAlias(context.Background(), duplicateID); !errors.Is(err, errorapi.ErrNotFound) {
		t.Fatalf("duplicate alias after rollback err = %v, want not found", err)
	}
	for _, id := range []string{canonicalID, duplicateID} {
		if _, err := db.GetObject(context.Background(), id); err != nil {
			t.Fatalf("physical object %q after rollback: %v", id, err)
		}
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
