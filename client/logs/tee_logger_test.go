package logs

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/calypr/syfon/client/common"
)

func TestProgressHandlerForwardsLogsAndProgressEvents(t *testing.T) {
	var output bytes.Buffer
	handler := NewProgressHandler(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger := slog.New(handler.WithGroup("request").WithAttrs([]slog.Attr{slog.String("component", "client")}))

	var event common.ProgressEvent
	ctx := common.WithOid(context.Background(), "did:test")
	ctx = common.WithProgress(ctx, func(got common.ProgressEvent) error {
		event = got
		return nil
	})
	logger.InfoContext(ctx, "download ready", "status", 200)

	if got := output.String(); !strings.Contains(got, "msg=\"download ready\"") || !strings.Contains(got, "request.component=client") || !strings.Contains(got, "request.status=200") {
		t.Fatalf("forwarded log output = %q", got)
	}
	if event.Event != "log" || event.Oid != "did:test" || event.Message != "download ready" || event.Level != "INFO" || event.Attrs["status"] != int64(200) {
		t.Fatalf("progress event = %+v", event)
	}
	if !handler.Enabled(ctx, slog.LevelInfo) || handler.Enabled(ctx, slog.LevelDebug) {
		t.Fatal("handler did not preserve the wrapped level policy")
	}
}

func TestProgressHandlerWithoutCallbackStillLogs(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewProgressHandler(slog.NewTextHandler(&output, nil)))
	logger.WarnContext(context.Background(), "plain warning")
	if !strings.Contains(output.String(), "msg=\"plain warning\"") {
		t.Fatalf("log output = %q", output.String())
	}
}

func TestGen3LoggerFormatsPrintfMessages(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	gen3 := NewGen3Logger(logger)
	gen3.Printf("copied %d objects", 3)
	if !strings.Contains(output.String(), "msg=\"copied 3 objects\"") {
		t.Fatalf("Gen3 output = %q", output.String())
	}
	if NewProgressHandler(nil) == nil || NewGen3Logger(nil) == nil {
		t.Fatal("nil logger fallbacks returned nil")
	}
}
