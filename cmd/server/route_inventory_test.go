package server

import (
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/config"
)

const wp01RouteInventory = "../../docs/internal-architecture-plan/wp01-route-inventory.tsv"

type inventoryRoute struct {
	Method string
	Path   string
	Owner  string
}

// TestMountedRouteParity pins the complete method/path set produced by all
// options in cmd/server/options.go. The inventory includes Fiber's root
// middleware entries because they are part of app.GetRoutes(false), and keeps
// aliases and optional workflow groups visible to later package moves.
func TestMountedRouteParity(t *testing.T) {
	expected := readWP01RouteInventory(t)
	app := buildMockServerRouterWithRoutes(config.RoutesConfig{
		Docs: true, Ga4gh: true, Metrics: true, Internal: true, LFS: true,
	})

	actual := make(map[string]struct{})
	for _, route := range app.GetRoutes(false) {
		actual[route.Method+"\t"+route.Path] = struct{}{}
	}

	for key := range expected {
		if _, ok := actual[key]; !ok {
			t.Errorf("route missing from mounted runtime: %s", strings.ReplaceAll(key, "\t", " "))
		}
	}
	for key := range actual {
		if _, ok := expected[key]; !ok {
			t.Errorf("unexpected mounted runtime route: %s", strings.ReplaceAll(key, "\t", " "))
		}
	}
	if len(actual) != len(expected) {
		t.Fatalf("mounted route count = %d, inventory count = %d", len(actual), len(expected))
	}
}

func readWP01RouteInventory(t *testing.T) map[string]inventoryRoute {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed while locating route inventory")
	}
	file, err := os.Open(filepath.Join(filepath.Dir(source), wp01RouteInventory))
	if err != nil {
		t.Fatalf("open route inventory: %v", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comma = '\t'
	reader.FieldsPerRecord = 3
	header, err := reader.Read()
	if err != nil {
		t.Fatalf("read route inventory header: %v", err)
	}
	if len(header) != 3 || header[0] != "method" || header[1] != "path" || header[2] != "owner" {
		t.Fatalf("unexpected route inventory header: %v", header)
	}

	routes := map[string]inventoryRoute{}
	for {
		row, err := reader.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("read route inventory: %v", err)
		}
		method, path, owner := row[0], row[1], row[2]
		if method == "" || path == "" || owner == "" {
			t.Fatalf("invalid route inventory row: %v", row)
		}
		key := method + "\t" + path
		if _, exists := routes[key]; exists {
			t.Fatalf("duplicate route inventory row: %s", strings.ReplaceAll(key, "\t", " "))
		}
		routes[key] = inventoryRoute{Method: method, Path: path, Owner: owner}
	}
	return routes
}
