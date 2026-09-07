package postgres

import (
	"github.com/calypr/syfon/internal/models"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
)

func TestDefaultProvider(t *testing.T) {
	if got := defaultProvider(""); got != "s3" {
		t.Fatalf("expected s3 default, got %q", got)
	}
	if got := defaultProvider("gcs"); got != "gcs" {
		t.Fatalf("unexpected provider passthrough: %q", got)
	}
}

func TestUniqueObjectsByID(t *testing.T) {
	in := []models.InternalObject{
		{DrsObject: drs.DrsObject{Id: "1"}},
		{DrsObject: drs.DrsObject{Id: "1"}},
		{DrsObject: drs.DrsObject{Id: "2"}},
	}
	out := uniqueObjectsByID(in)
	if len(out) != 2 || out[0].Id != "1" || out[1].Id != "2" {
		t.Fatalf("unexpected unique list: %#v", out)
	}
}

func TestLatestUsageTime(t *testing.T) {
	t1 := time.Now().Add(-2 * time.Hour)
	t2 := time.Now().Add(-1 * time.Hour)
	t3 := time.Now()

	if got := latestUsageTime(nil, nil); got != nil {
		t.Fatalf("expected nil latest time, got %v", got)
	}
	got := latestUsageTime(&t1, &t3, &t2)
	if got == nil || !got.Equal(t3) {
		t.Fatalf("expected latest=%v got %v", t3, got)
	}
}
