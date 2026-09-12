package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/transfer"
	"golang.org/x/sync/errgroup"
)

type DownloadOptions struct {
	MultipartThreshold   int64
	ChunkSize            int64
	Concurrency          int
	RetryStrategy        transfer.RetryStrategy
	EphemeralDestination bool
}

type downloader struct {
	Source        transfer.ReadBackend
	RetryStrategy transfer.RetryStrategy
}

type downloadResumeState struct {
	Identity string `json:"identity"`
	Size     int64  `json:"size"`
	Complete bool   `json:"complete"`
}

func Download(ctx context.Context, source transfer.ReadBackend, guid, dstPath string, opts DownloadOptions) error {
	if opts.EphemeralDestination {
		defer os.Remove(downloadResumeStatePath(dstPath))
	}
	d := &downloader{Source: source, RetryStrategy: opts.RetryStrategy}
	return d.download(ctx, guid, dstPath, opts.Concurrency, opts.ChunkSize, opts.MultipartThreshold)
}

func (d *downloader) download(ctx context.Context, guid string, dstPath string, concurrency int, chunkSize, multipartThreshold int64) error {
	meta, err := d.Source.Stat(ctx, guid)
	if err != nil {
		return fmt.Errorf("stat failed: %w", err)
	}

	totalSize := meta.Size
	complete, err := prepareDownloadDestination(dstPath, meta.Identity, totalSize)
	if err != nil {
		return err
	}
	if complete {
		return nil
	}
	finish := func(err error) error {
		if err != nil {
			return err
		}
		if totalSize > 0 {
			info, statErr := os.Stat(dstPath)
			if statErr != nil {
				return fmt.Errorf("stat completed download: %w", statErr)
			}
			if info.Size() != totalSize {
				return fmt.Errorf("download size mismatch: got %d, expected %d", info.Size(), totalSize)
			}
			if strings.TrimSpace(meta.Identity) != "" {
				matches, checksumErr := downloadMatchesIdentity(dstPath, meta.Identity)
				if checksumErr != nil {
					return checksumErr
				}
				if !matches {
					_ = os.Remove(dstPath)
					return fmt.Errorf("download checksum does not match %s", meta.Identity)
				}
				if stateErr := saveDownloadResumeState(dstPath, downloadResumeState{Identity: meta.Identity, Size: totalSize, Complete: true}); stateErr != nil {
					return stateErr
				}
			}
		}
		return nil
	}
	if totalSize <= 0 {
		return finish(d.downloadSingle(ctx, guid, dstPath, totalSize))
	}

	if multipartThreshold > 0 && totalSize < multipartThreshold {
		return finish(d.downloadSingle(ctx, guid, dstPath, totalSize))
	}

	if totalSize < common.MB || !meta.AcceptRanges {
		return finish(d.downloadSingle(ctx, guid, dstPath, totalSize))
	}

	err = d.downloadParallel(ctx, guid, dstPath, totalSize, concurrency, chunkSize)
	if !errors.Is(err, transfer.ErrRangeIgnored) {
		return finish(err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(dstPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("discard ranged download before restarting: %w", err)
	}
	return finish(d.downloadSingle(ctx, guid, dstPath, totalSize))
}

func prepareDownloadDestination(dstPath, identity string, expectedSize int64) (bool, error) {
	statePath := downloadResumeStatePath(dstPath)
	if expectedSize <= 0 || strings.TrimSpace(identity) == "" {
		_ = os.Remove(statePath)
		if err := os.Remove(dstPath); err != nil && !os.IsNotExist(err) {
			return false, fmt.Errorf("discard unverified destination: %w", err)
		}
		return false, nil
	}

	state, valid := loadDownloadResumeState(statePath)
	matching := valid && state.Identity == identity && state.Size == expectedSize
	if matching {
		if info, err := os.Stat(dstPath); err == nil {
			if state.Complete && info.Size() == expectedSize {
				matchesIdentity, identityErr := downloadMatchesIdentity(dstPath, identity)
				if identityErr != nil {
					return false, identityErr
				}
				if matchesIdentity {
					return true, nil
				}
			}
			if !state.Complete && info.Size() > 0 && info.Size() < expectedSize {
				return false, nil
			}
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}

	if err := os.Remove(dstPath); err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("discard unverified destination: %w", err)
	}
	if err := saveDownloadResumeState(dstPath, downloadResumeState{Identity: identity, Size: expectedSize}); err != nil {
		return false, err
	}
	return false, nil
}

func downloadMatchesIdentity(path, identity string) (bool, error) {
	normalized := strings.ToLower(strings.TrimSpace(identity))
	expected, isSHA256 := strings.CutPrefix(normalized, "sha256:")
	if !isSHA256 {
		return true, nil
	}
	if len(expected) != sha256.Size*2 {
		return false, fmt.Errorf("invalid SHA-256 download identity %q", identity)
	}
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open download for checksum verification: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return false, fmt.Errorf("checksum downloaded file: %w", err)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)) == expected, nil
}

func downloadResumeStatePath(dstPath string) string { return dstPath + ".syfon-download.json" }

func loadDownloadResumeState(path string) (downloadResumeState, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return downloadResumeState{}, false
	}
	var state downloadResumeState
	if json.Unmarshal(data, &state) != nil || strings.TrimSpace(state.Identity) == "" || state.Size <= 0 {
		return downloadResumeState{}, false
	}
	return state, true
}

func saveDownloadResumeState(dstPath string, state downloadResumeState) error {
	dir := filepath.Dir(dstPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".syfon-download-*.tmp")
	if err != nil {
		return fmt.Errorf("create download checkpoint: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := json.NewEncoder(temporary).Encode(state); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write download checkpoint: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close download checkpoint: %w", err)
	}
	if err := os.Rename(temporaryPath, downloadResumeStatePath(dstPath)); err != nil {
		return fmt.Errorf("replace download checkpoint: %w", err)
	}
	return nil
}

func (d *downloader) downloadSingle(ctx context.Context, guid string, dstPath string, expectedSize int64) error {
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
		if errors.Is(err, transfer.ErrRangeIgnored) {
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

func (d *downloader) downloadParallel(ctx context.Context, guid string, dstPath string, totalSize int64, concurrency int, chunkSize int64) error {
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
		New: func() any {
			buf := make([]byte, 256*1024)
			return &buf
		},
	}

	progress := common.GetProgress(ctx)
	oid := common.GetOid(ctx)
	var progressMu sync.Mutex
	serializedProgress := progress
	if progress != nil {
		serializedProgress = func(event common.ProgressEvent) error {
			progressMu.Lock()
			defer progressMu.Unlock()
			event.BytesSoFar = soFar.Load()
			return progress(event)
		}
	}

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
			if err := gctx.Err(); err != nil {
				return err
			}
			strategy := d.RetryStrategy
			if strategy == nil {
				strategy = transfer.DefaultBackoff()
			}
			return transfer.RetryAction(gctx, d.Source.Logger(), strategy, common.MaxRetryCount, func() error {
				partBody, err := d.Source.GetRangeReader(gctx, guid, partStart, partLength)
				if errors.Is(err, transfer.ErrRangeIgnored) {
					return transfer.NonRetryable(err)
				}
				if err != nil {
					return fmt.Errorf("range download [%d,%d]: %w", partStart, partEnd, err)
				}
				defer partBody.Close()

				w := io.NewOffsetWriter(file, partStart)
				bufPtr := bufPool.Get().(*[]byte)
				buf := *bufPtr
				progressReader := newDownloadProgressReader(partBody, serializedProgress, oid, soFar.Load(), &soFar)
				written, err := io.CopyBuffer(w, progressReader, buf)
				bufPool.Put(bufPtr)
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
