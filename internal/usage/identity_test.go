package usage

import (
	"testing"
	"time"
)

func TestGrantIDUsesCanonicalFields(t *testing.T) {
	event := Event{
		ObjectID:     "object",
		SHA256:       "sha",
		Organization: "org",
		Project:      "project",
		AccessID:     "access",
		Provider:     "s3",
		Bucket:       "bucket",
		StorageURL:   "s3://bucket/key",
	}

	want := GrantID(event)
	event.ActorEmail = "someone@example.org"
	event.RequestID = "request"
	if got := GrantID(event); got != want {
		t.Fatalf("non-grant fields changed ID: got %q, want %q", got, want)
	}
	event.StorageURL = "s3://bucket/other"
	if got := GrantID(event); got == want {
		t.Fatal("storage URL did not change grant ID")
	}
}

func TestEventIDPreservesExplicitID(t *testing.T) {
	if got := EventID(Event{EventID: " existing "}); got != " existing " {
		t.Fatalf("got %q, want the explicit event ID", got)
	}
}

func TestEventIDUsesTimeOnlyWithoutRequestOrSession(t *testing.T) {
	base := Event{ObjectID: "object", EventTime: time.Unix(1, 0)}
	withDifferentTime := base
	withDifferentTime.EventTime = time.Unix(2, 0)
	if EventID(base) == EventID(withDifferentTime) {
		t.Fatal("event time must distinguish otherwise anonymous events")
	}

	base.RequestID = "request"
	withDifferentTime.RequestID = "request"
	if EventID(base) != EventID(withDifferentTime) {
		t.Fatal("event time must not affect events with a request ID")
	}
}

func TestEventIDDistinguishesNilAndZeroRanges(t *testing.T) {
	zero := int64(0)
	withoutRange := Event{RequestID: "request", ObjectID: "object"}
	withZeroRange := withoutRange
	withZeroRange.RangeStart = &zero
	if EventID(withoutRange) == EventID(withZeroRange) {
		t.Fatal("nil and zero ranges must produce different IDs")
	}
}
