package access

import (
	"context"
	"testing"
)

func TestSessionCloneDeepCopy(t *testing.T) {
	s := NewSession("gen3")
	s.SetClaims(map[string]interface{}{"sub": "user-1"})
	s.SetAuthorizations([]string{"/programs/a/projects/p1"}, map[string]map[string]bool{
		"/programs/a/projects/p1": {"read": true},
	}, true)

	clone := s.Clone()
	clone.Claims["sub"] = "user-2"
	clone.Resources[0] = "/programs/a/projects/p2"
	clone.Privileges["/programs/a/projects/p1"]["read"] = false

	if got := s.Claims["sub"]; got != "user-1" {
		t.Fatalf("expected original claims unchanged, got %v", got)
	}
	if got := s.Resources[0]; got != "/programs/a/projects/p1" {
		t.Fatalf("expected original resources unchanged, got %q", got)
	}
	if !s.Privileges["/programs/a/projects/p1"]["read"] {
		t.Fatalf("expected original privileges unchanged")
	}
}

func TestSessionContextRoundTrip(t *testing.T) {
	s := NewSession("local")
	s.SetSubject("alice")
	s.SetSource(SourceLocalCSV)
	ctx := WithSession(context.Background(), s)
	out := FromContext(ctx)

	if out.Mode != "local" || out.Subject != "alice" || out.Source != SourceLocalCSV {
		t.Fatalf("unexpected roundtrip session: %+v", out)
	}
}

func TestNormalizeMethodNameWriteAlias(t *testing.T) {
	methods := normalizeMethodName("write", true)
	want := map[string]bool{"file_upload": true, "create": true, "update": true, "delete": true}
	if len(methods) != 4 {
		t.Fatalf("expected 4 normalized write aliases, got %v", methods)
	}
	for _, m := range methods {
		if !want[m] {
			t.Fatalf("unexpected method alias: %q", m)
		}
	}
}
