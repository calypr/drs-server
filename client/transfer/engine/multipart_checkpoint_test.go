package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/transfer"
)

type countedMultipartBackend struct {
	*fakeBackend
	completeCalls atomic.Int32
	replaySafe    bool
}

func (b *countedMultipartBackend) MultipartComplete(ctx context.Context, key, uploadID string, parts []transfer.MultipartPart) error {
	b.completeCalls.Add(1)
	return b.fakeBackend.MultipartComplete(ctx, key, uploadID, parts)
}

func (b *countedMultipartBackend) MultipartCompletionReplaySafe(context.Context, string, string, []transfer.MultipartPart) bool {
	return b.replaySafe
}

func testMultipartRequest(source string, metadata common.FileMetadata) transfer.TransferRequest {
	return transfer.TransferRequest{
		SourcePath:     source,
		ObjectKey:      "object",
		GUID:           "guid",
		Bucket:         "bucket",
		Metadata:       metadata,
		ForceMultipart: true,
	}
}

func writeCheckpointForRequest(t *testing.T, uploader *GenericUploader, req transfer.TransferRequest, state *uploaderResumeState) string {
	t.Helper()
	path, err := CheckpointPath(req.SourcePath, req.GUID)
	if err != nil {
		t.Fatal(err)
	}
	if err := uploader.saveState(path, state); err != nil {
		t.Fatal(err)
	}
	return path
}

func checkpointStateForRequest(t *testing.T, req transfer.TransferRequest, phase uploadCheckpointPhase, info os.FileInfo) *uploaderResumeState {
	t.Helper()
	return &uploaderResumeState{
		SourcePath:         req.SourcePath,
		ObjectKey:          effectiveObjectKey(req),
		GUID:               req.GUID,
		Bucket:             req.Bucket,
		FileSize:           info.Size(),
		FileModUnixNano:    info.ModTime().UnixNano(),
		ChunkSize:          OptimalChunkSize(info.Size()),
		UploadID:           "upload",
		RoutingFingerprint: uploadRoutingFingerprint(req.Metadata),
		Phase:              phase,
		Completed:          map[int]string{1: "etag-1"},
	}
}

func TestUploadRoutingFingerprintCanonicalizesScopes(t *testing.T) {
	first := common.FileMetadata{Authorizations: map[string][]string{
		" org-b ": {" project-2 ", "project-1", "project-1"},
		"org-a":   {"project-a"},
	}}
	second := common.FileMetadata{Authorizations: map[string][]string{
		"org-a": {" project-a "},
		"org-b": {"project-1", "project-2"},
	}}
	if got, want := uploadRoutingFingerprint(first), uploadRoutingFingerprint(second); got != want {
		t.Fatalf("scope fingerprint changed with map/order formatting: %q != %q", got, want)
	}
	duplicateOrganization := common.FileMetadata{Authorizations: map[string][]string{
		"org-b":   {"project-1"},
		" org-b ": {" project-2 ", "project-1"},
		"org-a":   {"project-a"},
	}}
	if got, want := uploadRoutingFingerprint(duplicateOrganization), uploadRoutingFingerprint(second); got != want {
		t.Fatalf("scope fingerprint changed with duplicate canonical organization: %q != %q", got, want)
	}
	if uploadRoutingFingerprint(first) == uploadRoutingFingerprint(common.FileMetadata{Authorizations: map[string][]string{"org-b": {"project-3"}}}) {
		t.Fatal("changed project scope reused the same fingerprint")
	}
	if uploadRoutingFingerprint(common.FileMetadata{}) == "" {
		t.Fatal("unscoped routing fingerprint is empty")
	}
}

func TestUploadCheckpointAtomicReaderVisibility(t *testing.T) {
	t.Setenv("DATA_CLIENT_CACHE_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	uploader := &GenericUploader{}
	state := &uploaderResumeState{Phase: uploadCheckpointUploading, UploadID: "one", Completed: map[int]string{}}
	if err := uploader.saveState(path, state); err != nil {
		t.Fatal(err)
	}

	var malformed atomic.Bool
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for i := 0; i < 300; i++ {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var got uploaderResumeState
			if err := json.Unmarshal(data, &got); err != nil {
				malformed.Store(true)
				return
			}
		}
	}()
	for i := 0; i < 300; i++ {
		state.UploadID = fmt.Sprintf("upload-%d", i)
		if err := uploader.saveState(path, state); err != nil {
			t.Fatal(err)
		}
	}
	readers.Wait()
	if malformed.Load() {
		t.Fatal("external reader observed malformed checkpoint JSON")
	}
}

func TestMultipartCheckpointAgeDoesNotDiscardProviderUploadIdentity(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("DATA_CLIENT_CACHE_DIR", cache)
	base := filepath.Join(cache, "syfon", "multipart")
	if _, err := CheckpointPath("seed", "seed"); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-25 * time.Hour)
	uploader := &GenericUploader{}
	for _, test := range []struct {
		name  string
		phase uploadCheckpointPhase
	}{
		{name: "uploading", phase: uploadCheckpointUploading},
		{name: "completing", phase: uploadCheckpointCompleting},
		{name: "completed", phase: uploadCheckpointCompleted},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(base, test.name+".json")
			if err := uploader.saveState(path, &uploaderResumeState{Phase: test.phase, UploadID: "upload", Completed: map[int]string{}}); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
			if _, err := CheckpointPath("other-"+test.name, "guid"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("critical %s checkpoint was removed, stat err=%v", test.phase, err)
			}
		})
	}

	for _, name := range []string{"malformed", "legacy"} {
		path := filepath.Join(base, name+".json")
		contents := []byte(`{"not":"a checkpoint"}`)
		if name == "legacy" {
			contents = []byte(`{"upload_id":"old"}`)
		}
		if err := os.WriteFile(path, contents, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := CheckpointPath("cleanup-final", "guid"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"malformed", "legacy"} {
		if _, err := os.Stat(filepath.Join(base, name+".json")); err != nil {
			t.Fatalf("unreadable or legacy checkpoint was removed: %s: %v", name, err)
		}
	}
}

func TestAbortMultipartRemovesCheckpointOnlyAfterRemoteAbort(t *testing.T) {
	t.Setenv("DATA_CLIENT_CACHE_DIR", t.TempDir())
	upload := func(t *testing.T, backend *fakeBackend) (*GenericUploader, string) {
		t.Helper()
		uploader := &GenericUploader{Backend: backend}
		checkpoint, err := CheckpointPath("source", "guid")
		if err != nil {
			t.Fatal(err)
		}
		if err := uploader.saveState(checkpoint, &uploaderResumeState{
			SourcePath: "source",
			GUID:       "guid",
			UploadID:   "provider-upload",
			Phase:      uploadCheckpointUploading,
			Completed:  map[int]string{},
		}); err != nil {
			t.Fatal(err)
		}
		return uploader, checkpoint
	}

	t.Run("success", func(t *testing.T) {
		backend := &fakeBackend{}
		uploader, checkpoint := upload(t, backend)
		if err := uploader.AbortMultipart(context.Background(), "source", "guid"); err != nil {
			t.Fatalf("AbortMultipart failed: %v", err)
		}
		if backend.abortedUploadID != "provider-upload" {
			t.Fatalf("aborted upload ID = %q, want provider-upload", backend.abortedUploadID)
		}
		if _, err := os.Stat(checkpoint); !os.IsNotExist(err) {
			t.Fatalf("checkpoint remains after remote abort: %v", err)
		}
	})

	t.Run("remote failure", func(t *testing.T) {
		backend := &fakeBackend{abortErr: errors.New("remote abort failed")}
		uploader, checkpoint := upload(t, backend)
		if err := uploader.AbortMultipart(context.Background(), "source", "guid"); err == nil {
			t.Fatal("AbortMultipart succeeded after remote failure")
		}
		if _, err := os.Stat(checkpoint); err != nil {
			t.Fatalf("checkpoint removed after remote failure: %v", err)
		}
	})
}

func TestMultipartCompletionIntentPrecedesProviderCall(t *testing.T) {
	t.Setenv("DATA_CLIENT_CACHE_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &countedMultipartBackend{fakeBackend: &fakeBackend{multipartInitID: "upload"}}
	uploader := &GenericUploader{Backend: backend}
	originalWriter := uploadCheckpointWriter
	uploadCheckpointWriter = func(path string, state *uploaderResumeState) error {
		if state.Phase == uploadCheckpointCompleting {
			return errors.New("intent write failed")
		}
		return originalWriter(path, state)
	}
	t.Cleanup(func() { uploadCheckpointWriter = originalWriter })

	err := uploader.Upload(context.Background(), testMultipartRequest(path, common.FileMetadata{}))
	if err == nil || !strings.Contains(err.Error(), "persist multipart completion intent") {
		t.Fatalf("intent write error = %v", err)
	}
	if got := backend.completeCalls.Load(); got != 0 {
		t.Fatalf("provider completion called after intent write failure: %d", got)
	}
}

func TestMultipartUploadRejectsChangedAndLegacyRoutingCheckpoint(t *testing.T) {
	for _, test := range []struct {
		name     string
		metadata common.FileMetadata
	}{
		{name: "changed scope", metadata: common.FileMetadata{Authorizations: map[string][]string{"org-new": {"project-new"}}}},
		{name: "legacy state", metadata: common.FileMetadata{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DATA_CLIENT_CACHE_DIR", t.TempDir())
			path := filepath.Join(t.TempDir(), "source")
			if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			req := testMultipartRequest(path, common.FileMetadata{Authorizations: map[string][]string{"org-old": {"project-old"}}})
			state := checkpointStateForRequest(t, req, uploadCheckpointUploading, info)
			if test.name == "legacy state" {
				state.RoutingFingerprint = ""
			} else {
				state.RoutingFingerprint = uploadRoutingFingerprint(test.metadata)
			}
			backend := &countedMultipartBackend{fakeBackend: &fakeBackend{multipartInitID: "new-session"}}
			uploader := &GenericUploader{Backend: backend}
			checkpoint := writeCheckpointForRequest(t, uploader, req, state)
			t.Cleanup(func() { _ = os.Remove(checkpoint) })
			if err := uploader.Upload(context.Background(), req); err != nil {
				t.Fatal(err)
			}
			if got := backend.fakeBackend.multipartInitCalls; got != 1 {
				t.Fatalf("stale checkpoint was resumed, init calls = %d", got)
			}
			if backend.fakeBackend.multipartInitID != "new-session" {
				t.Fatalf("unexpected fresh upload ID %q", backend.fakeBackend.multipartInitID)
			}
		})
	}
}

func TestMultipartCompletionCallbackRetryDoesNotReplayProvider(t *testing.T) {
	t.Setenv("DATA_CLIENT_CACHE_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &countedMultipartBackend{fakeBackend: &fakeBackend{multipartInitID: "upload"}}
	uploader := &GenericUploader{Backend: backend}
	var fail atomic.Bool
	fail.Store(true)
	ctx := common.WithProgress(context.Background(), func(event common.ProgressEvent) error {
		if fail.Load() && event.BytesSoFar == int64(len("payload")) {
			return errors.New("callback failed")
		}
		return nil
	})
	if err := uploader.Upload(ctx, testMultipartRequest(path, common.FileMetadata{})); err == nil {
		t.Fatal("callback failure was swallowed")
	}
	if got := backend.completeCalls.Load(); got != 1 {
		t.Fatalf("completion calls after first upload = %d, want 1", got)
	}
	checkpoint, err := CheckpointPath(path, "guid")
	if err != nil {
		t.Fatal(err)
	}
	checkpointState, loaded := uploader.loadState(checkpoint)
	if !loaded || checkpointState.Phase != uploadCheckpointCompleted || len(checkpointState.CompletionParts) != 1 {
		t.Fatalf("callback failure did not leave terminal checkpoint: loaded=%v state=%+v", loaded, checkpointState)
	}
	fail.Store(false)
	if err := uploader.Upload(ctx, testMultipartRequest(path, common.FileMetadata{})); err != nil {
		t.Fatalf("retry after callback failure: %v", err)
	}
	if got := backend.completeCalls.Load(); got != 1 {
		t.Fatalf("completion replayed after durable terminal state: %d", got)
	}
}

func TestMultipartCompletionCleanupRetryDoesNotReplayProvider(t *testing.T) {
	t.Setenv("DATA_CLIENT_CACHE_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &countedMultipartBackend{fakeBackend: &fakeBackend{multipartInitID: "upload"}}
	uploader := &GenericUploader{Backend: backend}
	originalRemover := removeMultipartCheckpoint
	removeMultipartCheckpoint = func(string) error { return errors.New("cleanup failed") }
	t.Cleanup(func() { removeMultipartCheckpoint = originalRemover })
	if err := uploader.Upload(context.Background(), testMultipartRequest(path, common.FileMetadata{})); err == nil || !strings.Contains(err.Error(), "remove multipart checkpoint") {
		t.Fatalf("cleanup failure = %v", err)
	}
	if got := backend.completeCalls.Load(); got != 1 {
		t.Fatalf("completion calls after cleanup failure = %d, want 1", got)
	}
	removeMultipartCheckpoint = originalRemover
	if err := uploader.Upload(context.Background(), testMultipartRequest(path, common.FileMetadata{})); err != nil {
		t.Fatalf("retry after cleanup failure: %v", err)
	}
	if got := backend.completeCalls.Load(); got != 1 {
		t.Fatalf("completion replayed after cleanup retry: %d", got)
	}
}

func TestMultipartCompletionMarkerFailureIsIndeterminate(t *testing.T) {
	t.Setenv("DATA_CLIENT_CACHE_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &countedMultipartBackend{fakeBackend: &fakeBackend{multipartInitID: "upload"}}
	uploader := &GenericUploader{Backend: backend}
	originalWriter := uploadCheckpointWriter
	uploadCheckpointWriter = func(path string, state *uploaderResumeState) error {
		if state.Phase == uploadCheckpointCompleted {
			return errors.New("terminal write failed")
		}
		return originalWriter(path, state)
	}
	t.Cleanup(func() { uploadCheckpointWriter = originalWriter })
	if err := uploader.Upload(context.Background(), testMultipartRequest(path, common.FileMetadata{})); !errors.Is(err, errMultipartCompletionUncertain) {
		t.Fatalf("marker failure = %v, want indeterminate error", err)
	}
	uploadCheckpointWriter = originalWriter
	if got := backend.completeCalls.Load(); got != 1 {
		t.Fatalf("completion calls after marker failure = %d, want 1", got)
	}
	if err := uploader.Upload(context.Background(), testMultipartRequest(path, common.FileMetadata{})); !errors.Is(err, errMultipartCompletionRecoveryRequired) {
		t.Fatalf("opaque recovery = %v, want recovery-required error", err)
	}
	if got := backend.completeCalls.Load(); got != 1 {
		t.Fatalf("opaque recovery replayed completion: %d", got)
	}
}

func TestMultipartCompletingCheckpointReplaysOnlyWithCapability(t *testing.T) {
	t.Setenv("DATA_CLIENT_CACHE_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	req := testMultipartRequest(path, common.FileMetadata{Authorizations: map[string][]string{"org": {"project"}}})
	parts := []transfer.MultipartPart{{PartNumber: 1, ETag: "etag-1"}}
	state := checkpointStateForRequest(t, req, uploadCheckpointCompleting, info)
	state.UploadID = "upload"
	state.CompletionParts = parts
	state.CompletionFingerprint = uploadPartsFingerprint(parts)

	for _, test := range []struct {
		name       string
		replaySafe bool
	}{
		{name: "opaque"},
		{name: "replay-safe", replaySafe: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &countedMultipartBackend{fakeBackend: &fakeBackend{multipartInitID: "new"}, replaySafe: test.replaySafe}
			uploader := &GenericUploader{Backend: backend}
			checkpoint := writeCheckpointForRequest(t, uploader, req, state)
			t.Cleanup(func() { _ = os.Remove(checkpoint) })
			err := uploader.Upload(context.Background(), req)
			if test.replaySafe {
				if err != nil {
					t.Fatalf("replay-safe recovery: %v", err)
				}
				if got := backend.completeCalls.Load(); got != 1 {
					t.Fatalf("replay-safe completion calls = %d, want 1", got)
				}
			} else {
				if !errors.Is(err, errMultipartCompletionRecoveryRequired) {
					t.Fatalf("opaque recovery = %v, want recovery-required error", err)
				}
				if got := backend.completeCalls.Load(); got != 0 {
					t.Fatalf("opaque completion calls = %d, want 0", got)
				}
			}
		})
	}
}

func TestMultipartCompletingCheckpointIdentityMismatchRetainsRecoveryEvidence(t *testing.T) {
	t.Setenv("DATA_CLIENT_CACHE_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	original := testMultipartRequest(path, common.FileMetadata{Authorizations: map[string][]string{"org": {"project"}}})
	changed := testMultipartRequest(path, common.FileMetadata{Authorizations: map[string][]string{"org-new": {"project-new"}}})
	parts := []transfer.MultipartPart{{PartNumber: 1, ETag: "etag-1"}}
	state := checkpointStateForRequest(t, original, uploadCheckpointCompleting, info)
	state.CompletionParts = parts
	state.CompletionFingerprint = uploadPartsFingerprint(parts)
	backend := &countedMultipartBackend{fakeBackend: &fakeBackend{multipartInitID: "new"}}
	uploader := &GenericUploader{Backend: backend}
	checkpoint := writeCheckpointForRequest(t, uploader, original, state)
	t.Cleanup(func() { _ = os.Remove(checkpoint) })
	if err := uploader.Upload(context.Background(), changed); !errors.Is(err, errMultipartCompletionRecoveryRequired) {
		t.Fatalf("identity-mismatched completing checkpoint = %v, want recovery-required", err)
	}
	if got := backend.completeCalls.Load(); got != 0 {
		t.Fatalf("identity-mismatched checkpoint invoked completion: %d", got)
	}
	if got := backend.fakeBackend.multipartInitCalls; got != 0 {
		t.Fatalf("identity-mismatched checkpoint was overwritten by new init: %d", got)
	}
}
