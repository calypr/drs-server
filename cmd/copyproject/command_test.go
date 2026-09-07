package copyproject

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/bucketapi"
	drsapi "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/spf13/cobra"
)

func TestCopyProjectContinuesAfterRecordErrorAndReportsCounts(t *testing.T) {
	sourceDir := t.TempDir()
	sourcePath := path.Join(sourceDir, "source.txt")
	if err := os.WriteFile(sourcePath, []byte("copy-project payload"), 0o600); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	targetPath := path.Join(t.TempDir(), "target.txt")
	sourceURL := fileURL(t, sourcePath)
	targetURL := fileURL(t, targetPath)
	size := int64(len("copy-project payload"))

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/data/buckets":
			writeCommandJSON(w, http.StatusOK, bucketapi.BucketsResponse{S3BUCKETS: map[string]bucketapi.BucketMetadata{"source-bucket": {}}})
		case r.Method == http.MethodGet && r.URL.Path == "/data/buckets/source-bucket/scopes":
			writeCommandJSON(w, http.StatusOK, []bucketapi.BucketScopeResponse{
				{Organization: "source-org", Path: stringPtr("s3://source-bucket/organizations/source-org")},
				{Organization: "source-org", ProjectId: "source-project", Path: stringPtr("s3://source-bucket/organizations/source-org/projects/source-project")},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/index":
			records := []internalapi.InternalRecord{
				{Did: "did-copy", Name: stringPtr("copy.txt"), Size: &size},
				{Did: "did-skip", Name: stringPtr("skip.txt"), Size: &size},
			}
			writeCommandJSON(w, http.StatusOK, internalapi.ListRecordsResponse{Records: &records})
		case r.Method == http.MethodGet && r.URL.Path == "/data/download/did-copy":
			writeCommandJSON(w, http.StatusOK, internalapi.InternalSignedURL{Url: &sourceURL})
		case r.Method == http.MethodGet && r.URL.Path == "/data/download/did-skip":
			missingURL := fileURL(t, path.Join(sourceDir, "missing.txt"))
			writeCommandJSON(w, http.StatusOK, internalapi.InternalSignedURL{Url: &missingURL})
		case r.Method == http.MethodGet && r.URL.Path == "/ga4gh/drs/v1/objects/did-copy":
			accessMethods := []drsapi.AccessMethod{{Type: drsapi.AccessMethodType("s3")}}
			writeCommandJSON(w, http.StatusOK, drsapi.DrsObject{Id: "did-copy", Size: size, AccessMethods: &accessMethods})
		case r.Method == http.MethodGet && r.URL.Path == "/ga4gh/drs/v1/objects/did-skip":
			accessMethods := []drsapi.AccessMethod{{Type: drsapi.AccessMethodType("s3")}}
			writeCommandJSON(w, http.StatusOK, drsapi.DrsObject{Id: "did-skip", Size: size, AccessMethods: &accessMethods})
		default:
			http.NotFound(w, r)
		}
	}))
	defer source.Close()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/data/buckets":
			writeCommandJSON(w, http.StatusOK, bucketapi.BucketsResponse{S3BUCKETS: map[string]bucketapi.BucketMetadata{"target-bucket": {}}})
		case r.Method == http.MethodGet && r.URL.Path == "/data/buckets/target-bucket/scopes":
			writeCommandJSON(w, http.StatusOK, []bucketapi.BucketScopeResponse{
				{Organization: "target-org", Path: stringPtr("s3://target-bucket/organizations/target-org")},
				{Organization: "target-org", ProjectId: "target-project", Path: stringPtr("s3://target-bucket/organizations/target-org/projects/target-project")},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/data/upload/did-copy":
			writeCommandJSON(w, http.StatusOK, internalapi.InternalSignedURL{Url: &targetURL})
		case r.Method == http.MethodGet && r.URL.Path == "/ga4gh/drs/v1/objects/did-copy":
			http.NotFound(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/ga4gh/drs/v1/objects/register":
			writeCommandJSON(w, http.StatusCreated, drsapi.N201ObjectsCreated{})
		case r.Method == http.MethodPut && r.URL.Path == "/ga4gh/drs/v1/objects/did-copy/access-methods":
			writeCommandJSON(w, http.StatusOK, drsapi.DrsObject{Id: "did-copy"})
		case r.Method == http.MethodGet && r.URL.Path == "/index/did-copy":
			http.NotFound(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/index":
			writeCommandJSON(w, http.StatusCreated, internalapi.InternalRecordResponse{Did: "did-copy"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	previousFlags := copyFlags
	copyFlags.SourceProfile = ""
	copyFlags.SourceToken = ""
	copyFlags.SourceBasicUser = ""
	copyFlags.SourceBasicPassword = ""
	copyFlags.TargetServerURL = target.URL
	copyFlags.TargetProfile = ""
	copyFlags.TargetToken = ""
	copyFlags.TargetBasicUser = ""
	copyFlags.TargetBasicPassword = ""
	copyFlags.IndividualDID = ""
	defer func() { copyFlags = previousFlags }()

	root := &cobra.Command{Use: "syfon"}
	root.PersistentFlags().String("server", source.URL, "")
	command := &cobra.Command{Use: Cmd.Use, RunE: Cmd.RunE}
	root.AddCommand(command)
	root.SetContext(context.Background())
	command.SetContext(context.Background())
	var output, errorsOutput bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&errorsOutput)
	if err := command.RunE(command, []string{"source-org/source-project", "target-org/target-project"}); err != nil {
		t.Fatalf("copy project returned error: %v", err)
	}
	if got, err := os.ReadFile(targetPath); err != nil {
		t.Fatalf("ReadFile target: %v", err)
	} else if string(got) != "copy-project payload" {
		t.Fatalf("target content = %q", got)
	}
	wantSummary := "Successfully copied project source-org/source-project to target-org/target-project (1 copied, 1 skipped, 2 total).\n"
	if !strings.Contains(output.String(), wantSummary) {
		t.Fatalf("copy output = %q; want summary %q", output.String(), wantSummary)
	}
	if !strings.Contains(errorsOutput.String(), "warning: skipping did-skip: failed to download file did-skip") {
		t.Fatalf("copy errors = %q; want skip warning", errorsOutput.String())
	}
}

func writeCommandJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
