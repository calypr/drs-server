package engine

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/syfon/client/transfer"
)

type ignoredRangeSource struct {
	data    []byte
	streams atomic.Int32
	ranges  atomic.Int32
}

func (s *ignoredRangeSource) Logger() transfer.TransferLogger { return transfer.NoOpLogger{} }
func (s *ignoredRangeSource) Stat(context.Context, string) (*transfer.ObjectMetadata, error) {
	return &transfer.ObjectMetadata{Size: int64(len(s.data)), AcceptRanges: true}, nil
}
func (s *ignoredRangeSource) GetReader(context.Context, string) (io.ReadCloser, error) {
	s.streams.Add(1)
	return io.NopCloser(bytes.NewReader(s.data)), nil
}
func (s *ignoredRangeSource) GetRangeReader(context.Context, string, int64, int64) (io.ReadCloser, error) {
	s.ranges.Add(1)
	return nil, transfer.ErrRangeIgnored
}

type immediateDownloadRetry struct{}

func (immediateDownloadRetry) WaitTime(int) time.Duration { return 0 }

func TestParallelDownloadFallsBackWhenRangesAreIgnored(t *testing.T) {
	source := &ignoredRangeSource{data: bytes.Repeat([]byte("download payload\n"), 128*1024)}
	destination := filepath.Join(t.TempDir(), "download.bin")
	err := Download(context.Background(), source, "object", destination, DownloadOptions{
		MultipartThreshold: 1, ChunkSize: 1024 * 1024, Concurrency: 2, RetryStrategy: immediateDownloadRetry{},
	})
	if err != nil {
		t.Fatalf("download with ignored ranges: %v", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, source.data) {
		t.Fatalf("downloaded %d bytes, want exact %d-byte payload", len(got), len(source.data))
	}
	if streams := source.streams.Load(); streams != 1 {
		t.Fatalf("full streams = %d, want one fallback", streams)
	}
	if ranges := source.ranges.Load(); ranges < 1 || ranges > 2 {
		t.Fatalf("range requests = %d, want one attempt per active worker without retries", ranges)
	}
}
