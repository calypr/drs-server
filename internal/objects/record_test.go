package objects

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/access"
)

func TestCandidateToRecordPreservesRegistrationContract(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	now := time.Unix(123, 0).UTC()
	name := `/nested/path/object.bin`
	size := int64(42)
	controlled := []string{"/organization/org/project/proj"}
	aliases := []string{"legacy-name", "id:explicit-id"}
	accessID := "provided"
	contents := []Content{{Name: "nested"}}
	candidate := Candidate{
		Name:             &name,
		Size:             &size,
		Aliases:          &aliases,
		Checksums:        &[]Checksum{{Type: "sha256", Checksum: checksum}},
		ControlledAccess: &controlled,
		Contents:         &contents,
		AccessMethods: &[]AccessMethod{{
			AccessId:  &accessID,
			Type:      "https",
			AccessUrl: &AccessURL{Url: "https://storage.example/object.bin"},
		}, {
			Type:      "s3",
			AccessUrl: &AccessURL{Url: "s3://bucket/object.bin"},
		}},
	}

	got, err := CandidateToRecord(candidate, now)
	if err != nil {
		t.Fatalf("CandidateToRecord() error = %v", err)
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
	if got.Contents != nil {
		t.Fatalf("Contents was persisted: %#v; baseline contract omits it", got.Contents)
	}
	if got.ControlledAccess == nil || len(*got.ControlledAccess) != 1 || (*got.ControlledAccess)[0] != controlled[0] {
		t.Fatalf("controlled access = %v, want %v", got.ControlledAccess, controlled)
	}
	if resources := AccessResources(&got); len(resources) != 1 || resources[0] != controlled[0] {
		t.Fatalf("controlled access = %#v", resources)
	}
	if got.AccessMethods == nil || len(*got.AccessMethods) != 2 || (*got.AccessMethods)[0].AccessId == nil || *(*got.AccessMethods)[0].AccessId != accessID || (*got.AccessMethods)[1].AccessId == nil || *(*got.AccessMethods)[1].AccessId != "s3" {
		t.Fatalf("access IDs = %#v", got.AccessMethods)
	}
}

func TestCandidateToRecordUsesDeterministicScopedIDAndDefaultName(t *testing.T) {
	checksum := strings.Repeat("b", 64)
	controlled := []string{"/organization/org/project/proj"}
	candidate := Candidate{
		ControlledAccess: &controlled,
		Checksums:        &[]Checksum{{Type: "sha256", Checksum: checksum}},
		AccessMethods:    &[]AccessMethod{{Type: "s3", AccessUrl: &AccessURL{Url: "s3://bucket/object"}}},
	}

	first, err := CandidateToRecord(candidate, time.Unix(1, 0))
	if err != nil {
		t.Fatalf("first CandidateToRecord() error = %v", err)
	}
	second, err := CandidateToRecord(candidate, time.Unix(2, 0))
	if err != nil {
		t.Fatalf("second CandidateToRecord() error = %v", err)
	}
	if first.Id == "" || first.Id != second.Id {
		t.Fatalf("IDs are not deterministic: %q vs %q", first.Id, second.Id)
	}
	if first.Name == nil || *first.Name != checksum {
		t.Fatalf("default name = %v, want checksum", first.Name)
	}
}

func TestCandidateToRecordRejectsMissingAccessMethods(t *testing.T) {
	checksums := []Checksum{{Type: "sha256", Checksum: strings.Repeat("c", 64)}}
	if _, err := CandidateToRecord(Candidate{Checksums: &checksums}, time.Unix(0, 0)); err == nil || !strings.Contains(err.Error(), "access method") {
		t.Fatalf("error = %v, want access-method validation", err)
	}
}

func TestChecksumHelpers(t *testing.T) {
	checksums := []Checksum{{Type: "sha-256", Checksum: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, {Type: "md5", Checksum: "m"}}
	if !LooksLikeSHA256("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatal("expected SHA256 shape")
	}
	if LooksLikeSHA256("not-a-hash") {
		t.Fatal("unexpected SHA256 shape")
	}
	if typ, value := ParseHashQuery("sha-256:abc", ""); typ != "sha256" || value != "abc" {
		t.Fatalf("parsed hash = %q/%q", typ, value)
	}
	if value, ok := CanonicalSHA256(checksums); !ok || value != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("canonical hash = %q/%v", value, ok)
	}
	if !RecordHasChecksumTypeAndValue(Record{Checksums: checksums}, "md5", "m") {
		t.Fatal("expected md5 match")
	}
	merged := mergeAdditionalChecksums(checksums, []Checksum{{Type: "SHA256", Checksum: "other"}, {Type: "etag", Checksum: "e"}})
	if len(merged) != 3 || merged[2].Type != "etag" {
		t.Fatalf("merged checksums = %+v", merged)
	}
	if _, ok, err := ValidateCanonicalSHA256([]Checksum{{Type: "sha256", Checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, {Type: "sha256", Checksum: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}); err == nil || ok {
		t.Fatal("expected conflicting SHA256 values")
	}
	if normalized, ok := NormalizeSHA256Query("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); !ok || normalized == "" {
		t.Fatal("expected normalized SHA256 query")
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
	record := Record{
		Id:               "  did:example:1  ",
		CreatedTime:      created,
		UpdatedTime:      &updated,
		Name:             &name,
		Checksums:        []Checksum{{Type: "SHA-256", Checksum: "sha256:" + valid}, {Type: "md5", Checksum: "kept"}, {Type: "sha256", Checksum: invalid}},
		ControlledAccess: &controlled,
		NameAliases:      []string{"/primary.txt", `other\alias.txt`, "alias.txt", `other\alias.txt`, ""},
	}

	got, err := NormalizeRecord(record, fallback)
	if err != nil {
		t.Fatalf("NormalizeRecord() error = %v", err)
	}
	wantControlled := []string{"/organization/org/project/proj", "/organization/org"}
	want := Record{
		Id:               "did:example:1",
		CreatedTime:      created,
		UpdatedTime:      &updated,
		Name:             objectStringPtr("primary.txt"),
		Version:          objectStringPtr("1"),
		Checksums:        []Checksum{{Type: "sha256", Checksum: strings.ToLower(valid)}, {Type: "md5", Checksum: "kept"}, {Type: "sha256", Checksum: invalid}},
		ControlledAccess: &wantControlled,
		NameAliases:      []string{"alias.txt"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeRecord() = %#v, want %#v", got, want)
	}

	got, err = NormalizeRecord(Record{Id: "did:example:2"}, fallback)
	if err != nil {
		t.Fatalf("NormalizeRecord() zero times error = %v", err)
	}
	if !got.CreatedTime.Equal(fallback.UTC()) || got.UpdatedTime == nil || !got.UpdatedTime.Equal(fallback.UTC()) {
		t.Fatalf("NormalizeRecord() zero times = %#v, want fallback %v", got, fallback.UTC())
	}
}

func TestNormalizeRecordRejectsMissingID(t *testing.T) {
	if _, err := NormalizeRecord(Record{Id: "  "}, time.Time{}); err == nil || err.Error() != "did is required" {
		t.Fatalf("NormalizeRecord() error = %v, want did is required", err)
	}
}

func TestEnforceCanonicalProjectScope(t *testing.T) {
	initial := []string{"/organization/other/project/proj"}
	obj, err := enforceCanonicalProjectScope(Record{
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
	existing Record
	replaced []Record
}

func (s *recordUpdateStore) GetObject(context.Context, string) (*Record, error) {
	copy := s.existing
	return &copy, nil
}

func (s *recordUpdateStore) GetObjectsByChecksum(context.Context, string) ([]Record, error) {
	return []Record{s.existing}, nil
}

func (s *recordUpdateStore) ReplaceObjects(_ context.Context, records []Record) error {
	s.replaced = append([]Record(nil), records...)
	return nil
}

func TestServiceUpdateRecordMergesRecordState(t *testing.T) {
	oldChecksum := strings.Repeat("a", 64)
	newChecksum := strings.Repeat("b", 64)
	created := time.Unix(10, 0)
	now := time.Unix(20, 0)
	name := "/nested/new-name.txt"
	description := "updated"
	controlled := []string{"/organization/org/project/proj"}
	existingControlled := []string{"/organization/org/project/proj"}
	store := &recordUpdateStore{existing: Record{
		Id:               "old-id",
		CreatedTime:      created,
		Name:             objectStringPtr("old.txt"),
		Checksums:        []Checksum{{Type: "sha256", Checksum: oldChecksum}},
		ControlledAccess: &existingControlled,
	}}
	session := access.NewSession("local")
	session.AuthzEnforced = true
	session.SetAuthorizations(nil, map[string]map[string]bool{
		"/organization/org/project/proj": {"update": true},
	}, true)
	ctx := access.WithSession(context.Background(), session)
	got, err := NewService(store).UpdateRecord(ctx, "new-id", Record{
		Name:             &name,
		Description:      &description,
		ControlledAccess: &controlled,
		Checksums:        []Checksum{{Type: "md5", Checksum: newChecksum}},
	}, nil, now)
	if err != nil {
		t.Fatalf("Service.UpdateRecord() error = %v", err)
	}
	if len(store.replaced) != 1 {
		t.Fatalf("ReplaceObjects() calls = %d, want 1", len(store.replaced))
	}
	if !reflect.DeepEqual(got, store.replaced[0]) {
		t.Fatalf("returned record = %#v, replaced record = %#v", got, store.replaced[0])
	}
	if got.Id != "new-id" || got.UpdatedTime == nil || !got.UpdatedTime.Equal(now) || got.Name == nil || *got.Name != "new-name.txt" {
		t.Fatalf("unexpected identity/name: %#v", got)
	}
	if len(got.Checksums) != 2 || got.ControlledAccess == nil || (*got.ControlledAccess)[0] != controlled[0] {
		t.Fatalf("unexpected merged checksums/access: %#v", got)
	}
	if !got.CreatedTime.Equal(created) {
		t.Fatalf("CreatedTime changed: got %v, want %v", got.CreatedTime, created)
	}
}
