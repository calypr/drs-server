package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/transfer"
	"github.com/calypr/syfon/client/transfer/engine"
)

func TestDataServiceMultipartCompletionRecovery(t *testing.T) {
	t.Setenv("DATA_CLIENT_CACHE_DIR", t.TempDir())

	source := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(source, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}

	var completionCalls atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/data/multipart/init":
			uploadID, guid := "upload-id", "guid"
			writeJSON(t, w, http.StatusOK, internalapi.InternalMultipartInitOutput{UploadId: &uploadID, Guid: &guid})
		case r.Method == http.MethodPost && r.URL.Path == "/data/multipart/upload":
			presignedURL := server.URL + "/multipart-part"
			writeJSON(t, w, http.StatusOK, internalapi.InternalMultipartUploadOutput{PresignedUrl: &presignedURL})
		case r.Method == http.MethodPut && r.URL.Path == "/multipart-part":
			w.Header().Set("ETag", `"etag-1"`)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/data/multipart/complete":
			if completionCalls.Add(1) == 1 {
				writeJSON(t, w, http.StatusInternalServerError, map[string]any{"error": map[string]string{"message": "completion failed"}})
				return
			}
			writeJSON(t, w, http.StatusOK, map[string]string{"object_url": "s3://bucket/object"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	service := NewDataService(mustInternalClient(t, server.URL), http.DefaultClient, discardLogger(), nil)
	uploader := &engine.GenericUploader{Backend: service}
	req := transfer.TransferRequest{
		SourcePath:     source,
		ObjectKey:      "object",
		GUID:           "guid",
		Metadata:       common.FileMetadata{},
		ForceMultipart: true,
	}

	if err := uploader.Upload(context.Background(), req); err == nil {
		t.Fatal("first completion failure was swallowed")
	}
	if got := completionCalls.Load(); got != 1 {
		t.Fatalf("completion calls after first attempt = %d, want 1", got)
	}
	checkpoint, err := engine.CheckpointPath(source, req.GUID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(checkpoint); err != nil {
		t.Fatalf("completion failure did not retain checkpoint: %v", err)
	}

	if err := uploader.Upload(context.Background(), req); err != nil {
		t.Fatalf("completion recovery failed through DataService: %v", err)
	}
	if got := completionCalls.Load(); got != 2 {
		t.Fatalf("completion calls after recovery = %d, want 2", got)
	}
	if _, err := os.Stat(checkpoint); !os.IsNotExist(err) {
		t.Fatalf("recovered completion checkpoint still exists: %v", err)
	}
}
