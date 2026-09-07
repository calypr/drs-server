package objects

import (
	"strings"
	"testing"
	"time"
)

func TestEnforceCanonicalProjectScope(t *testing.T) {
	obj, err := EnforceCanonicalProjectScope(Record{
		Id:             "obj-1",
		Authorizations: map[string][]string{"other": {"proj"}},
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
	if len(merged.Checksums) != 2 || merged.Authorizations["org"][0] != "proj" {
		t.Fatalf("unexpected merged checksums/authz: %#v", merged)
	}
	if !merged.CreatedTime.Equal(created) {
		t.Fatalf("CreatedTime changed: got %v, want %v", merged.CreatedTime, created)
	}
}
