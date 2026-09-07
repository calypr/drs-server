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
)

func TestAccessReturnsRawSlashNormalizedPathAndIgnoresCloudOptions(t *testing.T) {
	b, err := newBackend(t.TempDir())
	if err != nil {
		t.Fatalf("newBackend failed: %v", err)
	}

	options := storage.AccessOptions{
		ExpiresIn:        37,
		Method:           "POST",
		DownloadFilename: "ignored.txt",
	}
	target := storage.ObjectTarget{Bucket: "ignored-bucket", Key: "nested/object.bin"}
	want := filepath.ToSlash(filepath.Join(b.rootPath, target.Key))

	got, err := b.SignURL(context.Background(), target, options)
	if err != nil {
		t.Fatalf("SignURL failed: %v", err)
	}
	if got.Location != want {
		t.Fatalf("raw path = %q, want %q", got.Location, want)
	}

	ranged, err := b.SignDownloadPart(context.Background(), target, storage.ByteRange{Start: 20, End: 30}, options)
	if err != nil {
		t.Fatalf("SignDownloadPart failed: %v", err)
	}
	if ranged.Location != want {
		t.Fatalf("ranged path = %q, want %q", ranged.Location, want)
	}
}

func TestInitMultipartUploadReturnsUUID(t *testing.T) {
	b, err := newBackend(t.TempDir())
	if err != nil {
		t.Fatalf("newBackend failed: %v", err)
	}

	first, err := b.InitMultipartUpload(context.Background(), storage.ObjectTarget{})
	if err != nil {
		t.Fatalf("first InitMultipartUpload failed: %v", err)
	}
	if _, err := uuid.Parse(string(first)); err != nil {
		t.Fatalf("upload ID %q is not a UUID: %v", first, err)
	}
	second, err := b.InitMultipartUpload(context.Background(), storage.ObjectTarget{})
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

	if err := b.CompleteMultipartUpload(ctx, storage.CompleteMultipartRequest{
		Target:   storage.ObjectTarget{Bucket: "ignored", Key: key},
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
	writeBlob(t, b, part1, "hello ")

	err = b.CompleteMultipartUpload(ctx, storage.CompleteMultipartRequest{
		Target:   storage.ObjectTarget{Bucket: "ignored", Key: key},
		UploadID: uploadID,
		Parts: []storage.CompletedPart{
			{PartNumber: 1},
			{PartNumber: 2},
		},
	})
	if err == nil {
		t.Fatal("expected missing-part completion failure")
	}
	if _, err := b.rootBucket.NewReader(ctx, part1, nil); err != nil {
		t.Fatalf("successful part was cleaned up after failed completion: %v", err)
	}
	if _, err := b.rootBucket.NewReader(ctx, part2, nil); err == nil {
		t.Fatal("missing part unexpectedly exists")
	}
}

func TestCompleteMultipartUploadRejectsEmptyParts(t *testing.T) {
	b, err := newBackend(t.TempDir())
	if err != nil {
		t.Fatalf("newBackend failed: %v", err)
	}

	err = b.CompleteMultipartUpload(context.Background(), storage.CompleteMultipartRequest{Target: storage.ObjectTarget{Key: "object"}})
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
	if err := b.Delete(context.Background(), []storage.PhysicalTarget{target}); err != nil {
		t.Fatalf("Delete(existing) failed: %v", err)
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("expected exact path to be removed, stat err=%v", err)
	}
	if err := b.Delete(context.Background(), []storage.PhysicalTarget{target}); err != nil {
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

	probe := manager.Probe(context.Background(), []storage.ProbeTarget{{ID: "one", Target: storage.ObjectTarget{Bucket: "bucket", Key: "key"}}})
	var probeErr *storage.OperationError
	if len(probe) != 1 || !errors.As(probe[0].Err, &probeErr) || probeErr.Kind != storage.ErrorUnsupported {
		t.Fatalf("probe result = %#v, want unsupported capability", probe)
	}
	_, err = manager.Inventory(context.Background(), storage.InventoryRequest{Target: storage.PrefixTarget{Bucket: "bucket"}})
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
	writer, err := b.rootBucket.NewWriter(context.Background(), key, nil)
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
	reader, err := b.rootBucket.NewReader(context.Background(), key, nil)
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
