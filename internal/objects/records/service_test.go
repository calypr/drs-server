package records

import (
	"context"
	"strings"
	"testing"

	objectmodel "github.com/calypr/syfon/internal/objects"
)

type serviceCaptureWriter struct {
	registered []objectmodel.Record
}

func (w *serviceCaptureWriter) DeleteObject(context.Context, string) error              { return nil }
func (w *serviceCaptureWriter) CreateObject(context.Context, *objectmodel.Record) error { return nil }
func (w *serviceCaptureWriter) BulkDeleteObjects(context.Context, []string) error {
	return nil
}
func (w *serviceCaptureWriter) RegisterObjects(_ context.Context, records []objectmodel.Record) error {
	w.registered = append([]objectmodel.Record(nil), records...)
	return nil
}
func (w *serviceCaptureWriter) ReplaceObjects(context.Context, []objectmodel.Record) error {
	return nil
}

func TestServiceOwnsRegistrationPort(t *testing.T) {
	writer := &serviceCaptureWriter{}
	service := NewService(Dependencies{Writer: writer})
	input := []objectmodel.Record{{Id: "record-1"}}

	if err := service.RegisterObjects(context.Background(), input); err != nil {
		t.Fatalf("RegisterObjects() error = %v", err)
	}
	if len(writer.registered) != 1 || writer.registered[0].Id != input[0].Id {
		t.Fatalf("RegisterObjects() wrote %#v, want %#v", writer.registered, input)
	}
}

func TestCanonicalizeContentObjectsKeepsStableCanonicalIdentity(t *testing.T) {
	checksum := objectmodel.Checksum{Type: "sha256", Checksum: strings.Repeat("a", 64)}
	records := []objectmodel.Record{{Id: "older", Checksums: []objectmodel.Checksum{checksum}}, {Id: "newer", Checksums: []objectmodel.Checksum{checksum}}}

	got := canonicalizeContentObjects(records)
	if len(got) != 1 || got[0].Id != "newer" {
		t.Fatalf("CanonicalizeContentObjects() = %#v, want deterministic canonical record", got)
	}
}
