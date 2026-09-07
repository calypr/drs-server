package access

import (
	"context"
	"testing"
)

func testSessionContext(mode string, header bool, enforced bool, resources []string, privileges map[string]map[string]bool) context.Context {
	session := NewSession(mode)
	session.AuthHeaderPresent = header
	session.AuthzEnforced = enforced
	session.SetAuthorizations(resources, privileges, enforced)
	return WithSession(context.Background(), session)
}

func TestGetUserAuthz(t *testing.T) {
	t.Run("missing key returns empty", func(t *testing.T) {
		got := GetUserAuthz(context.Background())
		if len(got) != 0 {
			t.Fatalf("expected empty authz, got %v", got)
		}
	})

	t.Run("list returned as-is", func(t *testing.T) {
		expected := []string{"/programs/a", "/programs/a/projects/b"}
		ctx := testSessionContext("", false, false, expected, nil)
		got := GetUserAuthz(ctx)
		if len(got) != len(expected) {
			t.Fatalf("expected %d resources, got %d", len(expected), len(got))
		}
		for i := range expected {
			if got[i] != expected[i] {
				t.Fatalf("expected %q at index %d, got %q", expected[i], i, got[i])
			}
		}
	})
}

func TestCheckAccess(t *testing.T) {
	tests := []struct {
		name            string
		recordResources []string
		userResources   []string
		expected        bool
	}{
		{name: "public record", recordResources: nil, userResources: nil, expected: true},
		{name: "single match", recordResources: []string{"/p/a"}, userResources: []string{"/p/a"}, expected: true},
		{name: "no match", recordResources: []string{"/p/a"}, userResources: []string{"/p/b"}, expected: false},
		{name: "any match", recordResources: []string{"/p/a", "/p/b"}, userResources: []string{"/p/c", "/p/b"}, expected: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckAccess(tc.recordResources, tc.userResources)
			if got != tc.expected {
				t.Fatalf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

func TestAuthHeaderAndMode(t *testing.T) {
	ctx := context.Background()
	if HasAuthHeader(ctx) {
		t.Fatalf("expected no auth header in empty context")
	}
	if IsGen3Mode(ctx) {
		t.Fatalf("expected not gen3 in empty context")
	}

	ctx = testSessionContext("gen3", true, true, nil, nil)
	if !HasAuthHeader(ctx) {
		t.Fatalf("expected auth header to be present")
	}
	if !IsGen3Mode(ctx) {
		t.Fatalf("expected gen3 mode")
	}
}

func TestGetUserPrivileges(t *testing.T) {
	t.Run("missing key returns empty map", func(t *testing.T) {
		got := GetUserPrivileges(context.Background())
		if len(got) != 0 {
			t.Fatalf("expected empty privileges, got %v", got)
		}
	})

	t.Run("valid map returned", func(t *testing.T) {
		expected := map[string]map[string]bool{"/programs/a/projects/b": {"read": true, "*": false}}
		ctx := testSessionContext("", false, false, nil, expected)
		got := GetUserPrivileges(ctx)
		if !got["/programs/a/projects/b"]["read"] {
			t.Fatalf("expected read=true in privileges")
		}
	})

	t.Run("write alias is normalized before storage", func(t *testing.T) {
		resource := "/programs/a/projects/b"
		ctx := testSessionContext("", false, false, nil, map[string]map[string]bool{resource: {"write": true}})
		got := GetUserPrivileges(ctx)
		if got[resource]["write"] {
			t.Fatalf("did not expect write to persist in session privileges")
		}
		for _, method := range []string{"file_upload", "create", "update", "delete"} {
			if !got[resource][method] {
				t.Fatalf("expected write alias to grant %s", method)
			}
		}
	})
}

func TestHasMethodAccess(t *testing.T) {
	resource := "/programs/a/projects/b"

	t.Run("local mode allows", func(t *testing.T) {
		if !HasMethodAccess(context.Background(), "read", []string{resource}) {
			t.Fatalf("expected local mode to allow access")
		}
	})

	t.Run("local authz-enforced mode checks privileges", func(t *testing.T) {
		ctx := testSessionContext("local", false, true, nil, map[string]map[string]bool{resource: {"read": true}})
		if !HasMethodAccess(ctx, "read", []string{resource}) {
			t.Fatalf("expected read access")
		}
		if HasMethodAccess(ctx, "update", []string{resource}) {
			t.Fatalf("expected update to be denied")
		}
	})

	t.Run("gen3 without auth header denies", func(t *testing.T) {
		ctx := testSessionContext("gen3", false, true, nil, nil)
		if HasMethodAccess(ctx, "read", []string{resource}) {
			t.Fatalf("expected deny without auth header")
		}
	})

	t.Run("gen3 with empty resources denies", func(t *testing.T) {
		ctx := testSessionContext("gen3", true, true, nil, map[string]map[string]bool{resource: {"read": true}})
		if HasMethodAccess(ctx, "read", nil) {
			t.Fatalf("expected deny for empty resource set")
		}
	})

	t.Run("gen3 allows method on all resources", func(t *testing.T) {
		ctx := testSessionContext("gen3", true, true, nil, map[string]map[string]bool{resource: {"read": true}, "/programs/a/x": {"*": true}})
		if !HasMethodAccess(ctx, "read", []string{resource, "/programs/a/x"}) {
			t.Fatalf("expected allow with explicit and wildcard methods")
		}
	})

	t.Run("gen3 denies missing resource privilege", func(t *testing.T) {
		ctx := testSessionContext("gen3", true, true, nil, map[string]map[string]bool{resource: {"read": true}})
		if HasMethodAccess(ctx, "read", []string{resource, "/programs/missing"}) {
			t.Fatalf("expected deny when any resource lacks privilege")
		}
	})

	t.Run("gen3 denies when method missing", func(t *testing.T) {
		ctx := testSessionContext("gen3", true, true, nil, map[string]map[string]bool{resource: {"create": true}})
		if HasMethodAccess(ctx, "read", []string{resource}) {
			t.Fatalf("expected deny for missing method privilege")
		}
	})
}

func TestAuthorizationAnyAllBehavior(t *testing.T) {
	first := "/programs/a/projects/first"
	second := "/programs/a/projects/second"
	ctx := testSessionContext("local", false, true, nil, map[string]map[string]bool{first: {"read": true}, second: {"create": true}})

	if !HasMethodAccess(ctx, "read", []string{first}) {
		t.Fatal("expected a method check to allow the matching resource")
	}
	if HasMethodAccess(ctx, "read", []string{first, second}) {
		t.Fatal("expected HasMethodAccess to require the method on every resource")
	}
	if !HasAnyMethodAccess(ctx, []string{first}, "update", "read") {
		t.Fatal("expected HasAnyMethodAccess to allow one matching method")
	}
	if HasAnyMethodAccess(ctx, []string{first, second}, "read", "create") {
		t.Fatal("expected HasAnyMethodAccess to retain all-resource semantics per method")
	}
	if !HasObjectMethodAccess(ctx, "read", []string{first, second}) {
		t.Fatal("expected object access to allow any matching resource")
	}
}
