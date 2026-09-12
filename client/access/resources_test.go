package access

import (
	"reflect"
	"sort"
	"testing"
)

func TestResourceAndAuthzHelpers(t *testing.T) {
	t.Run("resource path", func(t *testing.T) {
		tests := []struct {
			name    string
			org     string
			project string
			want    string
			wantErr bool
		}{
			{name: "empty", want: ""},
			{name: "org only", org: "syfon", want: "/organization/syfon"},
			{name: "org and project", org: "syfon", project: "e2e", want: "/organization/syfon/project/e2e"},
			{name: "project without org", project: "e2e", wantErr: true},
		}

		for _, tc := range tests {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				got, err := ResourcePath(tc.org, tc.project)
				if tc.wantErr {
					if err == nil {
						t.Fatalf("expected error, got path=%q", got)
					}
					return
				}
				if err != nil {
					t.Fatalf("ResourcePath returned error: %v", err)
				}
				if got != tc.want {
					t.Fatalf("ResourcePath(%q,%q) = %q, want %q", tc.org, tc.project, got, tc.want)
				}
			})
		}
	})

	t.Run("authz map from scope", func(t *testing.T) {
		if got := AuthzMapFromScope("", ""); got != nil {
			t.Fatalf("expected nil authz map, got %+v", got)
		}
		if got := AuthzMapFromScope("syfon", ""); !reflect.DeepEqual(got, map[string][]string{"syfon": []string{}}) {
			t.Fatalf("unexpected org-wide authz map: %+v", got)
		}
		if got := AuthzMapFromScope("syfon", "e2e"); !reflect.DeepEqual(got, map[string][]string{"syfon": []string{"e2e"}}) {
			t.Fatalf("unexpected project authz map: %+v", got)
		}
	})

	t.Run("authz list to map", func(t *testing.T) {
		got := AuthzListToMap([]string{
			"/organization/syfon",
			" /organization/syfon/project/e2e ",
			"/organization/aced/project/proj-2",
			"/programs/other/projects/proj-1",
		})
		want := map[string][]string{
			"syfon": []string{},
			"other": []string{"proj-1"},
			"aced":  []string{"proj-2"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("AuthzListToMap mismatch:\n got: %+v\nwant: %+v", got, want)
		}
	})

	t.Run("organization-wide entries absorb project entries in either order", func(t *testing.T) {
		for name, paths := range map[string][]string{
			"organization first": {"/organization/org", "/organization/org/project/project"},
			"project first":      {"/organization/org/project/project", "/organization/org"},
		} {
			t.Run(name, func(t *testing.T) {
				want := map[string][]string{"org": {}}
				if got := AuthzListToMap(paths); !reflect.DeepEqual(got, want) {
					t.Fatalf("AuthzListToMap(%v) = %+v, want %+v", paths, got, want)
				}
			})
		}
	})

	t.Run("normalize access resource aliases", func(t *testing.T) {
		tests := []struct {
			raw  string
			want string
		}{
			{raw: "/organization/cbds", want: "/organization/cbds"},
			{raw: "/organization/cbds/project/training", want: "/organization/cbds/project/training"},
			{raw: "https://example.org/organization/cbds/project/training", want: "/organization/cbds/project/training"},
			{raw: "/programs/cbds/projects/training", want: "/organization/cbds/project/training"},
			{raw: "/organizations/cbds/projects/training", want: "/organization/cbds/project/training"},
			{raw: "/program/cbds/project/training", want: "/organization/cbds/project/training"},
			{raw: "/programs", want: "/programs"},
		}
		for _, tc := range tests {
			if got := NormalizeAccessResource(tc.raw); got != tc.want {
				t.Fatalf("NormalizeAccessResource(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		}
	})

	t.Run("rejects malformed recognized paths and preserves unknown prefixes", func(t *testing.T) {
		for _, raw := range []string{
			"/organization",
			"/organization/cbds/project",
			"/organization/cbds/project/training/extra",
			"/organization/cbds/unknown/training",
			"/organizations//project/training",
			"/programs/cbds/projects/",
		} {
			if got := NormalizeAccessResource(raw); got != "" {
				t.Errorf("NormalizeAccessResource(%q) = %q, want empty", raw, got)
			}
		}
		for _, raw := range []string{"/data_file", "/custom/scope", "s3://bucket/object"} {
			if got := NormalizeAccessResource(raw); got != raw {
				t.Errorf("NormalizeAccessResource(%q) = %q, want unchanged", raw, got)
			}
		}
	})

	t.Run("authz map to list", func(t *testing.T) {
		got := AuthzMapToList(map[string][]string{
			"syfon": {"e2e", "e2e-2"},
			"other": {},
		})
		sort.Strings(got)
		want := []string{
			"/organization/other",
			"/organization/syfon/project/e2e",
			"/organization/syfon/project/e2e-2",
		}
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("AuthzMapToList mismatch:\n got: %+v\nwant: %+v", got, want)
		}
	})

	t.Run("controlled access round trip", func(t *testing.T) {
		claims := []string{
			" /programs/syfon/projects/e2e ",
			"/organization/syfon/project/e2e",
			"https://example.test/organization/other",
			"",
		}
		got := ControlledAccessToAuthzMap(claims)
		want := map[string][]string{"syfon": {"e2e"}, "other": {}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ControlledAccessToAuthzMap mismatch:\n got: %+v\nwant: %+v", got, want)
		}
		list := AuthzMapToControlledAccess(got)
		sort.Strings(list)
		wantList := []string{"/organization/other", "/organization/syfon/project/e2e"}
		if !reflect.DeepEqual(list, wantList) {
			t.Fatalf("AuthzMapToControlledAccess mismatch:\n got: %+v\nwant: %+v", list, wantList)
		}
	})

	t.Run("resource scope", func(t *testing.T) {
		for _, tc := range []struct {
			resource     string
			organization string
			project      string
			ok           bool
		}{
			{resource: "/organization/syfon", organization: "syfon", ok: true},
			{resource: "/programs/syfon/projects/e2e", organization: "syfon", project: "e2e", ok: true},
			{resource: "/unknown/syfon", ok: false},
		} {
			organization, project, ok := ResourceScope(tc.resource)
			if organization != tc.organization || project != tc.project || ok != tc.ok {
				t.Fatalf("ResourceScope(%q) = %q, %q, %t", tc.resource, organization, project, ok)
			}
		}
	})

}
