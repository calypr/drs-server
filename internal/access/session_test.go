package access

import (
	"context"
	"reflect"
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

func TestSessionClaimsCloneSupportedNestedShapes(t *testing.T) {
	opaque := &struct{ Value string }{Value: "opaque"}
	claims := map[string]interface{}{
		"json": map[string]interface{}{
			"items": []interface{}{map[string]interface{}{"name": "before"}},
		},
		"resources": []string{"/organization/org"},
		"privileges": map[string]map[string]bool{
			"/organization/org": {"read": true},
		},
		"labels": map[string]string{"kind": "source"},
		"scopes": map[string][]string{"org": {"project"}},
		"opaque": opaque,
	}

	session := NewSession("gen3")
	session.SetClaims(claims)
	setClaims := session.Claims
	setClaims["json"].(map[string]interface{})["items"].([]interface{})[0].(map[string]interface{})["name"] = "set"
	setClaims["resources"].([]string)[0] = "/organization/changed"
	setClaims["privileges"].(map[string]map[string]bool)["/organization/org"]["read"] = false
	setClaims["labels"].(map[string]string)["kind"] = "changed"
	setClaims["scopes"].(map[string][]string)["org"][0] = "changed"
	if got := claims["json"].(map[string]interface{})["items"].([]interface{})[0].(map[string]interface{})["name"]; got != "before" {
		t.Fatalf("SetClaims aliased nested JSON value: %v", got)
	}
	if got := claims["resources"].([]string)[0]; got != "/organization/org" {
		t.Fatalf("SetClaims aliased string slice: %q", got)
	}
	if !claims["privileges"].(map[string]map[string]bool)["/organization/org"]["read"] {
		t.Fatal("SetClaims aliased local privilege map")
	}
	if got := claims["labels"].(map[string]string)["kind"]; got != "source" {
		t.Fatalf("SetClaims aliased string map: %q", got)
	}
	if got := claims["scopes"].(map[string][]string)["org"][0]; got != "project" {
		t.Fatalf("SetClaims aliased string-slice map: %q", got)
	}
	if session.Claims["opaque"] != opaque {
		t.Fatal("unsupported opaque claim type was unexpectedly coerced")
	}

	clone := session.Clone()
	clone.Claims["json"].(map[string]interface{})["items"].([]interface{})[0].(map[string]interface{})["name"] = "clone"
	if got := session.Claims["json"].(map[string]interface{})["items"].([]interface{})[0].(map[string]interface{})["name"]; got != "set" {
		t.Fatalf("Clone aliased nested JSON value: %v", got)
	}

	fromContext := FromContext(WithSession(context.Background(), session))
	fromContext.Claims["privileges"].(map[string]map[string]bool)["/organization/org"]["read"] = true
	if got := session.Claims["privileges"].(map[string]map[string]bool)["/organization/org"]["read"]; got != false {
		t.Fatal("FromContext aliased local privilege map")
	}
	if !reflect.DeepEqual(fromContext.Claims["resources"], []string{"/organization/changed"}) {
		t.Fatalf("unexpected cloned resource claim: %#v", fromContext.Claims["resources"])
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
