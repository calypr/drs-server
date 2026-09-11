package logs

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/calypr/syfon/client/common"
)

type ProgressHandler struct {
	next slog.Handler
}

func NewProgressHandler(next slog.Handler) *ProgressHandler {
	if next == nil {
		next = slog.Default().Handler()
	}
	return &ProgressHandler{next: next}
}

func (h *ProgressHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *ProgressHandler) Handle(ctx context.Context, record slog.Record) error {
	err := h.next.Handle(ctx, record)
	if callback := common.GetProgress(ctx); callback != nil {
		attrs := make(map[string]any)
		record.Attrs(func(attr slog.Attr) bool {
			attrs[attr.Key] = attr.Value.Any()
			return true
		})
		_ = callback(common.ProgressEvent{
			Event:   "log",
			Oid:     common.GetOid(ctx),
			Message: record.Message,
			Level:   record.Level.String(),
			Attrs:   attrs,
		})
	}
	return err
}

func (h *ProgressHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ProgressHandler{next: h.next.WithAttrs(attrs)}
}

func (h *ProgressHandler) WithGroup(name string) slog.Handler {
	return &ProgressHandler{next: h.next.WithGroup(name)}
}

type Gen3Logger struct {
	*slog.Logger
}

func NewGen3Logger(logger *slog.Logger) *Gen3Logger {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
	}
	return &Gen3Logger{Logger: logger}
}

func (t *Gen3Logger) Printf(format string, v ...any) {
	t.Info(fmt.Sprintf(format, v...))
}
