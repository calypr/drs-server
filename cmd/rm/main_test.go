package rm

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	internalapi "github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/cmd/cliauth"
	"github.com/spf13/cobra"
)

func TestRmCommandRequiresDID(t *testing.T) {
	root := newTestRoot(t, "")
	root.SetArgs([]string{"rm"})

	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--did is required") {
		t.Fatalf("expected missing did error, got %v", err)
	}
}

func TestRmCommandRequiresOrganizationForMultiScopeRecord(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/index/did-multi" {
			w.Header().Set("Content-Type", "application/json")
			resp := internalapi.InternalRecordResponse{
				Did:              "did-multi",
				ControlledAccess: &[]string{"/organization/a/project/one", "/organization/b/project/two"},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	root := newTestRoot(t, server.URL)
	root.SetArgs([]string{"rm", "--did", "did-multi"})

	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--organization is required") {
		t.Fatalf("expected organization-required error, got %v", err)
	}
}

func TestRmCommandRemovesControlledAccessForMultiScopeRecord(t *testing.T) {
	var gotReq internalapi.ControlledAccessRemoveRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/index/did-multi":
			w.Header().Set("Content-Type", "application/json")
			resp := internalapi.InternalRecordResponse{
				Did:              "did-multi",
				ControlledAccess: &[]string{"/organization/syfon/project/e2e", "/organization/other/project/x"},
			}
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodPost && r.URL.Path == "/index/did-multi/controlled-access/remove":
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
				t.Fatalf("decode remove request: %v", err)
			}
			resp := internalapi.InternalRecordResponse{
				Did:              "did-multi",
				ControlledAccess: &[]string{"/organization/other/project/x"},
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	root := newTestRoot(t, server.URL)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"rm", "--did", "did-multi", "--organization", "syfon", "--project", "e2e"})

	if err := root.Execute(); err != nil {
		t.Fatalf("rm execute: %v output=%s", err, out.String())
	}
	if gotReq.Resource != "/organization/syfon/project/e2e" {
		t.Fatalf("unexpected remove resource: %+v", gotReq)
	}
	if !strings.Contains(out.String(), "removed scoped access /organization/syfon/project/e2e from did-multi") {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func newTestRoot(t *testing.T, serverURL string) *cobra.Command {
	t.Helper()
	rmDID = ""
	rmOrganization = ""
	rmProject = ""

	root := &cobra.Command{Use: "syfon", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("server", serverURL, "server")
	cliauth.RegisterRootFlags(root.PersistentFlags())
	root.AddCommand(Cmd)
	t.Cleanup(func() {
		Cmd.SetArgs(nil)
		Cmd.SetOut(nil)
		Cmd.SetErr(nil)
	})
	return root
}

func TestRmCommandDeletesSingleScopeRecord(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/index/did-single":
			w.Header().Set("Content-Type", "application/json")
			resp := internalapi.InternalRecordResponse{
				Did:              "did-single",
				ControlledAccess: &[]string{"/organization/syfon/project/e2e"},
			}
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodPut && r.URL.Path == "/ga4gh/drs/v1/objects/did-single/delete":
			var body map[string]bool
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode delete request: %v", err)
			}
			if !body["delete_storage_data"] || !body["delete_object_metadata"] {
				t.Fatalf("unexpected delete body: %+v", body)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	root := newTestRoot(t, server.URL)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"rm", "--did", "did-single"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("rm execute: %v output=%s", err, out.String())
	}
	if !strings.Contains(out.String(), "removed did-single and attempted storage purge") {
		t.Fatalf("unexpected output: %s", out.String())
	}
}
