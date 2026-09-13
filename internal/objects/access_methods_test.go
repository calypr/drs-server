package objects

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
)

func TestCandidateDefaultAccessIDsDistinguishLocations(t *testing.T) {
	methods := []drs.AccessMethod{
		{Type: drs.AccessMethodTypeS3, AccessUrl: &drs.AccessURL{Url: "s3://bucket/one"}},
		{Type: drs.AccessMethodTypeS3, AccessUrl: &drs.AccessURL{Url: "s3://bucket/two"}},
	}
	aliases := []string{"id:explicit-id"}
	got, err := MaterializeCandidate(drs.DrsObjectCandidate{Aliases: &aliases, Checksums: []drs.Checksum{{Type: "sha256", Checksum: strings.Repeat("a", 64)}}, AccessMethods: &methods}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	first, second := (*got.AccessMethods)[0], (*got.AccessMethods)[1]
	if first.AccessId == nil || second.AccessId == nil || *first.AccessId == *second.AccessId {
		t.Fatalf("distinct locations received duplicate access IDs: %+v", *got.AccessMethods)
	}
}

func TestCanonicalMergePreservesAccessMethodIDsAndPayloads(t *testing.T) {
	headers := []string{"Authorization: Bearer synthetic-token"}
	methods := []drs.AccessMethod{
		{Type: drs.AccessMethodTypeHttps, AccessId: objectStringPtr("resolver-one")},
		{Type: drs.AccessMethodTypeHttps, AccessId: objectStringPtr("resolver-two")},
		{Type: drs.AccessMethodTypeHttps, AccessId: objectStringPtr("signed-download"), AccessUrl: &drs.AccessURL{Url: "https://example.test/object", Headers: &headers}, Region: objectStringPtr("us-west-2")},
		{Type: drs.AccessMethodTypeHttps, AccessId: objectStringPtr("public-download"), AccessUrl: &drs.AccessURL{Url: "https://example.test/object"}},
	}
	group := []drs.DrsObject{
		{Id: "one", Checksums: []drs.Checksum{{Type: "sha256", Checksum: strings.Repeat("a", 64)}}, AccessMethods: &methods},
		{Id: "two", Checksums: []drs.Checksum{{Type: "sha256", Checksum: strings.Repeat("a", 64)}}},
	}
	merged := canonicalizeContentObjects(group, nil)
	if len(merged) != 1 || merged[0].AccessMethods == nil {
		t.Fatalf("merged objects = %+v", merged)
	}
	got := make(map[string]drs.AccessMethod)
	for _, method := range *merged[0].AccessMethods {
		if method.AccessId != nil {
			got[*method.AccessId] = method
		}
	}
	for _, want := range methods {
		if !reflect.DeepEqual(got[*want.AccessId], want) {
			t.Errorf("access method %q = %+v, want %+v", *want.AccessId, got[*want.AccessId], want)
		}
	}
}

func TestCanonicalMergeRetainsConflictsForPersistenceValidation(t *testing.T) {
	headers := []string{"Authorization: Bearer synthetic-token"}
	first := []drs.AccessMethod{{Type: drs.AccessMethodTypeHttps, AccessId: objectStringPtr("shared-id"), AccessUrl: &drs.AccessURL{Url: "https://example.test/object"}}}
	second := []drs.AccessMethod{{Type: drs.AccessMethodTypeHttps, AccessId: objectStringPtr("shared-id"), AccessUrl: &drs.AccessURL{Url: "https://example.test/object", Headers: &headers}}}
	group := []drs.DrsObject{
		{Id: "one", Checksums: []drs.Checksum{{Type: "sha256", Checksum: strings.Repeat("a", 64)}}, AccessMethods: &first},
		{Id: "two", Checksums: []drs.Checksum{{Type: "sha256", Checksum: strings.Repeat("a", 64)}}, AccessMethods: &second},
	}
	merged := canonicalizeContentObjects(group, nil)
	if len(merged) != 1 || merged[0].AccessMethods == nil || len(*merged[0].AccessMethods) != 2 {
		t.Fatalf("canonical merge hid an access-method conflict: %+v", merged)
	}
}
