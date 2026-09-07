package logs

import (
	"context"
	"fmt"
	"maps"
	"os"
	"runtime"
	"sync"
	"time"

	"log/slog"

	"github.com/calypr/syfon/client/common"
)

type Gen3Logger struct {
	*slog.Logger

	failedMu  sync.Mutex
	FailedMap map[string]common.RetryObject // Maps filePath to FileMetadata

	succeededMu  sync.Mutex
	succeededMap map[string]string // Maps filePath to GUID
}

// NewGen3Logger creates a new Gen3Logger wrapping the provided slog.Logger.
// logDir and profile are retained for compatibility and do not enable file output.
func NewGen3Logger(logger *slog.Logger, logDir, profile string) *Gen3Logger {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
	}
	return &Gen3Logger{
		Logger:       logger,
		FailedMap:    make(map[string]common.RetryObject),
		succeededMap: make(map[string]string),
	}
}

// logWithSkip logs a message at the given level, skipping `skip` stack frames for source attribution.
func (t *Gen3Logger) logWithSkip(ctx context.Context, level slog.Level, skip int, msg string, args ...any) {
	if !t.Enabled(ctx, level) {
		return
	}
	var pcs [1]uintptr
	runtime.Callers(skip, pcs[:])
	r := slog.NewRecord(time.Now(), level, msg, pcs[0])
	r.Add(args...)
	if err := t.Handler().Handle(ctx, r); err != nil {
		fmt.Fprintf(os.Stderr, "handle log record: %v\n", err)
	}
}

func (t *Gen3Logger) Info(msg string, args ...any) {
	t.logWithSkip(context.Background(), slog.LevelInfo, 3, msg, args...)
}

func (t *Gen3Logger) InfoContext(ctx context.Context, msg string, args ...any) {
	t.logWithSkip(ctx, slog.LevelInfo, 3, msg, args...)
}

func (t *Gen3Logger) Error(msg string, args ...any) {
	t.logWithSkip(context.Background(), slog.LevelError, 3, msg, args...)
}

func (t *Gen3Logger) ErrorContext(ctx context.Context, msg string, args ...any) {
	t.logWithSkip(ctx, slog.LevelError, 3, msg, args...)
}

func (t *Gen3Logger) Warn(msg string, args ...any) {
	t.logWithSkip(context.Background(), slog.LevelWarn, 3, msg, args...)
}

func (t *Gen3Logger) WarnContext(ctx context.Context, msg string, args ...any) {
	t.logWithSkip(ctx, slog.LevelWarn, 3, msg, args...)
}

func (t *Gen3Logger) Debug(msg string, args ...any) {
	t.logWithSkip(context.Background(), slog.LevelDebug, 3, msg, args...)
}

func (t *Gen3Logger) DebugContext(ctx context.Context, msg string, args ...any) {
	t.logWithSkip(ctx, slog.LevelDebug, 3, msg, args...)
}

func (t *Gen3Logger) Printf(format string, v ...any) {
	t.logWithSkip(context.Background(), slog.LevelInfo, 3, fmt.Sprintf(format, v...))
}

func (t *Gen3Logger) Println(v ...any) {
	t.logWithSkip(context.Background(), slog.LevelInfo, 3, fmt.Sprint(v...))
}

func (t *Gen3Logger) Fatalf(format string, v ...any) {
	t.logWithSkip(context.Background(), slog.LevelError, 3, fmt.Sprintf(format, v...))
}

func (t *Gen3Logger) Fatal(v ...any) {
	t.logWithSkip(context.Background(), slog.LevelError, 3, fmt.Sprint(v...))
}

// Slog exposes the underlying slog.Logger for code that needs direct slog access.
func (t *Gen3Logger) Slog() *slog.Logger {
	return t.Logger
}

func (t *Gen3Logger) GetSucceededLogMap() map[string]string {
	t.succeededMu.Lock()
	defer t.succeededMu.Unlock()
	copiedMap := make(map[string]string, len(t.succeededMap))
	maps.Copy(copiedMap, t.succeededMap)
	return copiedMap
}

func (t *Gen3Logger) GetFailedLogMap() map[string]common.RetryObject {
	t.failedMu.Lock()
	defer t.failedMu.Unlock()
	copiedMap := make(map[string]common.RetryObject, len(t.FailedMap))
	maps.Copy(copiedMap, t.FailedMap)
	return copiedMap
}

func (t *Gen3Logger) DeleteFromFailedLog(path string) {
	t.failedMu.Lock()
	defer t.failedMu.Unlock()
	delete(t.FailedMap, path)
}

func (t *Gen3Logger) GetSucceededCount() int {
	return len(t.succeededMap)
}

func (t *Gen3Logger) Failed(filePath, filename string, metadata common.FileMetadata, guid string, retryCount int, multipart bool) {
	t.failedHelper(context.Background(), filePath, guid, retryCount, 4)
}

func (t *Gen3Logger) FailedContext(ctx context.Context, filePath, filename string, metadata common.FileMetadata, guid string, retryCount int, multipart bool) {
	t.failedHelper(ctx, filePath, guid, retryCount, 4)
}

func (t *Gen3Logger) failedHelper(ctx context.Context, filePath, guid string, retryCount, skip int) {
	msg := fmt.Sprintf("Failed: %s (GUID: %s, Retry: %d)", filePath, guid, retryCount)
	t.logWithSkip(ctx, slog.LevelError, skip, msg)
}

func (t *Gen3Logger) Succeeded(filePath, guid string) {
	t.succeededHelper(context.Background(), filePath, guid, 4)
}

func (t *Gen3Logger) SucceededContext(ctx context.Context, filePath, guid string) {
	t.succeededHelper(ctx, filePath, guid, 4)
}

func (t *Gen3Logger) succeededHelper(ctx context.Context, filePath, guid string, skip int) {
	msg := fmt.Sprintf("Succeeded: %s (GUID: %s)", filePath, guid)
	t.logWithSkip(ctx, slog.LevelDebug, skip, msg)
}
