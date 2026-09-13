package file

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/storage"
	"github.com/google/uuid"
	"gocloud.dev/blob"
)

func TestAccessReturnsRawSlashNormalizedPathAndIgnoresCloudOptions(t *testing.T) {
	b, err := newBackend(t.TempDir())
	if err != nil {
		t.Fatalf("newBackend failed: %v", err)
	}

	options := storage.SignRequest{ExpiresIn: 37, Method: "POST", DownloadFilename: "ignored.txt"}
	target := storage.Target{PhysicalBucket: "ignored-bucket", Key: "nested/object.bin"}
	want := filepath.ToSlash(filepath.Join(b.rootPath, target.Key))

	got, err := b.Sign(context.Background(), storage.ProviderBinding{}, storage.SignRequest{Target: target, Method: options.Method, ExpiresIn: options.ExpiresIn, DownloadFilename: options.DownloadFilename})
	if err != nil {
		t.Fatalf("SignURL failed: %v", err)
	}
	if got.Location != want {
		t.Fatalf("raw path = %q, want %q", got.Location, want)
	}

	ranged, err := b.Sign(context.Background(), storage.ProviderBinding{}, storage.SignRequest{Target: target, Range: &storage.ByteRange{Start: 20, End: 30}, Method: options.Method, ExpiresIn: options.ExpiresIn, DownloadFilename: options.DownloadFilename})
	if err != nil {
		t.Fatalf("SignDownloadPart failed: %v", err)
	}
	if ranged.Location != want {
		t.Fatalf("ranged path = %q, want %q", ranged.Location, want)
	}
}

func TestCredentialEndpointIsEffectiveMultipartRoot(t *testing.T) {
	constructorRoot := t.TempDir()
	credentialRoot := t.TempDir()
	b, err := newBackend(constructorRoot)
	if err != nil {
		t.Fatalf("newBackend failed: %v", err)
	}
	defer b.Close()
	binding := storage.ProviderBinding{Provider: "file", LookupKey: "bucket", PhysicalBucket: "bucket", Credential: &buckets.Credential{
		Provider: "file", Bucket: "bucket", Endpoint: credentialRoot,
	}}
	access, err := b.Sign(context.Background(), binding, storage.SignRequest{Target: storage.Target{Key: "object.bin"}})
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}
	if want := filepath.ToSlash(filepath.Join(credentialRoot, "object.bin")); access.Location != want {
		t.Fatalf("access location = %q, want %q", access.Location, want)
	}

	ctx := context.Background()
	partKey := storage.MultipartPartObjectKey("object.bin", "upload", 1)
	credentialBucket, err := b.bucketForRoot(credentialRoot)
	if err != nil {
		t.Fatalf("bucketForRoot failed: %v", err)
	}
	writeBucket(t, credentialBucket, partKey, "contents")
	if err := b.CompleteMultipart(ctx, binding, storage.CompleteMultipartRequest{
		Target: storage.Target{Key: "object.bin"}, UploadID: "upload", CompletionID: "completion",
		Parts: []storage.CompletedPart{{PartNumber: 1}},
	}); err != nil {
		t.Fatalf("CompleteMultipart failed: %v", err)
	}
	if got := readBucket(t, credentialBucket, "object.bin"); got != "contents" {
		t.Fatalf("completed contents = %q, want contents", got)
	}
	if _, err := os.Stat(filepath.Join(constructorRoot, "object.bin")); !os.IsNotExist(err) {
		t.Fatalf("constructor root was modified, stat error = %v", err)
	}
}

func TestInitMultipartUploadReturnsUUID(t *testing.T) {
	b, err := newBackend(t.TempDir())
	if err != nil {
		t.Fatalf("newBackend failed: %v", err)
	}

	first, err := b.BeginMultipart(context.Background(), storage.ProviderBinding{}, storage.BeginMultipartRequest{Target: storage.Target{}, CompletionID: "completion-1"})
	if err != nil {
		t.Fatalf("first InitMultipartUpload failed: %v", err)
	}
	if _, err := uuid.Parse(string(first)); err != nil {
		t.Fatalf("upload ID %q is not a UUID: %v", first, err)
	}
	second, err := b.BeginMultipart(context.Background(), storage.ProviderBinding{}, storage.BeginMultipartRequest{Target: storage.Target{}, CompletionID: "completion-2"})
	if err != nil {
		t.Fatalf("second InitMultipartUpload failed: %v", err)
	}
	if first == second {
		t.Fatalf("multipart upload IDs unexpectedly repeated: %q", first)
	}
}

func TestCompleteMultipartUploadSortsPartsAndCleansUp(t *testing.T) {
	b, err := newBackend(t.TempDir())
	if err != nil {
		t.Fatalf("newBackend failed: %v", err)
	}

	ctx := context.Background()
	key := "test/object.bin"
	uploadID := storage.UploadID("upload-123")
	part1 := storage.MultipartPartObjectKey(key, uploadID, 1)
	part2 := storage.MultipartPartObjectKey(key, uploadID, 2)
	writeBlob(t, b, part1, "hello ")
	writeBlob(t, b, part2, "world")

	if err := b.CompleteMultipart(ctx, storage.ProviderBinding{}, storage.CompleteMultipartRequest{
		Target:   storage.Target{PhysicalBucket: "ignored", Key: key},
		UploadID: uploadID,
		Parts: []storage.CompletedPart{
			{PartNumber: 2, ETag: "e2"},
			{PartNumber: 1, ETag: "e1"},
		},
	}); err != nil {
		t.Fatalf("CompleteMultipartUpload failed: %v", err)
	}

	if got := readBlob(t, b, key); got != "hello world" {
		t.Fatalf("stitched object = %q, want %q", got, "hello world")
	}
	for _, partKey := range []string{part1, part2} {
		if _, err := b.rootBucket.NewReader(ctx, partKey, nil); err == nil {
			t.Fatalf("part %q was not cleaned up", partKey)
		}
	}
}

func TestCompleteMultipartUploadRetriesAfterCommittedObject(t *testing.T) {
	b, err := newBackend(t.TempDir())
	if err != nil {
		t.Fatalf("newBackend failed: %v", err)
	}

	ctx := context.Background()
	key := "test/object.bin"
	uploadID := storage.UploadID("upload-retry")
	part := storage.MultipartPartObjectKey(key, uploadID, 1)
	writeBlob(t, b, part, "hello world")
	request := storage.CompleteMultipartRequest{
		Target:       storage.Target{PhysicalBucket: "ignored", Key: key},
		UploadID:     uploadID,
		CompletionID: "completion",
		Parts:        []storage.CompletedPart{{PartNumber: 1}},
	}
	if err := b.CompleteMultipart(ctx, storage.ProviderBinding{}, request); err != nil {
		t.Fatalf("first completion failed: %v", err)
	}
	attrs, err := b.rootBucket.Attributes(ctx, key)
	if err != nil {
		t.Fatalf("read completed object attributes: %v", err)
	}
	if got := attrs.Metadata[storage.MultipartCompletionMarkerMetadataKey]; got != "completion" {
		t.Fatalf("completion marker = %q, want completion", got)
	}
	if err := b.CompleteMultipart(ctx, storage.ProviderBinding{}, request); err != nil {
		t.Fatalf("retry completion failed: %v", err)
	}
	if got := readBlob(t, b, key); got != "hello world" {
		t.Fatalf("retried object = %q, want hello world", got)
	}
}

func TestCompleteMultipartUploadLeavesPartsOnMissingPartFailure(t *testing.T) {
	b, err := newBackend(t.TempDir())
	if err != nil {
		t.Fatalf("newBackend failed: %v", err)
	}

	ctx := context.Background()
	key := "test/object.bin"
	uploadID := storage.UploadID("upload-missing")
	part1 := storage.MultipartPartObjectKey(key, uploadID, 1)
	part2 := storage.MultipartPartObjectKey(key, uploadID, 2)
	writeBlob(t, b, key, "original")
	writeBlob(t, b, part1, "hello ")

	err = b.CompleteMultipart(ctx, storage.ProviderBinding{}, storage.CompleteMultipartRequest{
		Target:   storage.Target{PhysicalBucket: "ignored", Key: key},
		UploadID: uploadID,
		Parts: []storage.CompletedPart{
			{PartNumber: 1},
			{PartNumber: 2},
		},
	})
	if err == nil {
		t.Fatal("expected missing-part completion failure")
	}
	if got := readBlob(t, b, key); got != "original" {
		t.Fatalf("destination after failed completion = %q, want original", got)
	}
	if exists, err := b.rootBucket.Exists(ctx, part1); err != nil || !exists {
		t.Fatalf("successful part exists = %v, error = %v; want true", exists, err)
	}
	if exists, err := b.rootBucket.Exists(ctx, part2); err != nil || exists {
		t.Fatalf("missing part exists = %v, error = %v; want false", exists, err)
	}

	writeBlob(t, b, part2, "world")
	if err := b.CompleteMultipart(ctx, storage.ProviderBinding{}, storage.CompleteMultipartRequest{
		Target:   storage.Target{PhysicalBucket: "ignored", Key: key},
		UploadID: uploadID,
		Parts: []storage.CompletedPart{
			{PartNumber: 1},
			{PartNumber: 2},
		},
	}); err != nil {
		t.Fatalf("retry completion failed: %v", err)
	}
	if got := readBlob(t, b, key); got != "hello world" {
		t.Fatalf("destination after retry = %q, want hello world", got)
	}
	for _, partKey := range []string{part1, part2} {
		if exists, err := b.rootBucket.Exists(ctx, partKey); err != nil || exists {
			t.Fatalf("part %q after retry exists=%v error=%v", partKey, exists, err)
		}
	}
}

func TestAbortMultipartRejectsPathTraversalWithoutDeletingFiles(t *testing.T) {
	root := t.TempDir()
	b, err := newBackend(root)
	if err != nil {
		t.Fatalf("newBackend failed: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })

	victim := filepath.Join(root, "victim", "keep.txt")
	if err := os.MkdirAll(filepath.Dir(victim), 0o755); err != nil {
		t.Fatalf("create victim directory: %v", err)
	}
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatalf("create victim file: %v", err)
	}

	err = b.AbortMultipart(context.Background(), storage.ProviderBinding{}, storage.AbortMultipartRequest{
		Target:   storage.Target{Key: "../../victim"},
		UploadID: "upload",
	})
	if err == nil {
		t.Error("AbortMultipart accepted a traversal key")
	}
	if content, readErr := os.ReadFile(victim); readErr != nil || string(content) != "keep" {
		t.Fatalf("victim file after abort = %q, error = %v", content, readErr)
	}
}

func TestAbortMultipartDeletesOnlyTheRequestedUploadParts(t *testing.T) {
	root := t.TempDir()
	b, err := newBackend(root)
	if err != nil {
		t.Fatalf("newBackend failed: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })

	requested := storage.MultipartPartObjectKey("nested/object.bin", "requested", 1)
	unrelated := storage.MultipartPartObjectKey("nested/object.bin", "unrelated", 1)
	writeBlob(t, b, requested, "remove")
	writeBlob(t, b, unrelated, "keep")
	if err := b.AbortMultipart(context.Background(), storage.ProviderBinding{}, storage.AbortMultipartRequest{
		Target:   storage.Target{Key: "nested/object.bin"},
		UploadID: "requested",
	}); err != nil {
		t.Fatalf("AbortMultipart failed: %v", err)
	}
	if exists, err := b.rootBucket.Exists(context.Background(), requested); err != nil || exists {
		t.Fatalf("requested part exists = %v, error = %v; want false, nil", exists, err)
	}
	if exists, err := b.rootBucket.Exists(context.Background(), unrelated); err != nil || !exists {
		t.Fatalf("unrelated part exists = %v, error = %v; want true, nil", exists, err)
	}
}

func TestCompleteMultipartUploadRejectsEmptyParts(t *testing.T) {
	b, err := newBackend(t.TempDir())
	if err != nil {
		t.Fatalf("newBackend failed: %v", err)
	}

	err = b.CompleteMultipart(context.Background(), storage.ProviderBinding{}, storage.CompleteMultipartRequest{Target: storage.Target{Key: "object"}})
	if err == nil || err.Error() != "multipart complete requires at least one part" {
		t.Fatalf("empty completion error = %v", err)
	}
}

func TestDeleteRemovesExactPathAndIsIdempotent(t *testing.T) {
	b, err := newBackend(t.TempDir())
	if err != nil {
		t.Fatalf("newBackend failed: %v", err)
	}

	targetPath := filepath.Join(b.rootPath, "nested", "file.txt")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	target := storage.PhysicalTarget{Provider: "file", Path: targetPath}
	if err := b.Delete(context.Background(), storage.ProviderBinding{Provider: "file"}, []storage.PhysicalTarget{target}); err != nil {
		t.Fatalf("Delete(existing) failed: %v", err)
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("expected exact path to be removed, stat err=%v", err)
	}
	if err := b.Delete(context.Background(), storage.ProviderBinding{Provider: "file"}, []storage.PhysicalTarget{target}); err != nil {
		t.Fatalf("Delete(missing) failed: %v", err)
	}
}

func TestFileRegistrationDoesNotClaimProbeInventoryOrInvalidation(t *testing.T) {
	registration, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	manager, err := storage.NewManager(fileCredentialLookup{}, registration)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	probe := manager.Probe(context.Background(), []storage.ProbeTarget{{ID: "one", Target: storage.Target{Provider: "file", PhysicalBucket: "bucket", LookupKey: "bucket", Key: "key"}}})
	var probeErr *storage.OperationError
	if len(probe) != 1 || !errors.As(probe[0].Err, &probeErr) || probeErr.Kind != storage.ErrorUnsupported {
		t.Fatalf("probe result = %#v, want unsupported capability", probe)
	}
	_, err = manager.Inventory(context.Background(), storage.InventoryRequest{Target: storage.Target{Provider: "file", PhysicalBucket: "bucket", LookupKey: "bucket"}})
	var inventoryErr *storage.OperationError
	if !errors.As(err, &inventoryErr) || inventoryErr.Kind != storage.ErrorUnsupported {
		t.Fatalf("inventory error = %v, want unsupported capability", err)
	}
	manager.InvalidateBucket("bucket")
}

type fileCredentialLookup struct{}

func (fileCredentialLookup) GetS3Credential(context.Context, string) (*buckets.Credential, error) {
	return &buckets.Credential{Provider: "file", Bucket: "bucket"}, nil
}

func writeBlob(t *testing.T, b *backend, key, contents string) {
	t.Helper()
	writeBucket(t, b.rootBucket, key, contents)
}

func writeBucket(t *testing.T, bucket *blob.Bucket, key, contents string) {
	t.Helper()
	writer, err := bucket.NewWriter(context.Background(), key, nil)
	if err != nil {
		t.Fatalf("open writer %q: %v", key, err)
	}
	if _, err := writer.Write([]byte(contents)); err != nil {
		t.Fatalf("write %q: %v", key, err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer %q: %v", key, err)
	}
}

func readBlob(t *testing.T, b *backend, key string) string {
	t.Helper()
	return readBucket(t, b.rootBucket, key)
}

func readBucket(t *testing.T, bucket *blob.Bucket, key string) string {
	t.Helper()
	reader, err := bucket.NewReader(context.Background(), key, nil)
	if err != nil {
		t.Fatalf("open reader %q: %v", key, err)
	}
	contents, err := io.ReadAll(reader)
	if closeErr := reader.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatalf("read %q: %v", key, err)
	}
	return string(contents)
}
