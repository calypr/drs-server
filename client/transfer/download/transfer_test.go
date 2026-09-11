package download

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/transfer"
)

type memoryBackend struct {
	data       []byte
	mu         sync.Mutex
	rangeCalls int
}

func (b *memoryBackend) Logger() transfer.TransferLogger { return transfer.NoOpLogger{} }

func (b *memoryBackend) Stat(context.Context, string) (*transfer.ObjectMetadata, error) {
	return &transfer.ObjectMetadata{Size: int64(len(b.data)), AcceptRanges: true}, nil
}

func (b *memoryBackend) GetReader(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(b.data)), nil
}

func (b *memoryBackend) GetRangeReader(_ context.Context, _ string, offset, length int64) (io.ReadCloser, error) {
	end := offset + length
	if offset < 0 || end > int64(len(b.data)) || end < offset {
		return nil, fmt.Errorf("invalid range %d:%d", offset, end)
	}
	b.mu.Lock()
	b.rangeCalls++
	b.mu.Unlock()
	return io.NopCloser(bytes.NewReader(b.data[offset:end])), nil
}

func TestDownloadEntryPoints(t *testing.T) {
	payload := bytes.Repeat([]byte("0123456789abcdef"), int((2*common.MB)/16))

	t.Run("options reach parallel engine", func(t *testing.T) {
		backend := &memoryBackend{data: payload}
		destination := filepath.Join(t.TempDir(), "parallel.bin")
		err := DownloadToPathWithOptions(context.Background(), backend, "object", destination, DownloadOptions{
			MultipartThreshold: common.MB,
			ChunkSize:          common.MB,
			Concurrency:        2,
		})
		if err != nil {
			t.Fatalf("download: %v", err)
		}
		got, err := os.ReadFile(destination)
		if err != nil {
			t.Fatalf("read destination: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatal("downloaded content differs from source")
		}
		backend.mu.Lock()
		defer backend.mu.Unlock()
		if backend.rangeCalls != 2 {
			t.Fatalf("expected two ranged reads, got %d", backend.rangeCalls)
		}
	})

	t.Run("default entry point downloads object", func(t *testing.T) {
		backend := &memoryBackend{data: []byte("payload")}
		destination := filepath.Join(t.TempDir(), "single.bin")
		if err := DownloadFile(context.Background(), backend, "object", destination); err != nil {
			t.Fatalf("download: %v", err)
		}
		got, err := os.ReadFile(destination)
		if err != nil {
			t.Fatalf("read destination: %v", err)
		}
		if string(got) != "payload" {
			t.Fatalf("unexpected content %q", got)
		}
	})
}
