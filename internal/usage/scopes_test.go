package usage

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/access"
)

func TestResolveMetricsScopePreservesMetricReadPolicy(t *testing.T) {
	ctx := func(privileges map[string]map[string]bool) context.Context {
		session := access.NewSession("gen3")
		session.AuthHeaderPresent = true
		session.SetAuthorizations(nil, privileges, true)
		return access.WithSession(context.Background(), session)
	}

	t.Run("global read", func(t *testing.T) {
		got, err := ResolveMetricsScope(ctx(map[string]map[string]bool{"/data_file": {"read": true}}), ScopeSelection{})
		if err != nil || !reflect.DeepEqual(got, ScopeQuery{}) {
			t.Fatalf("scope = %+v, err = %v", got, err)
		}
	})
	t.Run("explicit scope", func(t *testing.T) {
		got, err := ResolveMetricsScope(ctx(map[string]map[string]bool{"/programs/org/projects/project": {"read": true}}), ScopeSelection{Organization: "org", Project: "project"})
		want := ScopeQuery{Organization: "org", Project: "project"}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("scope = %+v, err = %v, want %+v", got, err, want)
		}
	})
	t.Run("aggregate suppression dedup and sorting", func(t *testing.T) {
		got, err := ResolveMetricsScope(ctx(map[string]map[string]bool{
			"/programs/z/projects/p":         {"read": true},
			"/programs/org/projects/project": {"*": true},
			"/organization/org":              {"read": true},
			"/organization/z/project/p":      {"read": true},
			"/organization/a":                {"read": true},
		}), ScopeSelection{})
		if err != nil {
			t.Fatalf("ResolveMetricsScope error: %v", err)
		}
		want := []Scope{{Organization: "a"}, {Organization: "org"}, {Organization: "z", Project: "p"}}
		if !reflect.DeepEqual(got.Scopes, want) {
			t.Fatalf("scopes = %+v, want %+v", got.Scopes, want)
		}
		if !reflect.DeepEqual(got.Resources, []string{"/organization/a", "/organization/org", "/organization/z/project/p"}) {
			t.Fatalf("resources = %v", got.Resources)
		}
	})
	t.Run("denied aggregate", func(t *testing.T) {
		_, err := ResolveMetricsScope(ctx(map[string]map[string]bool{}), ScopeSelection{})
		if !errors.Is(err, errorapi.ErrAccessDenied) {
			t.Fatalf("error = %v, want access denied", err)
		}
	})
	t.Run("project requires organization", func(t *testing.T) {
		_, err := ResolveMetricsScope(context.Background(), ScopeSelection{Project: "project"})
		if err == nil {
			t.Fatal("expected missing organization error")
		}
	})
}

func TestParseInactiveSinceUsesSuppliedClock(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 30, 0, 0, time.FixedZone("PDT", -7*60*60))
	days := 3
	got, err := ParseInactiveSince(now, &days)
	if err != nil {
		t.Fatalf("ParseInactiveSince error: %v", err)
	}
	want := now.UTC().AddDate(0, 0, -3)
	if !got.Equal(want) {
		t.Fatalf("cutoff = %v, want %v", got, want)
	}
	negative := -1
	if _, err := ParseInactiveSince(now, &negative); err == nil {
		t.Fatal("expected negative inactive_days error")
	}
}
