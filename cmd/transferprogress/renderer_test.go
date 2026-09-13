package transferprogress

import (
	"bytes"
	"context"
	"strings"
	"testing"

	clientcommon "github.com/calypr/syfon/client/common"
)

func TestRendererReportsObservableProgressLifecycle(t *testing.T) {
	var output bytes.Buffer
	renderer := New(&output, "a-very-long-object-name-that-needs-trimming", 100)

	renderer.Start()
	if got := output.String(); !strings.Contains(got, "...e-that-needs-trimming") || !strings.Contains(got, "0 B / 100 B") {
		t.Fatalf("start output = %q", got)
	}

	before := output.String()
	if err := renderer.ProgressCallback()(clientcommon.ProgressEvent{Event: "log", BytesSoFar: 40}); err != nil {
		t.Fatalf("non-progress callback returned error: %v", err)
	}
	if output.String() != before {
		t.Fatalf("non-progress event changed output from %q to %q", before, output.String())
	}

	if err := renderer.ProgressCallback()(clientcommon.ProgressEvent{Event: "progress", BytesSoFar: 40}); err != nil {
		t.Fatalf("progress callback returned error: %v", err)
	}
	renderer.Abort()
	if got := output.String(); !strings.Contains(got, "40 B / 100 B") || !strings.HasSuffix(got, "\n") {
		t.Fatalf("abort output = %q", got)
	}

	renderer.Finish()
	if got := output.String(); !strings.Contains(got, "100 B / 100 B") || !strings.Contains(got, "100 %") {
		t.Fatalf("finish output = %q", got)
	}
}

func TestRendererFormattingBoundaries(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "short label", got: trimLabel("object", 10), want: "object"},
		{name: "trimmed label", got: trimLabel("abcdefghij", 7), want: "...ghij"},
		{name: "tiny maximum", got: trimLabel("abcdef", 3), want: "abcdef"},
		{name: "empty bar", got: renderProgressBar(5, 10, 0), want: "[]"},
		{name: "unknown total", got: renderProgressBar(0, 0, 4), want: "[    ]"},
		{name: "negative progress", got: renderProgressBar(-2, 10, 4), want: "[    ]"},
		{name: "half bar", got: renderProgressBar(5, 10, 4), want: "[==  ]"},
		{name: "clamped bar", got: renderProgressBar(20, 10, 4), want: "[====]"},
		{name: "unknown percent", got: renderPercent(1, 0, false), want: "0 %"},
		{name: "negative percent", got: renderPercent(-1, 10, false), want: "0 %"},
		{name: "in-flight percent", got: renderPercent(9999, 10000, false), want: "100 %"},
		{name: "clamped percent", got: renderPercent(20, 10, true), want: "100 %"},
		{name: "zero bytes", got: formatBinaryBytes(0), want: "0 B"},
		{name: "bytes", got: formatBinaryBytes(17), want: "17 B"},
		{name: "kibibytes", got: formatBinaryBytes(1536), want: "1.50 KiB"},
		{name: "mebibytes", got: formatBinaryBytes(2 * 1024 * 1024), want: "2.00 MiB"},
		{name: "no speed", got: renderSpeed(0), want: ""},
		{name: "speed", got: renderSpeed(2048), want: "2.00 KiB/s"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %q, want %q", tc.got, tc.want)
			}
		})
	}

	visibleTests := []struct {
		current   int64
		total     int64
		completed bool
		want      int64
	}{
		{current: -1, total: 10, want: 0},
		{current: 11, total: 10, want: 9},
		{current: 10, total: 10, completed: true, want: 10},
		{current: 7, total: 0, want: 7},
	}
	for _, tc := range visibleTests {
		if got := visibleProgressBytes(tc.current, tc.total, tc.completed); got != tc.want {
			t.Fatalf("visibleProgressBytes(%d, %d, %t) = %d, want %d", tc.current, tc.total, tc.completed, got, tc.want)
		}
	}
}

func TestWithProgressAddsObjectAndCallbackToContext(t *testing.T) {
	var output bytes.Buffer
	renderer := New(&output, "object", 10)
	ctx := WithProgress(context.Background(), "did:test", renderer)

	if got := clientcommon.GetOid(ctx); got != "did:test" {
		t.Fatalf("object ID = %q, want did:test", got)
	}
	callback := clientcommon.GetProgress(ctx)
	if callback == nil {
		t.Fatal("progress callback is missing")
	}
	if err := callback(clientcommon.ProgressEvent{Event: "progress", BytesSoFar: 5}); err != nil {
		t.Fatalf("callback returned error: %v", err)
	}
	renderer.Abort()
	if !strings.Contains(output.String(), "5 B / 10 B") {
		t.Fatalf("progress output = %q", output.String())
	}
}

func TestRendererWithUnknownTotalProducesNoOutput(t *testing.T) {
	var output bytes.Buffer
	renderer := New(&output, "object", 0)
	renderer.Start()
	renderer.SetCurrent(10)
	renderer.Finish()
	if got := output.String(); got != "\n" {
		t.Fatalf("unknown-total output = %q, want newline", got)
	}
}
