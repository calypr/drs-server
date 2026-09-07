package engine

import (
	"fmt"
	"io"
	"sync/atomic"

	"github.com/calypr/syfon/client/common"
)

type progressReader struct {
	reader            io.Reader
	onProgress        common.ProgressCallback
	oid               string
	total             int64
	localBytes        int64
	bytesSinceReport  int64
	lastReportedSoFar int64
	completed         bool
	globalBytes       *int64
}

type progressCallbackError struct {
	err error
}

func (e progressCallbackError) Error() string {
	return e.err.Error()
}

func (e progressCallbackError) Unwrap() error {
	return e.err
}

func newProgressReader(reader io.Reader, onProgress common.ProgressCallback, oid string, total int64, globalBytes *int64) *progressReader {
	return &progressReader{
		reader:      reader,
		onProgress:  onProgress,
		oid:         oid,
		total:       total,
		globalBytes: globalBytes,
	}
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	if n > 0 && pr.onProgress != nil {
		delta := int64(n)
		pr.bytesSinceReport += delta
		pr.advance(delta)

		if pr.bytesSinceReport >= common.OnProgressThreshold {
			if progressErr := pr.emit(false); progressErr != nil {
				return n, progressErr
			}
		}
	}
	return n, err
}

func (pr *progressReader) FlushPendingProgress() error {
	return pr.emit(false)
}

func (pr *progressReader) Complete() error {
	return pr.emit(true)
}

func (pr *progressReader) ResetForRetry() {
	pr.localBytes = 0
	pr.bytesSinceReport = 0
}

func (pr *progressReader) Finalize() error {
	if err := pr.Complete(); err != nil {
		return err
	}
	if pr.globalBytes == nil && pr.total > 0 && pr.currentRaw() < pr.total {
		return fmt.Errorf("upload incomplete: %d/%d bytes", pr.currentRaw(), pr.total)
	}
	return nil
}

func (pr *progressReader) advance(delta int64) int64 {
	if pr.globalBytes != nil {
		return atomic.AddInt64(pr.globalBytes, delta)
	}
	pr.localBytes += delta
	return pr.localBytes
}

func (pr *progressReader) currentRaw() int64 {
	if pr.globalBytes != nil {
		return atomic.LoadInt64(pr.globalBytes)
	}
	return pr.localBytes
}

func (pr *progressReader) visibleCurrent(final bool) int64 {
	current := pr.currentRaw()
	if !final && pr.total > 0 && current >= pr.total {
		return pr.total - 1
	}
	return current
}

func (pr *progressReader) emit(final bool) error {
	if pr.onProgress == nil {
		return nil
	}
	visible := pr.visibleCurrent(final)
	if visible < pr.lastReportedSoFar {
		visible = pr.lastReportedSoFar
	}
	delta := visible - pr.lastReportedSoFar
	emitZeroFinal := final && pr.total == 0 && !pr.completed
	if delta <= 0 && !emitZeroFinal {
		return nil
	}
	if err := pr.onProgress(common.ProgressEvent{
		Event:          "progress",
		Oid:            pr.oid,
		BytesSoFar:     visible,
		BytesSinceLast: delta,
	}); err != nil {
		return progressCallbackError{err: err}
	}
	pr.lastReportedSoFar = visible
	pr.bytesSinceReport = 0
	if final {
		pr.completed = true
	}
	return nil
}
