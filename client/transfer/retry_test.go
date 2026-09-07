package transfer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/calypr/syfon/client/common"
)

type fixedStrategy time.Duration

func (f fixedStrategy) WaitTime(retryCount int) time.Duration {
	return time.Duration(f)
}

type captureLogger struct {
	NoOpLogger
	printfs    []string
	errorsSeen []error
}

func (l *captureLogger) Printf(format string, v ...any) {
	l.printfs = append(l.printfs, format)
}

func (l *captureLogger) Error(msg string, args ...any) {
	for i := 1; i < len(args); i += 2 {
		if err, ok := args[i].(error); ok {
			l.errorsSeen = append(l.errorsSeen, err)
		}
	}
}

func TestExponentialBackoffAndDefaultBackoff(t *testing.T) {
	t.Parallel()

	backoff := &ExponentialBackoff{MaxWaitSeconds: 10}
	if got := backoff.WaitTime(0); got != time.Second {
		t.Fatalf("WaitTime(0) = %v, want %v", got, time.Second)
	}
	if got := backoff.WaitTime(3); got != 8*time.Second {
		t.Fatalf("WaitTime(3) = %v, want %v", got, 8*time.Second)
	}
	if got := backoff.WaitTime(8); got != 10*time.Second {
		t.Fatalf("WaitTime should cap at 10s, got %v", got)
	}

	defaultBackoff, ok := DefaultBackoff().(*ExponentialBackoff)
	if !ok {
		t.Fatalf("DefaultBackoff returned unexpected type %T", DefaultBackoff())
	}
	if defaultBackoff.MaxWaitSeconds != common.MaxWaitTime {
		t.Fatalf("unexpected max wait seconds: %d", defaultBackoff.MaxWaitSeconds)
	}
}

func TestRetryAction(t *testing.T) {
	t.Parallel()

	t.Run("eventual success", func(t *testing.T) {
		logger := &captureLogger{}
		attempts := 0
		err := RetryAction(context.Background(), logger, fixedStrategy(0), 3, func() error {
			attempts++
			if attempts < 3 {
				return errors.New("try again")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("RetryAction returned error: %v", err)
		}
		if attempts != 3 {
			t.Fatalf("expected 3 attempts, got %d", attempts)
		}
		if len(logger.printfs) != 2 || len(logger.errorsSeen) != 2 {
			t.Fatalf("expected retry logging, got printfs=%d errors=%d", len(logger.printfs), len(logger.errorsSeen))
		}
	})

	t.Run("context cancellation during wait", func(t *testing.T) {
		logger := &captureLogger{}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		attempts := 0
		err := RetryAction(ctx, logger, fixedStrategy(time.Hour), 3, func() error {
			attempts++
			return errors.New("boom")
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context canceled, got %v", err)
		}
		if attempts != 1 {
			t.Fatalf("expected one attempt before cancellation, got %d", attempts)
		}
	})

	t.Run("final failure", func(t *testing.T) {
		logger := &captureLogger{}
		wantErr := errors.New("still failing")
		err := RetryAction(context.Background(), logger, fixedStrategy(0), 1, func() error {
			return wantErr
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("expected %v, got %v", wantErr, err)
		}
	})
}

func TestNoOpLogger(t *testing.T) {
	t.Parallel()

	logger := NoOpLogger{}
	ctx := context.Background()
	metadata := common.FileMetadata{Authorizations: map[string][]string{"test": {}}}

	if logger.Slog() == nil {
		t.Fatal("expected slog logger")
	}
	logger.Info("info")
	logger.InfoContext(ctx, "info")
	logger.Error("error")
	logger.ErrorContext(ctx, "error")
	logger.Warn("warn")
	logger.WarnContext(ctx, "warn")
	logger.Debug("debug")
	logger.DebugContext(ctx, "debug")
	logger.Printf("formatted %s", "message")
	logger.Println("line")
	logger.Failed("file", "name", metadata, "guid", 1, true)
	logger.FailedContext(ctx, "file", "name", metadata, "guid", 1, false)
	logger.Succeeded("file", "guid")
	logger.SucceededContext(ctx, "file", "guid")
	logger.DeleteFromFailedLog("file")

	if got := logger.GetSucceededLogMap(); len(got) != 0 {
		t.Fatalf("expected empty succeeded log map, got %+v", got)
	}
	if got := logger.GetFailedLogMap(); len(got) != 0 {
		t.Fatalf("expected empty failed log map, got %+v", got)
	}
}
