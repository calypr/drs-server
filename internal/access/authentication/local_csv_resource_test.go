package authentication

import (
	"os"
	"strings"
	"testing"
)

func TestLocalAuthzCSVRejectsMalformedRecognizedResource(t *testing.T) {
	path := t.TempDir() + "/authz.csv"
	contents := "username,password,methods,resource\n" +
		"alice,secret,read,/organization/cbds/project\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	_, err := loadLocalAuthzCSV(path)
	if err == nil {
		t.Fatal("malformed recognized resource unexpectedly loaded")
	}
	if !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "resource") {
		t.Fatalf("malformed resource error = %q, want line and resource context", err)
	}
}

func TestLocalAuthzCSVPreservesUnknownResourcePrefix(t *testing.T) {
	path := t.TempDir() + "/authz.csv"
	contents := "username,password,methods,resource\n" +
		"alice,secret,read,/data_file\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	store, err := loadLocalAuthzCSV(path)
	if err != nil {
		t.Fatalf("load csv: %v", err)
	}
	resources, _, ok := store.authzForSubject("alice")
	if !ok || len(resources) != 1 || resources[0] != "/data_file" {
		t.Fatalf("unknown resource = %v, want [/data_file]", resources)
	}
}
