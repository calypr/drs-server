package file

import (
	"context"
	"io"
	"testing"

	"github.com/calypr/syfon/internal/signer"
)

func TestFileSigner_CompleteMultipartUpload_StitchesParts(t *testing.T) {
	s, err := NewFileSigner(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileSigner failed: %v", err)
	}

	ctx := context.Background()
	key := "test/object.bin"
	uploadID := "upload-123"

	part1 := signer.MultipartPartObjectKey(key, uploadID, 1)
	part2 := signer.MultipartPartObjectKey(key, uploadID, 2)

	w1, err := s.rootBucket.NewWriter(ctx, part1, nil)
	if err != nil {
		t.Fatalf("open part1 writer: %v", err)
	}
	if _, err := w1.Write([]byte("hello ")); err != nil {
		t.Fatalf("write part1: %v", err)
	}
	if err := w1.Close(); err != nil {
		t.Fatalf("close part1: %v", err)
	}

	w2, err := s.rootBucket.NewWriter(ctx, part2, nil)
	if err != nil {
		t.Fatalf("open part2 writer: %v", err)
	}
	if _, err := w2.Write([]byte("world")); err != nil {
		t.Fatalf("write part2: %v", err)
	}
	if err := w2.Close(); err != nil {
		t.Fatalf("close part2: %v", err)
	}

	if err := s.CompleteMultipartUpload(ctx, "", key, uploadID, []signer.MultipartPart{
		{PartNumber: 2, ETag: "e2"},
		{PartNumber: 1, ETag: "e1"},
	}); err != nil {
		t.Fatalf("CompleteMultipartUpload failed: %v", err)
	}

	r, err := s.rootBucket.NewReader(ctx, key, nil)
	if err != nil {
		t.Fatalf("open stitched object: %v", err)
	}
	defer func() {
		if closeErr := r.Close(); closeErr != nil {
			t.Logf("warning: failed to close stitched object reader: %v", closeErr)
		}
	}()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stitched object: %v", err)
	}
	if got := string(b); got != "hello world" {
		t.Fatalf("unexpected stitched object content: %q", got)
	}

	if _, err := s.rootBucket.NewReader(ctx, part1, nil); err == nil {
		t.Fatalf("expected part1 to be cleaned up")
	}
	if _, err := s.rootBucket.NewReader(ctx, part2, nil); err == nil {
		t.Fatalf("expected part2 to be cleaned up")
	}
}
