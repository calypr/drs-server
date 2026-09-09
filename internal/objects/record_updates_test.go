package objects

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

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
	obj, err := EnforceCanonicalProjectScope(Record{
		Id: "obj-1", ControlledAccess: &initial,
	}, "org", "proj")
	if err != nil {
		t.Fatalf("EnforceCanonicalProjectScope() error = %v", err)
	}
	got := AccessResources(&obj)
	want := []string{"/organization/other/project/proj", "/organization/org/project/proj"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("AccessResources() = %#v, want %#v", got, want)
	}
}

func TestMergeRecordUpdatePreservesAndMergesRecordState(t *testing.T) {
	oldChecksum := strings.Repeat("a", 64)
	newChecksum := strings.Repeat("b", 64)
	created := time.Unix(10, 0)
	now := time.Unix(20, 0)
	name := "/nested/new-name.txt"
	description := "updated"
	controlled := []string{"/organization/org/project/proj"}
	update := Record{
		Name:             &name,
		Description:      &description,
		ControlledAccess: &controlled,
		Checksums:        []Checksum{{Type: "md5", Checksum: newChecksum}},
	}
	existing := Record{
		Id:          "old-id",
		CreatedTime: created,
		Name:        objectStringPtr("old.txt"),
		Checksums:   []Checksum{{Type: "sha256", Checksum: oldChecksum}},
	}

	merged, err := MergeRecordUpdate(existing, update, "new-id", now)
	if err != nil {
		t.Fatalf("MergeRecordUpdate() error = %v", err)
	}
	if merged.Id != "new-id" || !merged.UpdatedTime.Equal(now) || merged.Name == nil || *merged.Name != "new-name.txt" {
		t.Fatalf("unexpected identity/name: %#v", merged)
	}
	if len(merged.Checksums) != 2 || merged.ControlledAccess == nil || (*merged.ControlledAccess)[0] != controlled[0] {
		t.Fatalf("unexpected merged checksums/access: %#v", merged)
	}
	if !merged.CreatedTime.Equal(created) {
		t.Fatalf("CreatedTime changed: got %v, want %v", merged.CreatedTime, created)
	}
}
