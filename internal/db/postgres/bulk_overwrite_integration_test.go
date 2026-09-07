package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
	sycommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/core"
	postgresdb "github.com/calypr/syfon/internal/db/postgres"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/testutils"
)

func TestPostgresBulkOverwriteObjects(t *testing.T) {
	dsn := os.Getenv("SYFON_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("SYFON_TEST_POSTGRES_DSN is not configured")
	}
	db, err := postgresdb.NewPostgresDB(dsn)
	if err != nil {
		t.Fatalf("open postgres test database: %v", err)
	}

	resource, err := sycommon.ResourcePath("ci-overwrite", "project")
	if err != nil {
		t.Fatal(err)
	}
	sha := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	now := time.Now().UTC()
	oldName := "old"
	if err := db.RegisterObjects(context.Background(), []models.InternalObject{{
		DrsObject: drs.DrsObject{
			Id:               "ci-overwrite-target",
			Name:             &oldName,
			CreatedTime:      now,
			UpdatedTime:      &now,
			Checksums:        []drs.Checksum{{Type: "sha256", Checksum: sha}},
			ControlledAccess: &[]string{resource},
		},
		Authorizations: map[string][]string{"ci-overwrite": {"project"}},
	}}); err != nil {
		t.Fatalf("seed target record: %v", err)
	}

	newName := "new"
	om := core.NewObjectManager(db, &testutils.MockUrlManager{})
	result, err := om.BulkOverwriteObjects(context.Background(), "ci-overwrite", "project", []models.InternalObject{{
		DrsObject: drs.DrsObject{
			Id:               "ci-overwrite-source",
			Name:             &newName,
			CreatedTime:      now,
			UpdatedTime:      &now,
			Checksums:        []drs.Checksum{{Type: "sha256", Checksum: sha}},
			ControlledAccess: &[]string{resource},
		},
		Authorizations: map[string][]string{"ci-overwrite": {"project"}},
	}})
	if err != nil {
		t.Fatalf("bulk overwrite: %v", err)
	}
	if result.Replaced != 1 || result.ChecksumMatched != 1 {
		t.Fatalf("unexpected overwrite result: %+v", result)
	}
	got, err := db.GetObject(context.Background(), "ci-overwrite-target")
	if err != nil {
		t.Fatalf("read overwritten target: %v", err)
	}
	if got.Name == nil || *got.Name != newName {
		t.Fatalf("source metadata did not replace target: %+v", got.Name)
	}
	if _, err := db.GetObject(context.Background(), "ci-overwrite-source"); err == nil {
		t.Fatal("checksum sibling should retain target DID")
	}
}
