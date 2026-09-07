package objects

import (
	"strings"
	"testing"
	"time"
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
	if got.Authorizations["org"][0] != "proj" {
		t.Fatalf("authorizations = %#v", got.Authorizations)
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
