package postgres_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/google/uuid"
)

func TestPostgresObjectDeletionClearsPendingUsage(t *testing.T) {
	database := openPostgresTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	one, two, retained := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if err := database.RegisterObjects(ctx, []drs.DrsObject{
		postgresUsageObject(one, one, now),
		postgresUsageObject(two, two, now),
		postgresUsageObject(retained, retained, now),
	}); err != nil {
		t.Fatal(err)
	}
	alias := uuid.NewString()
	if err := database.CreateObjectAlias(ctx, alias, two); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{one, two, retained} {
		if err := database.RecordFileUpload(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.BulkDeleteObjects(ctx, []string{one, alias, uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	if err := database.RegisterObjects(ctx, []drs.DrsObject{
		postgresUsageObject(one, one+"-new", now),
		postgresUsageObject(two, two+"-new", now),
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{one, two} {
		usage, err := database.GetFileUsage(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got := postgresUsageCount(usage.UploadCount); got != 0 {
			t.Fatalf("recreated object %q upload count = %d, want 0", id, got)
		}
	}
	usage, err := database.GetFileUsage(ctx, retained)
	if err != nil {
		t.Fatal(err)
	}
	if got := postgresUsageCount(usage.UploadCount); got != 1 {
		t.Fatalf("retained object upload count = %d, want 1", got)
	}
}

func postgresUsageCount(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func postgresUsageObject(id, checksumSeed string, now time.Time) drs.DrsObject {
	checksum := sha256.Sum256([]byte(checksumSeed))
	return drs.DrsObject{
		Id:          id,
		Size:        1,
		CreatedTime: now,
		UpdatedTime: &now,
		Checksums:   []drs.Checksum{{Type: "sha256", Checksum: fmt.Sprintf("%x", checksum)}},
	}
}
