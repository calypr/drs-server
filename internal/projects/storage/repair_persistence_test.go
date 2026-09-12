package storage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/sqlite"
)

func TestRepairPreservesWorkingLocationWithoutConfirmedReplacement(t *testing.T) {
	for _, test := range []struct {
		name       string
		probeError error
	}{
		{name: "missing replacement"},
		{name: "provider failure", probeError: errors.New("provider unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := sqlite.NewSqliteDB(":memory:", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			sha := strings.Repeat("a", 64)
			original := "s3://repair-bucket/prefix/legacy/working-object"
			canonical := "s3://repair-bucket/prefix/did-1/" + sha
			records := objects.NewService(db)
			if _, err := records.RegisterObjects(ctx, []drs.DrsObject{repairRecord("did-1", sha, original)}); err != nil {
				t.Fatal(err)
			}
			probe := &fakeRepairProbe{missing: map[string]bool{canonical: true, "s3://repair-bucket/prefix/file.txt": true}, providerError: test.probeError}
			service := newRepairTestService(records, repairBuckets(), probe)
			result, err := service.ApplyAuthorized(ctx, internalapi.ScopeRepairOptions{Organization: "org", Project: "project", CheckStorage: true})
			if err != nil {
				t.Fatal(err)
			}
			got, err := db.GetObject(ctx, "did-1")
			if err != nil {
				t.Fatal(err)
			}
			if got.AccessMethods == nil || len(*got.AccessMethods) != 1 {
				t.Fatalf("stored access methods = %+v", got.AccessMethods)
			}
			if location := (*got.AccessMethods)[0].AccessUrl.Url; location != original {
				t.Errorf("stored URL = %q, want working URL %q", location, original)
			}
			if result.Mutated != 0 || result.AutoFixable != 0 {
				t.Errorf("repair mutated=%d auto_fixable=%d, want 0, 0", result.Mutated, result.AutoFixable)
			}
		})
	}
}
