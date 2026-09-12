package metricscmd

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/metricsapi"
	"github.com/spf13/cobra"
)

func TestTransferOptionsNormalizeCLIInputs(t *testing.T) {
	previousOrganization := metricsOrganization
	previousProject := metricsProject
	previousDirection := metricsDirection
	previousReconcile := metricsReconcile
	previousFrom := metricsFrom
	previousTo := metricsTo
	previousProvider := metricsProvider
	previousBucket := metricsBucket
	previousSHA256 := metricsSHA256
	previousUser := metricsUser
	previousGroupBy := metricsGroupBy
	previousAllowStale := metricsAllowStale
	t.Cleanup(func() {
		metricsOrganization = previousOrganization
		metricsProject = previousProject
		metricsDirection = previousDirection
		metricsReconcile = previousReconcile
		metricsFrom = previousFrom
		metricsTo = previousTo
		metricsProvider = previousProvider
		metricsBucket = previousBucket
		metricsSHA256 = previousSHA256
		metricsUser = previousUser
		metricsGroupBy = previousGroupBy
		metricsAllowStale = previousAllowStale
	})

	metricsOrganization = " org "
	metricsProject = " project "
	metricsDirection = " download "
	metricsReconcile = " matched "
	metricsFrom = " start "
	metricsTo = " end "
	metricsProvider = " s3 "
	metricsBucket = " bucket "
	metricsSHA256 = " digest "
	metricsUser = " alice "
	metricsGroupBy = " user "
	metricsAllowStale = true

	got := transferOptions()
	if got.Organization != "org" || got.ProjectID != "project" || got.Direction != "download" || got.ReconciliationStatus != "matched" || got.From != "start" || got.To != "end" || got.Provider != "s3" || got.Bucket != "bucket" || got.SHA256 != "digest" || got.User != "alice" || got.GroupBy != "user" || !got.AllowStale {
		t.Fatalf("transfer options were not normalized: %+v", got)
	}
}

func TestBreakdownOptionValidation(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
		err  bool
	}{
		{raw: "", want: "user"},
		{raw: " USER ", want: "user"},
		{raw: "scope", want: "scope"},
		{raw: "provider", want: "provider"},
		{raw: "object", want: "object"},
		{raw: "bucket", err: true},
	} {
		got, err := normalizedBreakdownGroupBy(tc.raw)
		if (err != nil) != tc.err || got != tc.want {
			t.Fatalf("normalizedBreakdownGroupBy(%q) = %q, %v", tc.raw, got, err)
		}
	}

	for _, tc := range []struct {
		sortBy, order, groupBy, direction string
		wantSort, wantOrder               string
		err                               bool
	}{
		{groupBy: "user", wantSort: "downloaded", wantOrder: "desc"},
		{groupBy: "user", direction: " upload ", wantSort: "uploaded", wantOrder: "desc"},
		{groupBy: "scope", wantSort: "last-transfer", wantOrder: "desc"},
		{sortBy: "bytes_downloaded", order: "asc", wantSort: "downloaded", wantOrder: "asc"},
		{sortBy: "bytes_uploaded", wantSort: "uploaded", wantOrder: "desc"},
		{sortBy: "bytes_requested", wantSort: "requested", wantOrder: "desc"},
		{sortBy: "event_count", wantSort: "events", wantOrder: "desc"},
		{sortBy: "last_transfer", wantSort: "last-transfer", wantOrder: "desc"},
		{sortBy: "user", wantSort: "key", wantOrder: "desc"},
		{sortBy: "unknown", err: true},
		{sortBy: "events", order: "sideways", err: true},
	} {
		gotSort, gotOrder, err := normalizedBreakdownSort(tc.sortBy, tc.order, tc.groupBy, tc.direction)
		if (err != nil) != tc.err || gotSort != tc.wantSort || gotOrder != tc.wantOrder {
			t.Fatalf("normalizedBreakdownSort(%q, %q, %q, %q) = %q, %q, %v", tc.sortBy, tc.order, tc.groupBy, tc.direction, gotSort, gotOrder, err)
		}
	}
}

func TestTransferBreakdownSortingAndProjection(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	rows := []metricsapi.TransferAttributionBreakdown{
		{Key: ptr("beta"), ActorEmail: ptr("beta@example.org"), BytesDownloaded: intPtr(10), BytesUploaded: intPtr(30), BytesRequested: intPtr(40), EventCount: intPtr(2), LastTransferTime: &t1},
		{Key: ptr("alpha"), ActorSubject: ptr("subject-alpha"), BytesDownloaded: intPtr(20), BytesUploaded: intPtr(5), BytesRequested: intPtr(25), EventCount: intPtr(3), LastTransferTime: &t2},
		{ActorEmail: ptr("fallback@example.org")},
	}

	resp := metricsapi.TransferBreakdownResponse{Data: &rows}
	if got := transferBreakdownRows(resp); !reflect.DeepEqual(got, rows) {
		t.Fatalf("projected rows = %+v, want %+v", got, rows)
	}
	if got := transferBreakdownRows(metricsapi.TransferBreakdownResponse{}); got != nil {
		t.Fatalf("nil response data projected as %+v", got)
	}

	sortTransferBreakdowns(rows, "downloaded", "desc")
	if got := []string{stringValue(rows[0].Key), stringValue(rows[1].Key), transferUserLabel(rows[2])}; !reflect.DeepEqual(got, []string{"alpha", "beta", "fallback@example.org"}) {
		t.Fatalf("download ordering = %v", got)
	}
	sortTransferBreakdowns(rows, "uploaded", "asc")
	if stringValue(rows[2].Key) != "beta" {
		t.Fatalf("upload ordering = %+v", rows)
	}
	sortTransferBreakdowns(rows, "events", "desc")
	if stringValue(rows[0].Key) != "alpha" {
		t.Fatalf("event ordering = %+v", rows)
	}
	sortTransferBreakdowns(rows, "requested", "desc")
	if stringValue(rows[0].Key) != "beta" {
		t.Fatalf("requested ordering = %+v", rows)
	}
	sortTransferBreakdowns(rows, "key", "asc")
	if stringValue(rows[0].Key) != "" || stringValue(rows[1].Key) != "alpha" {
		t.Fatalf("key ordering = %+v", rows)
	}
	sortTransferBreakdowns(rows, "last-transfer", "desc")
	if stringValue(rows[0].Key) != "alpha" {
		t.Fatalf("time ordering = %+v", rows)
	}

	if got := limitedTransferBreakdowns(rows, 2); len(got) != 2 {
		t.Fatalf("limited rows length = %d", len(got))
	}
	if got := limitedTransferBreakdowns(rows, 0); len(got) != 3 {
		t.Fatalf("unlimited rows length = %d", len(got))
	}
	if got := transferUserLabel(metricsapi.TransferAttributionBreakdown{ActorSubject: ptr("subject")}); got != "subject" {
		t.Fatalf("subject label = %q", got)
	}
	if got := transferUserLabel(metricsapi.TransferAttributionBreakdown{}); got != "(unattributed)" {
		t.Fatalf("empty label = %q", got)
	}
}

func TestMetricValueAndJSONBoundaries(t *testing.T) {
	if compareInt64(2, 1) != 1 || compareInt64(1, 2) != -1 || compareInt64(1, 1) != 0 {
		t.Fatal("integer comparison returned an unexpected ordering")
	}
	now := time.Now()
	later := now.Add(time.Second)
	if compareTimePtr(nil, nil) != 0 || compareTimePtr(nil, &now) != -1 || compareTimePtr(&now, nil) != 1 || compareTimePtr(&later, &now) != 1 || compareTimePtr(&now, &later) != -1 || compareTimePtr(&now, &now) != 0 {
		t.Fatal("time comparison returned an unexpected ordering")
	}
	if stringValue(nil) != "" || stringValue(ptr("value")) != "value" || int64Value(nil) != 0 || int64Value(intPtr(7)) != 7 {
		t.Fatal("optional metric values were not projected correctly")
	}

	cmd := &cobra.Command{}
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := writeJSON(cmd, map[string]int{"count": 2}); err != nil {
		t.Fatalf("writeJSON returned error: %v", err)
	}
	if got, want := output.String(), "{\n  \"count\": 2\n}\n"; got != want {
		t.Fatalf("JSON output = %q, want %q", got, want)
	}
}

func ptr(value string) *string { return &value }

func intPtr(value int64) *int64 { return &value }
