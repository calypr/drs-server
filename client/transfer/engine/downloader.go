package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/transfer"
	"golang.org/x/sync/errgroup"
)

type GenericDownloader struct {
	Source        transfer.ReadBackend
	RetryStrategy transfer.RetryStrategy
}

func (d *GenericDownloader) Download(ctx context.Context, guid string, dstPath string, concurrency int, chunkSize, multipartThreshold int64) error {
	meta, err := d.Source.Stat(ctx, guid)
	if err != nil {
		return fmt.Errorf("stat failed: %w", err)
	}

	totalSize := meta.Size
	if totalSize <= 0 {
		return d.downloadSingle(ctx, guid, dstPath, totalSize)
	}

	if multipartThreshold > 0 && totalSize < multipartThreshold {
		return d.downloadSingle(ctx, guid, dstPath, totalSize)
	}

	if totalSize < common.MB || !meta.AcceptRanges {
		return d.downloadSingle(ctx, guid, dstPath, totalSize)
	}

	return d.downloadParallel(ctx, guid, dstPath, totalSize, concurrency, chunkSize)
}

func (d *GenericDownloader) downloadSingle(ctx context.Context, guid string, dstPath string, expectedSize int64) error {
	var startOffset int64
	if stat, err := os.Stat(dstPath); err == nil {
		if expectedSize > 0 && stat.Size() == expectedSize {
			return nil // Already complete
		}
		if stat.Size() > 0 && expectedSize > stat.Size() {
			startOffset = stat.Size()
		}
	}

	var body io.ReadCloser
	var err error
	if startOffset > 0 {
		body, err = d.Source.GetRangeReader(ctx, guid, startOffset, expectedSize-startOffset)
		if err == transfer.ErrRangeIgnored {
			// Server ignored our range request, restart from zero.
			startOffset = 0
			body, err = d.Source.GetReader(ctx, guid)
		}
	} else {
		body, err = d.Source.GetReader(ctx, guid)
	}
	if err != nil {
		return err
	}
	defer body.Close()

	progressReader := newDownloadProgressReader(body, common.GetProgress(ctx), common.GetOid(ctx), startOffset, nil)
	body = io.NopCloser(progressReader)

	if dir := filepath.Dir(dstPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	mode := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if startOffset > 0 {
		mode = os.O_WRONLY | os.O_APPEND
	}

	file, err := os.OpenFile(dstPath, mode, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	written, err := io.Copy(file, body)
	if err != nil {
		_ = progressReader.FlushPendingProgress()
		return err
	}

	if err := progressReader.Complete(); err != nil {
		return err
	}
	if expectedSize > 0 && (startOffset+written) < expectedSize {
		return fmt.Errorf("short download: got %d, expected %d", startOffset+written, expectedSize)
	}
	return nil
}

type downloadProgressReader struct {
	reader            io.Reader
	onProgress        common.ProgressCallback
	oid               string
	bytesSoFar        int64
	bytesSinceReport  int64
	lastReportedSoFar int64
	localBytes        int64
	localReported     int64
	globalBytes       *atomic.Int64
}

func newDownloadProgressReader(reader io.Reader, onProgress common.ProgressCallback, oid string, initialBytes int64, globalBytes *atomic.Int64) *downloadProgressReader {
	return &downloadProgressReader{
		reader:            reader,
		onProgress:        onProgress,
		oid:               oid,
		bytesSoFar:        initialBytes,
		lastReportedSoFar: initialBytes,
		globalBytes:       globalBytes,
	}
}

func (r *downloadProgressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 && r.onProgress != nil {
		delta := int64(n)
		r.localBytes += delta
		if r.globalBytes != nil {
			r.bytesSoFar = r.globalBytes.Add(delta)
		} else {
			r.bytesSoFar += delta
		}
		r.bytesSinceReport += delta
		if r.bytesSinceReport >= common.OnProgressThreshold {
			if progressErr := r.emit(); progressErr != nil {
				return n, progressErr
			}
		}
	}
	return n, err
}

func (r *downloadProgressReader) FlushPendingProgress() error {
	return r.emit()
}

func (r *downloadProgressReader) Complete() error {
	return r.emit()
}

func (r *downloadProgressReader) emit() error {
	if r.onProgress == nil {
		return nil
	}
	delta := r.bytesSoFar - r.lastReportedSoFar
	displaySoFar := r.bytesSoFar
	if r.globalBytes != nil {
		delta = r.localBytes - r.localReported
		displaySoFar = r.globalBytes.Load()
	}
	if delta <= 0 {
		return nil
	}
	if err := r.onProgress(common.ProgressEvent{
		Event:          "progress",
		Oid:            r.oid,
		BytesSoFar:     displaySoFar,
		BytesSinceLast: delta,
	}); err != nil {
		return err
	}
	r.lastReportedSoFar = r.bytesSoFar
	r.localReported = r.localBytes
	r.bytesSinceReport = 0
	return nil
}

func (d *GenericDownloader) downloadParallel(ctx context.Context, guid string, dstPath string, totalSize int64, concurrency int, chunkSize int64) error {
	if dir := filepath.Dir(dstPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	file, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	if err := file.Truncate(totalSize); err != nil {
		return fmt.Errorf("pre-allocate failed: %w", err)
	}

	if chunkSize <= 0 {
		chunkSize = 64 * common.MB
	}
	if concurrency <= 0 {
		concurrency = 8
	}

	totalParts := int((totalSize + chunkSize - 1) / chunkSize)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)

	var soFar atomic.Int64
	bufPool := sync.Pool{
		New: func() any { return make([]byte, 256*1024) },
	}

	progress := common.GetProgress(ctx)
	oid := common.GetOid(ctx)

	for i := 0; i < totalParts; i++ {
		ps := int64(i) * chunkSize
		pe := ps + chunkSize - 1
		if pe >= totalSize {
			pe = totalSize - 1
		}
		partSize := pe - ps + 1
		partStart := ps
		partEnd := pe
		partLength := partSize

		g.Go(func() error {
			strategy := d.RetryStrategy
			if strategy == nil {
				strategy = transfer.DefaultBackoff()
			}
			return transfer.RetryAction(gctx, d.Source.Logger(), strategy, common.MaxRetryCount, func() error {
				partBody, err := d.Source.GetRangeReader(gctx, guid, partStart, partLength)
				if err != nil {
					return fmt.Errorf("range download [%d,%d]: %w", partStart, partEnd, err)
				}
				defer partBody.Close()

				w := io.NewOffsetWriter(file, partStart)
				buf := bufPool.Get().([]byte)
				progressReader := newDownloadProgressReader(partBody, progress, oid, soFar.Load(), &soFar)
				written, err := io.CopyBuffer(w, progressReader, buf)
				bufPool.Put(buf)
				if err != nil {
					_ = progressReader.FlushPendingProgress()
					return err
				}
				if err := progressReader.Complete(); err != nil {
					return err
				}
				if written != partLength {
					return fmt.Errorf("short write: got %d, expected %d", written, partLength)
				}
				return nil
			})
		})
	}

	if err := g.Wait(); err != nil {
		// Parallel downloads pre-allocate the destination file to its final size.
		// If any part fails, remove the incomplete file so retries do not mistake it
		// for a completed cache entry.
		_ = file.Close()
		_ = os.Remove(dstPath)
		return err
	}

	return nil
}




