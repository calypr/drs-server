package transfer

import (
	"context"
	"io"
	"log/slog"

	"github.com/calypr/syfon/client/common"
)

// NoOpLogger satisfies TransferLogger without emitting output.
type NoOpLogger struct{}

func (NoOpLogger) Slog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func (NoOpLogger) Info(string, ...any)                                           {}
func (NoOpLogger) InfoContext(context.Context, string, ...any)                   {}
func (NoOpLogger) Error(string, ...any)                                          {}
func (NoOpLogger) ErrorContext(context.Context, string, ...any)                  {}
func (NoOpLogger) Warn(string, ...any)                                           {}
func (NoOpLogger) WarnContext(context.Context, string, ...any)                   {}
func (NoOpLogger) Debug(string, ...any)                                          {}
func (NoOpLogger) DebugContext(context.Context, string, ...any)                  {}
func (NoOpLogger) Printf(string, ...any)                                         {}
func (NoOpLogger) Println(...any)                                                {}
func (NoOpLogger) Failed(string, string, common.FileMetadata, string, int, bool) {}
func (NoOpLogger) FailedContext(context.Context, string, string, common.FileMetadata, string, int, bool) {
}
func (NoOpLogger) Succeeded(string, string)                         {}
func (NoOpLogger) SucceededContext(context.Context, string, string) {}
func (NoOpLogger) GetSucceededLogMap() map[string]string            { return map[string]string{} }
func (NoOpLogger) GetFailedLogMap() map[string]common.RetryObject {
	return map[string]common.RetryObject{}
}
func (NoOpLogger) DeleteFromFailedLog(string)   {}
