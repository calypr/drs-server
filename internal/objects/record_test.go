package objects

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/access"
)

func recordPtr[T any](value T) *T { return &value }

func TestMaterializeCandidatePreservesRegistrationContract(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	now := time.Unix(123, 0).UTC()
	name := `/nested/path/object.bin`
	size := int64(42)
	controlled := []string{"/organization/org/project/proj"}
	aliases := []string{"legacy-name", "id:explicit-id"}
	accessID := "provided"
	candidate := drs.DrsObjectCandidate{
		Name:             &name,
		Size:             size,
		Aliases:          &aliases,
		Checksums:        []drs.Checksum{{Type: "sha256", Checksum: checksum}},
		ControlledAccess: &controlled,
		AccessMethods: &[]drs.AccessMethod{{
			AccessId:  &accessID,
			Type:      "https",
			AccessUrl: &drs.AccessURL{Url: "https://storage.example/object.bin"},
		}, {
			Type:      "s3",
			AccessUrl: &drs.AccessURL{Url: "s3://bucket/object.bin"},
		}},
	}

	got, err := MaterializeCandidate(candidate, now)
	if err != nil {
		t.Fatalf("MaterializeCandidate() error = %v", err)
	}
	if got.Id != "explicit-id" || got.SelfUri != "drs://explicit-id" {
		t.Fatalf("explicit ID was not preserved: id=%q self=%q", got.Id, got.SelfUri)
	}
	if got.Name == nil || *got.Name != "object.bin" {
		t.Fatalf("name = %v, want normalized basename", got.Name)
	}
	if got.Size != size || !got.CreatedTime.Equal(now) || got.UpdatedTime == nil || !got.UpdatedTime.Equal(now) {
		t.Fatalf("timestamps/size changed: %#v", got)
	}
	if got.ControlledAccess == nil || len(*got.ControlledAccess) != 1 || (*got.ControlledAccess)[0] != controlled[0] {
		t.Fatalf("controlled access = %v, want %v", got.ControlledAccess, controlled)
	}
	if resources := AccessResources(&got); len(resources) != 1 || resources[0] != controlled[0] {
		t.Fatalf("controlled access = %#v", resources)
	}
	if got.AccessMethods == nil || len(*got.AccessMethods) != 2 || (*got.AccessMethods)[0].AccessId == nil || *(*got.AccessMethods)[0].AccessId != accessID || (*got.AccessMethods)[1].AccessId == nil || *(*got.AccessMethods)[1].AccessId != "s3-0812f73732f52048578808c9" {
		t.Fatalf("access IDs = %#v", got.AccessMethods)
	}
}

func TestMaterializeCandidateUsesDeterministicScopedIDAndDefaultName(t *testing.T) {
	checksum := strings.Repeat("b", 64)
	controlled := []string{"/organization/org/project/proj"}
	candidate := drs.DrsObjectCandidate{
		ControlledAccess: &controlled,
		Checksums:        []drs.Checksum{{Type: "sha256", Checksum: checksum}},
		AccessMethods:    &[]drs.AccessMethod{{Type: "s3", AccessUrl: &drs.AccessURL{Url: "s3://bucket/object"}}},
	}

	first, err := MaterializeCandidate(candidate, time.Unix(1, 0))
	if err != nil {
		t.Fatalf("first MaterializeCandidate() error = %v", err)
	}
	second, err := MaterializeCandidate(candidate, time.Unix(2, 0))
	if err != nil {
		t.Fatalf("second MaterializeCandidate() error = %v", err)
	}
	if first.Id == "" || first.Id != second.Id {
		t.Fatalf("IDs are not deterministic: %q vs %q", first.Id, second.Id)
	}
	if first.Name == nil || *first.Name != checksum {
		t.Fatalf("default name = %v, want checksum", first.Name)
	}
}

func TestMaterializeCandidateRejectsMissingAccessMethods(t *testing.T) {
	checksums := []drs.Checksum{{Type: "sha256", Checksum: strings.Repeat("c", 64)}}
	if _, err := MaterializeCandidate(drs.DrsObjectCandidate{Checksums: checksums}, time.Unix(0, 0)); err == nil || !strings.Contains(err.Error(), "access method") {
		t.Fatalf("error = %v, want access-method validation", err)
	}
}

func TestMintRecordIDFromChecksumUsesCanonicalProjectScope(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	first, err := MintRecordIDFromChecksum(checksum, []string{"/organization/syfon/project/e2e"})
	if err != nil {
		t.Fatalf("MintRecordIDFromChecksum returned error: %v", err)
	}
	second, err := MintRecordIDFromChecksum(checksum, []string{"/programs/syfon/projects/e2e"})
	if err != nil {
		t.Fatalf("MintRecordIDFromChecksum returned error: %v", err)
	}
	other, err := MintRecordIDFromChecksum(checksum, []string{"/organization/syfon/project/other"})
	if err != nil {
		t.Fatalf("MintRecordIDFromChecksum returned error: %v", err)
	}
	if first == "" || second == "" || other == "" {
		t.Fatalf("expected non-empty object IDs: %q %q %q", first, second, other)
	}
	if first != second {
		t.Fatalf("canonical scope IDs differ: %q and %q", first, second)
	}
	if first == other {
		t.Fatalf("scope-sensitive IDs match: %q and %q", first, other)
	}
	if _, err := MintRecordIDFromChecksum(checksum, nil); err == nil {
		t.Fatal("expected missing-scope error")
	}
	if _, err := MintRecordIDFromChecksum(checksum, []string{"/organization/syfon"}); err == nil {
		t.Fatal("expected organization-only scope error")
	}
	if got := AccessMethodID(" S3 ", " s3://bucket/key "); got == "" {
		t.Fatal("expected access method id")
	}
}

func TestNameNormalizationPreservesTrailingSlashCompatibility(t *testing.T) {
	if got := CleanToBasename("foo/bar/"); got != "bar" {
		t.Fatalf("trailing slash basename = %q", got)
	}
	got := NormalizeNameAliases("/primary/primary.txt", []string{"\\other\\z.txt", "/primary/primary.txt", "z.txt", ""})
	if len(got) != 1 || got[0] != "z.txt" {
		t.Fatalf("normalized aliases = %#v", got)
	}
}

func TestNormalizeRecordAppliesIncomingRecordPolicy(t *testing.T) {
	fallback := time.Date(2026, 9, 9, 12, 0, 0, 0, time.FixedZone("PDT", -7*60*60))
	created := time.Date(2026, 9, 8, 14, 30, 0, 123, time.UTC)
	updated := time.Date(2026, 9, 8, 9, 30, 0, 456, time.UTC)
	name := `dir\primary.txt`
	controlled := []string{"https://example.test/programs/org/projects/proj", " /organization/org ", ""}
	valid := strings.Repeat("A", 64)
	invalid := "not-a-sha256"
	record := drs.DrsObject{
		Id:               "  did:example:1  ",
		CreatedTime:      created,
		UpdatedTime:      &updated,
		Name:             &name,
		Checksums:        []drs.Checksum{{Type: "SHA-256", Checksum: "sha256:" + valid}, {Type: "md5", Checksum: "kept"}, {Type: "sha256", Checksum: invalid}},
		ControlledAccess: &controlled,
		NameAliases:      recordPtr([]string{"/primary.txt", `other\alias.txt`, "alias.txt", `other\alias.txt`, ""}),
	}

	got, err := NormalizeRecord(record, fallback)
	if err != nil {
		t.Fatalf("NormalizeRecord() error = %v", err)
	}
	wantControlled := []string{"/organization/org/project/proj", "/organization/org"}
	want := drs.DrsObject{
		Id:               "did:example:1",
		CreatedTime:      created,
		UpdatedTime:      &updated,
		Name:             objectStringPtr("primary.txt"),
		Version:          objectStringPtr("1"),
		Checksums:        []drs.Checksum{{Type: "sha256", Checksum: strings.ToLower(valid)}, {Type: "md5", Checksum: "kept"}, {Type: "sha256", Checksum: invalid}},
		ControlledAccess: &wantControlled,
		NameAliases:      recordPtr([]string{"alias.txt"}),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeRecord() = %#v, want %#v", got, want)
	}

	got, err = NormalizeRecord(drs.DrsObject{Id: "did:example:2"}, fallback)
	if err != nil {
		t.Fatalf("NormalizeRecord() zero times error = %v", err)
	}
	if !got.CreatedTime.Equal(fallback.UTC()) || got.UpdatedTime == nil || !got.UpdatedTime.Equal(fallback.UTC()) {
		t.Fatalf("NormalizeRecord() zero times = %#v, want fallback %v", got, fallback.UTC())
	}
}

func TestNormalizeRecordRejectsMissingID(t *testing.T) {
	if _, err := NormalizeRecord(drs.DrsObject{Id: "  "}, time.Time{}); err == nil || err.Error() != "did is required" {
		t.Fatalf("NormalizeRecord() error = %v, want did is required", err)
	}
}

func TestRecordHasChecksumTypeAndValueNormalizesValidSHAOnly(t *testing.T) {
	valid := strings.Repeat("a", 64)
	object := drs.DrsObject{Checksums: []drs.Checksum{
		{Type: "sha-256", Checksum: strings.ToUpper(valid)},
		{Type: "sha256", Checksum: "not-a-sha"},
		{Type: "md5", Checksum: "CaseSensitive"},
	}}
	if !RecordHasChecksumTypeAndValue(object, "sha-256", "SHA256:"+strings.ToUpper(valid)) {
		t.Fatal("valid SHA-256 query should match a differently cased candidate")
	}
	if RecordHasChecksumTypeAndValue(object, "sha256", "not-a-sha") {
		t.Fatal("invalid SHA-256 values must not compare equal")
	}
	if !RecordHasChecksumTypeAndValue(object, "md5", "CaseSensitive") {
		t.Fatal("generic checksum should match literally")
	}
	if RecordHasChecksumTypeAndValue(object, "md5", "casesensitive") {
		t.Fatal("generic checksum matching must remain case-sensitive")
	}
}

func TestEnforceCanonicalProjectScope(t *testing.T) {
	initial := []string{"/organization/other/project/proj"}
	obj, err := enforceCanonicalProjectScope(drs.DrsObject{
		Id: "obj-1", ControlledAccess: &initial,
	}, "org", "proj")
	if err != nil {
		t.Fatalf("enforceCanonicalProjectScope() error = %v", err)
	}
	got := AccessResources(&obj)
	want := []string{"/organization/other/project/proj", "/organization/org/project/proj"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("AccessResources() = %#v, want %#v", got, want)
	}
}

func TestMergeRegistrationMetadata(t *testing.T) {
	oldTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Hour)
	tests := []struct {
		name string
		in   RegistrationMergeInput
		want RegistrationMergeResult
	}{
		{
			name: "replacement updates metadata and preserves old name",
			in: RegistrationMergeInput{
				ExistingName: "old.txt", ExistingVersion: "1", ExistingDescription: "old", ExistingSize: 7, ExistingUpdated: oldTime,
				IncomingName: `nested\\new.txt`, IncomingVersion: "2", IncomingDescription: "new", IncomingSize: 9, IncomingUpdated: newTime,
				CurrentResources: []string{"/organization/o/project/p"}, IncomingResources: []string{"/organization/o/project/p"},
			},
			want: RegistrationMergeResult{Name: "new.txt", Version: "2", Description: "new", Size: 7, Updated: newTime, NameAlias: "old.txt"},
		},
		{
			name: "non replacement keeps metadata and aliases incoming name",
			in: RegistrationMergeInput{
				ExistingName: "old.txt", ExistingVersion: "1", ExistingDescription: "old", ExistingSize: 7, ExistingUpdated: oldTime,
				IncomingName: "/tmp/new.txt", IncomingVersion: "2", IncomingDescription: "new", IncomingSize: 9, IncomingUpdated: newTime,
				CurrentResources: []string{"/organization/o/project/p", "/organization/o/project/q"}, IncomingResources: []string{"/organization/o/project/p"},
			},
			want: RegistrationMergeResult{Name: "old.txt", Version: "1", Description: "old", Size: 7, Updated: newTime, NameAlias: "new.txt"},
		},
		{
			name: "one nonoverlapping resource does not replace",
			in: RegistrationMergeInput{
				ExistingName: "old.txt", ExistingVersion: "1", ExistingDescription: "old", ExistingSize: 7, ExistingUpdated: oldTime,
				IncomingName: "new.txt", IncomingVersion: "2", IncomingDescription: "new", IncomingSize: 9, IncomingUpdated: newTime,
				CurrentResources: []string{"/organization/o/project/p"}, IncomingResources: []string{"/organization/o/project/q"},
			},
			want: RegistrationMergeResult{Name: "old.txt", Version: "1", Description: "old", Size: 7, Updated: newTime, NameAlias: "new.txt"},
		},
		{
			name: "empty stored fields are filled",
			in: RegistrationMergeInput{
				ExistingUpdated: oldTime,
				IncomingName:    "dir/new.txt", IncomingVersion: "2", IncomingDescription: "new", IncomingSize: 9, IncomingUpdated: newTime,
			},
			want: RegistrationMergeResult{Name: "new.txt", Version: "2", Description: "new", Size: 9, Updated: newTime},
		},
		{
			name: "blank incoming fields do not erase during replacement",
			in: RegistrationMergeInput{
				ExistingName: "old.txt", ExistingVersion: "1", ExistingDescription: "old", ExistingSize: 7, ExistingUpdated: oldTime,
				IncomingUpdated: newTime, CurrentResources: []string{"/organization/o/project/p"}, IncomingResources: []string{"/organization/o/project/p"},
			},
			want: RegistrationMergeResult{Name: "old.txt", Version: "1", Description: "old", Size: 7, Updated: newTime},
		},
		{
			name: "equal names do not create alias",
			in: RegistrationMergeInput{
				ExistingName: "same.txt", ExistingUpdated: oldTime, IncomingName: "/tmp/same.txt", IncomingUpdated: newTime,
				CurrentResources: []string{"/organization/o/project/p"}, IncomingResources: []string{"/organization/o/project/p"},
			},
			want: RegistrationMergeResult{Name: "same.txt", Updated: newTime},
		},
		{
			name: "blank fields do not erase and zero size does not replace",
			in: RegistrationMergeInput{
				ExistingName: "old.txt", ExistingVersion: "1", ExistingDescription: "old", ExistingSize: 7, ExistingUpdated: newTime,
				IncomingName: "", IncomingVersion: "", IncomingDescription: "", IncomingSize: 0, IncomingUpdated: oldTime,
			},
			want: RegistrationMergeResult{Name: "old.txt", Version: "1", Description: "old", Size: 7, Updated: newTime},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MergeRegistrationMetadata(tt.in); got != tt.want {
				t.Fatalf("MergeRegistrationMetadata() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

type recordUpdateStore struct {
	ObjectStore
	existing drs.DrsObject
	replaced []drs.DrsObject
}

func (s *recordUpdateStore) GetObject(context.Context, string) (*drs.DrsObject, error) {
	copy := s.existing
	return &copy, nil
}

func (s *recordUpdateStore) GetObjectsByChecksums(_ context.Context, checksums []string) (map[string][]drs.DrsObject, error) {
	return map[string][]drs.DrsObject{checksums[0]: {s.existing}}, nil
}

func (s *recordUpdateStore) GetPublicReadByIDs(context.Context, []string) (map[string]bool, error) {
	return map[string]bool{}, nil
}

func (s *recordUpdateStore) ReplaceObjects(_ context.Context, records []drs.DrsObject) error {
	s.replaced = append([]drs.DrsObject(nil), records...)
	return nil
}

func TestServiceUpdateRecordMergesRecordState(t *testing.T) {
	oldChecksum := strings.Repeat("a", 64)
	newChecksum := strings.Repeat("b", 64)
	created := time.Unix(10, 0)
	before := time.Now().UTC()
	name := "/nested/new-name.txt"
	description := "updated"
	controlled := []string{"/organization/org/project/proj"}
	existingControlled := []string{"/organization/org/project/proj"}
	store := &recordUpdateStore{existing: drs.DrsObject{
		Id:               "old-id",
		CreatedTime:      created,
		Name:             objectStringPtr("old.txt"),
		Checksums:        []drs.Checksum{{Type: "sha256", Checksum: oldChecksum}},
		ControlledAccess: &existingControlled,
	}}
	session := access.NewSession("local")
	session.AuthzEnforced = true
	session.SetAuthorizations(nil, map[string]map[string]bool{
		"/organization/org/project/proj": {"update": true},
	}, true)
	ctx := access.WithSession(context.Background(), session)
	got, err := NewService(store).UpdateObjectMetadata(ctx, "new-id", drs.DrsObject{
		Name:             &name,
		Description:      &description,
		ControlledAccess: &controlled,
		Checksums:        []drs.Checksum{{Type: "md5", Checksum: newChecksum}},
	}, Scope{}, nil)
	if err != nil {
		t.Fatalf("Service.UpdateObjectMetadata() error = %v", err)
	}
	if len(store.replaced) != 1 {
		t.Fatalf("ReplaceObjects() calls = %d, want 1", len(store.replaced))
	}
	if !reflect.DeepEqual(got, store.replaced[0]) {
		t.Fatalf("returned record = %#v, replaced record = %#v", got, store.replaced[0])
	}
	if got.Id != "new-id" || got.UpdatedTime == nil || got.UpdatedTime.Before(before) || got.UpdatedTime.After(time.Now()) || got.Name == nil || *got.Name != "new-name.txt" {
		t.Fatalf("unexpected identity/name: %#v", got)
	}
	if len(got.Checksums) != 2 || got.ControlledAccess == nil || (*got.ControlledAccess)[0] != controlled[0] {
		t.Fatalf("unexpected merged checksums/access: %#v", got)
	}
	if !got.CreatedTime.Equal(created) {
		t.Fatalf("CreatedTime changed: got %v, want %v", got.CreatedTime, created)
	}
}
