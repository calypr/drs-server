package lfs

import (
	"strings"
	"testing"

	generated "github.com/calypr/syfon/apigen/server/lfsapi"
)

func TestFromGeneratedCandidatePreservesLegacyFields(t *testing.T) {
	size := int64(42)
	id := "lfs-explicit-id"
	typ := "s3"
	region := "legacy-cloud"
	url := "s3://bucket/object.bin"
	candidate := generated.DrsObjectCandidate{
		Id:   &id,
		Name: stringPtr("object.bin"),
		Size: &size,
		Checksums: &[]generated.Checksum{{
			Type: "sha256", Checksum: strings.Repeat("a", 64),
		}},
		AccessMethods: &[]generated.AccessMethod{{
			AccessId:  stringPtr("s3"),
			Type:      &typ,
			Region:    &region,
			AccessUrl: &generated.AccessMethodAccessUrl{Url: &url},
			Authorizations: &generated.AccessMethodAuthorizations{
				BearerAuthIssuers: stringSlicePtr([]string{"issuer"}),
			},
		}},
	}

	got := FromGeneratedCandidate(candidate)
	if got.Aliases == nil || len(*got.Aliases) != 1 || (*got.Aliases)[0] != "id:"+id {
		t.Fatalf("explicit id alias = %#v", got.Aliases)
	}
	if got.AccessMethods == nil || len(*got.AccessMethods) != 1 {
		t.Fatalf("access methods = %#v", got.AccessMethods)
	}
	method := (*got.AccessMethods)[0]
	if method.Cloud == nil || *method.Cloud != region || method.Region != nil {
		t.Fatalf("region/cloud mapping = %#v", method)
	}
	if method.Authorizations != nil {
		t.Fatalf("dropped legacy fields were retained: %#v", method)
	}
	if method.AccessUrl == nil || method.AccessUrl.Url != url {
		t.Fatalf("access URL mapping = %#v", method.AccessUrl)
	}
}

func TestFromGeneratedCandidatePreservesExplicitZeroSize(t *testing.T) {
	size := int64(0)
	got := FromGeneratedCandidate(generated.DrsObjectCandidate{Size: &size})
	if got.Size == nil || *got.Size != 0 {
		t.Fatalf("explicit zero size = %#v, want nonnil pointer to zero", got.Size)
	}
}

func TestFromGeneratedCandidateDerivesAliasFromSHA256(t *testing.T) {
	oid := strings.Repeat("b", 64)
	got := FromGeneratedCandidate(generated.DrsObjectCandidate{
		Checksums: &[]generated.Checksum{{Type: "sha256", Checksum: oid}},
	})
	if got.Aliases == nil || len(*got.Aliases) != 1 || (*got.Aliases)[0] != "id:"+oid {
		t.Fatalf("sha256 alias = %#v", got.Aliases)
	}
}

func stringPtr(value string) *string { return &value }

func stringSlicePtr(value []string) *[]string { return &value }
