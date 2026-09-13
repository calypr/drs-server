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
	if dir := filepath.Dir(dstPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	var initialOffset int64
	if stat, err := os.Stat(dstPath); err == nil {
		if expectedSize > 0 && stat.Size() == expectedSize {
			return nil
		}
		if expectedSize > stat.Size() && stat.Size() > 0 {
			initialOffset = stat.Size()
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	progress := newDownloadProgressLedger(common.GetProgress(ctx), common.GetOid(ctx), expectedSize, initialOffset)
	fullRestart := initialOffset == 0
	strategy := d.RetryStrategy
	if strategy == nil {
		strategy = transfer.DefaultBackoff()
	}

	err := transfer.RetryAction(ctx, d.Source.Logger(), strategy, common.MaxRetryCount, func() error {
		if err := ctx.Err(); err != nil {
			return transfer.NonRetryable(err)
		}

		for {
			startOffset := int64(0)
			if !fullRestart {
				if stat, statErr := os.Stat(dstPath); statErr == nil {
					if expectedSize > 0 && stat.Size() >= expectedSize {
						return nil
					}
					if stat.Size() > 0 {
						startOffset = stat.Size()
					}
				} else if !os.IsNotExist(statErr) {
					return transfer.NonRetryable(statErr)
				}
			}

			var body io.ReadCloser
			var err error
			if !fullRestart && startOffset > 0 {
				body, err = d.Source.GetRangeReader(ctx, guid, startOffset, expectedSize-startOffset)
				if errors.Is(err, transfer.ErrRangeIgnored) {
					if body != nil {
						_ = body.Close()
					}
					fullRestart = true
					progress.Reset()
					if truncateErr := truncateDownloadDestination(dstPath); truncateErr != nil {
						return transfer.NonRetryable(truncateErr)
					}
					continue
				}
			} else {
				if fullRestart {
					if err := truncateDownloadDestination(dstPath); err != nil {
						return transfer.NonRetryable(err)
					}
				}
				body, err = d.Source.GetReader(ctx, guid)
			}
			if err != nil {
				if body != nil {
					_ = body.Close()
				}
				return err
			}

			mode := os.O_CREATE | os.O_WRONLY | os.O_APPEND
			if fullRestart || startOffset == 0 {
				mode = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
			}
			file, err := os.OpenFile(dstPath, mode, 0o644)
			if err != nil {
				_ = body.Close()
				return transfer.NonRetryable(err)
			}

			maxBytes := int64(-1)
			if expectedSize > 0 {
				maxBytes = expectedSize - startOffset
			}
			attempt := progress.NewAttempt(startOffset)
			written, copyErr := copyDownloadAttempt(ctx, file, body, maxBytes, attempt)
			closeErr := body.Close()
			fileErr := file.Close()
			if copyErr == nil && closeErr != nil {
				copyErr = closeErr
			}
			if copyErr == nil && fileErr != nil {
				copyErr = downloadDestinationError{err: fileErr}
			}

			if errors.Is(copyErr, transfer.ErrRangeIgnored) {
				if fullRestart {
					return transfer.NonRetryable(fmt.Errorf("full download exceeded expected size"))
				}
				fullRestart = true
				progress.Reset()
				if err := truncateDownloadDestination(dstPath); err != nil {
					return transfer.NonRetryable(err)
				}
				continue
			}

			if copyErr != nil {
				if errors.Is(copyErr, context.Canceled) || errors.Is(copyErr, context.DeadlineExceeded) {
					return transfer.NonRetryable(copyErr)
				}
				var destinationErr downloadDestinationError
				if errors.As(copyErr, &destinationErr) {
					return transfer.NonRetryable(copyErr)
				}
				if !fullRestart {
					attempt.Publish()
				}
				return copyErr
			}
			if expectedSize > 0 && written != maxBytes {
				if !fullRestart {
					attempt.Publish()
				}
				return transfer.NonRetryable(fmt.Errorf("short download: got %d, expected %d", startOffset+written, expectedSize))
			}

			attempt.Publish()
			return nil
		}
	})
	if err != nil {
		return err
	}

	return progress.Flush()
}

type downloadProgressLedger struct {
	onProgress common.ProgressCallback
	oid        string
	total      int64
	base       int64

	mu     sync.Mutex
	deltas []int64
}

type downloadAttemptProgress struct {
	ledger   *downloadProgressLedger
	floor    int64
	physical int64
	pending  int64
	deltas   []int64
}

type downloadDestinationError struct{ err error }

func (e downloadDestinationError) Error() string { return e.err.Error() }

func (e downloadDestinationError) Unwrap() error { return e.err }

func newDownloadProgressLedger(onProgress common.ProgressCallback, oid string, total, base int64) *downloadProgressLedger {
	if total > 0 && base > total {
		base = total
	}
	return &downloadProgressLedger{onProgress: onProgress, oid: oid, total: total, base: base}
}

func (p *downloadProgressLedger) NewAttempt(physicalStart int64) *downloadAttemptProgress {
	return &downloadAttemptProgress{ledger: p, floor: p.base, physical: physicalStart}
}

func (p *downloadProgressLedger) Reset() {
	p.mu.Lock()
	p.deltas = nil
	p.mu.Unlock()
}

func (p *downloadProgressLedger) append(deltas []int64) {
	if len(deltas) == 0 {
		return
	}
	p.mu.Lock()
	p.deltas = append(p.deltas, deltas...)
	p.mu.Unlock()
}

func (p *downloadProgressLedger) Flush() error {
	if p.onProgress == nil {
		return nil
	}
	p.mu.Lock()
	deltas := append([]int64(nil), p.deltas...)
	p.mu.Unlock()

	soFar := p.base
	for _, delta := range deltas {
		if delta <= 0 {
			continue
		}
		if p.total > 0 && soFar+delta > p.total {
			delta = p.total - soFar
		}
		if delta <= 0 {
			continue
		}
		soFar += delta
		if err := p.onProgress(common.ProgressEvent{Event: "progress", Oid: p.oid, BytesSoFar: soFar, BytesSinceLast: delta}); err != nil {
			return progressCallbackError{err: err}
		}
	}
	return nil
}

func (a *downloadAttemptProgress) Commit(bytes int64) {
	if bytes <= 0 {
		return
	}
	oldLogical := a.floor
	if a.physical > oldLogical {
		oldLogical = a.physical
	}
	a.physical += bytes
	newLogical := a.floor
	if a.physical > newLogical {
		newLogical = a.physical
	}
	a.pending += newLogical - oldLogical
	for a.pending >= common.OnProgressThreshold {
		a.deltas = append(a.deltas, common.OnProgressThreshold)
		a.pending -= common.OnProgressThreshold
	}
}

func (a *downloadAttemptProgress) Publish() {
	if a == nil || a.ledger == nil {
		return
	}
	if a.pending > 0 {
		a.deltas = append(a.deltas, a.pending)
		a.pending = 0
	}
	a.ledger.append(a.deltas)
	a.deltas = nil
}

func copyDownloadAttempt(ctx context.Context, dst io.Writer, src io.Reader, maxBytes int64, progress *downloadAttemptProgress) (int64, error) {
	buf := make([]byte, 256*1024)
	var written int64
	remaining := maxBytes
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		if remaining == 0 {
			var probe [1]byte
			n, err := src.Read(probe[:])
			if n > 0 {
				return written, transfer.ErrRangeIgnored
			}
			if err == io.EOF {
				return written, nil
			}
			if err != nil {
				return written, err
			}
			continue
		}

		n, readErr := src.Read(buf)
		if n > 0 {
			if remaining >= 0 && int64(n) > remaining {
				n = int(remaining)
				if n > 0 {
					nWritten, writeErr := dst.Write(buf[:n])
					if nWritten > 0 {
						written += int64(nWritten)
						progress.Commit(int64(nWritten))
					}
					if writeErr != nil {
						return written, downloadDestinationError{err: writeErr}
					}
					if nWritten != n {
						return written, downloadDestinationError{err: io.ErrShortWrite}
					}
				}
				return written, transfer.ErrRangeIgnored
			}

			nWritten, writeErr := dst.Write(buf[:n])
			if nWritten > 0 {
				written += int64(nWritten)
				progress.Commit(int64(nWritten))
			}
			if writeErr != nil {
				return written, downloadDestinationError{err: writeErr}
			}
			if nWritten != n {
				return written, downloadDestinationError{err: io.ErrShortWrite}
			}
			if remaining > 0 {
				remaining -= int64(n)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return written, nil
			}
			return written, readErr
		}
	}
}

func truncateDownloadDestination(dstPath string) error {
	file, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	return file.Close()
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
	fileClosed := false
	defer func() {
		if !fileClosed {
			_ = file.Close()
		}
	}()

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

	progress := newDownloadProgressLedger(common.GetProgress(ctx), common.GetOid(ctx), totalSize, 0)

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
			var committed int64
			strategy := d.RetryStrategy
			if strategy == nil {
				strategy = transfer.DefaultBackoff()
			}
			return transfer.RetryAction(gctx, d.Source.Logger(), strategy, common.MaxRetryCount, func() error {
				if err := gctx.Err(); err != nil {
					return transfer.NonRetryable(err)
				}
				remaining := partLength - committed
				if remaining <= 0 {
					return nil
				}
				partBody, err := d.Source.GetRangeReader(gctx, guid, partStart+committed, remaining)
				if errors.Is(err, transfer.ErrRangeIgnored) {
					if partBody != nil {
						_ = partBody.Close()
					}
					return transfer.NonRetryable(err)
				}
				if err != nil {
					if partBody != nil {
						_ = partBody.Close()
					}
					return fmt.Errorf("range download [%d,%d]: %w", partStart, partEnd, err)
				}

				w := io.NewOffsetWriter(file, partStart+committed)
				attempt := progress.NewAttempt(partStart + committed)
				written, copyErr := copyDownloadAttempt(gctx, w, partBody, remaining, attempt)
				closeErr := partBody.Close()
				if copyErr == nil && closeErr != nil {
					copyErr = closeErr
				}
				if written > 0 {
					committed += written
				}
				if errors.Is(copyErr, transfer.ErrRangeIgnored) {
					return transfer.NonRetryable(copyErr)
				}
				if copyErr != nil {
					if errors.Is(copyErr, context.Canceled) || errors.Is(copyErr, context.DeadlineExceeded) {
						return transfer.NonRetryable(copyErr)
					}
					var destinationErr downloadDestinationError
					if errors.As(copyErr, &destinationErr) {
						return transfer.NonRetryable(copyErr)
					}
					attempt.Publish()
					return copyErr
				}
				attempt.Publish()
				if written != remaining {
					return fmt.Errorf("short write: got %d, expected %d", committed, partLength)
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
		fileClosed = true
		_ = os.Remove(dstPath)
		return err
	}

	if err := file.Close(); err != nil {
		fileClosed = true
		return err
	}
	fileClosed = true
	return progress.Flush()
}
