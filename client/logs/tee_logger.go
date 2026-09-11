package logs

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"log/slog"
)

type Gen3Logger struct {
	*slog.Logger
}

// NewGen3Logger creates a new Gen3Logger wrapping the provided slog.Logger.
// logDir and profile are retained for compatibility and do not enable file output.
func NewGen3Logger(logger *slog.Logger, logDir, profile string) *Gen3Logger {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
	}
	return &Gen3Logger{Logger: logger}
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
