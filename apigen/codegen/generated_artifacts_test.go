package codegen_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/internalapi"
)

func TestGenerationExtensionsStayOutOfCanonicalDRSSpec(t *testing.T) {
	var _ *drs.AccessURL = drs.AccessMethod{}.AccessUrl
	var _ *drs.Authorizations = drs.AccessMethod{}.Authorizations
	var _ *string = drs.DrsObject{}.Did
	var _ *[]string = drs.DrsObject{}.NameAliases
	var _ *[]drs.Checksum = internalapi.InternalRecordResponse{}.Checksums

	spec, err := os.ReadFile("../openapi/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"did", "name_aliases", "public_read", "public_read_policy_known"} {
		if bytes.Contains(spec, []byte("        "+field+":")) {
			t.Fatalf("canonical DRS schema contains generation-only field %q", field)
		}
	}
}
