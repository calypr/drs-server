package buckets

import (
	"context"
	"errors"
	"sort"
	"testing"
)

func TestSaveS3CredentialInvalidatesCredentialIDAndPhysicalAliasesAfterCommit(t *testing.T) {
	invalidator := &recordingInvalidator{}
	service, credentials, _ := newFakeService(nil, nil, &fakeVisibilityQuery{}, nil, invalidator)
	credential := &Credential{
		CredentialID: " credential-id ",
		Bucket:       " physical-bucket ",
		Provider:     "s3",
	}

	if err := service.SaveS3Credential(context.Background(), credential); err != nil {
		t.Fatalf("SaveS3Credential: %v", err)
	}
	if credentials.saveCalls != 1 || credentials.lastSaved == nil {
		t.Fatalf("save was not delegated: calls=%d saved=%v", credentials.saveCalls, credentials.lastSaved)
	}
	got := invalidator.snapshot()
	sort.Strings(got)
	want := []string{"credential-id", "physical-bucket"}
	if !equalStrings(got, want) {
		t.Fatalf("invalidated aliases=%v, want %v", got, want)
	}
}

func TestSaveS3CredentialDoesNotInvalidateAfterFailedCommit(t *testing.T) {
	invalidator := &recordingInvalidator{}
	service, credentials, _ := newFakeService(nil, nil, &fakeVisibilityQuery{}, nil, invalidator)
	credentials.saveErr = errors.New("write failed")

	err := service.SaveS3Credential(context.Background(), &Credential{CredentialID: "id-a", Bucket: "bucket-a"})
	if !errors.Is(err, credentials.saveErr) {
		t.Fatalf("SaveS3Credential error=%v, want %v", err, credentials.saveErr)
	}
	if got := invalidator.snapshot(); len(got) != 0 {
		t.Fatalf("failed save invalidated aliases %v", got)
	}
}

func TestDeleteS3CredentialInvalidatesRequestedResolvedAndPhysicalAliases(t *testing.T) {
	credential := Credential{CredentialID: "credential-id", Bucket: "physical-bucket"}
	invalidator := &recordingInvalidator{}
	service, credentials, _ := newFakeService([]Credential{credential}, nil, &fakeVisibilityQuery{}, nil, invalidator)

	if err := service.DeleteS3Credential(context.Background(), "credential-id"); err != nil {
		t.Fatalf("DeleteS3Credential: %v", err)
	}
	if credentials.lastGet != "credential-id" || credentials.lastDeleted != "credential-id" {
		t.Fatalf("delete resolution was not preserved: get=%q delete=%q", credentials.lastGet, credentials.lastDeleted)
	}
	got := invalidator.snapshot()
	sort.Strings(got)
	want := []string{"credential-id", "physical-bucket"}
	if !equalStrings(got, want) {
		t.Fatalf("invalidated aliases=%v, want %v", got, want)
	}
}

func TestDeleteS3CredentialByPhysicalAliasIncludesBothIdentityForms(t *testing.T) {
	credential := Credential{CredentialID: "credential-id", Bucket: "physical-bucket"}
	invalidator := &recordingInvalidator{}
	service, _, _ := newFakeService([]Credential{credential}, nil, &fakeVisibilityQuery{}, nil, invalidator)

	if err := service.DeleteS3Credential(context.Background(), "physical-bucket"); err != nil {
		t.Fatalf("DeleteS3Credential: %v", err)
	}
	got := invalidator.snapshot()
	sort.Strings(got)
	if want := []string{"credential-id", "physical-bucket"}; !equalStrings(got, want) {
		t.Fatalf("invalidated aliases=%v, want %v", got, want)
	}
}

func TestDeleteS3CredentialDoesNotInvalidateAfterFailedDelete(t *testing.T) {
	credential := Credential{CredentialID: "credential-id", Bucket: "physical-bucket"}
	invalidator := &recordingInvalidator{}
	service, credentials, _ := newFakeService([]Credential{credential}, nil, &fakeVisibilityQuery{}, nil, invalidator)
	credentials.deleteErr = errors.New("delete failed")

	err := service.DeleteS3Credential(context.Background(), "physical-bucket")
	if !errors.Is(err, credentials.deleteErr) {
		t.Fatalf("DeleteS3Credential error=%v, want %v", err, credentials.deleteErr)
	}
	if got := invalidator.snapshot(); len(got) != 0 {
		t.Fatalf("failed delete invalidated aliases %v", got)
	}
}

func TestSaveS3CredentialWithoutExplicitIDUsesPhysicalAlias(t *testing.T) {
	invalidator := &recordingInvalidator{}
	service, _, _ := newFakeService(nil, nil, &fakeVisibilityQuery{}, nil, invalidator)

	if err := service.SaveS3Credential(context.Background(), &Credential{Bucket: "physical-bucket"}); err != nil {
		t.Fatalf("SaveS3Credential: %v", err)
	}
	if got := invalidator.snapshot(); !equalStrings(got, []string{"physical-bucket"}) {
		t.Fatalf("invalidated aliases=%v, want [physical-bucket]", got)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
