package httpapi

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var prohibitedHTTPImports = []string{
	"database/sql",
	"github.com/aws/aws-sdk-go-v2",
	"github.com/Azure/azure-sdk-for-go",
	"cloud.google.com/go",
	"gocloud.dev",
	"github.com/calypr/syfon/internal/persistence",
	"github.com/calypr/syfon/internal/storage",
}

var domainRoots = []string{
	"../buckets",
	"../objects",
	"../projects/storage",
	"../usage",
}

func TestHTTPBoundaryImports(t *testing.T) {
	for _, file := range productionGoFiles(t, ".") {
		for _, imported := range goImports(t, file) {
			for _, prohibited := range prohibitedHTTPImports {
				if imported == prohibited || strings.HasPrefix(imported, prohibited+"/") {
					t.Errorf("HTTP adapter %s imports prohibited dependency %s", file, imported)
				}
			}
			for _, retired := range []string{
				"github.com/calypr/syfon/internal/httpapi/maintenance",
				"github.com/calypr/syfon/internal/httpapi/records",
				"github.com/calypr/syfon/internal/httpapi/transfers",
			} {
				if imported == retired || strings.HasPrefix(imported, retired+"/") {
					t.Errorf("HTTP adapter %s imports retired HTTP package %s", file, imported)
				}
			}
		}
	}
}

func TestRetiredHTTPPackagesAreAbsent(t *testing.T) {
	for _, directory := range []string{"maintenance", "records", "transfers"} {
		path := filepath.Join(".", directory)
		for _, file := range productionGoFiles(t, path) {
			t.Errorf("retired HTTP package contains production file %s", file)
		}
	}
}

func TestDomainPackagesDoNotImportHTTPAdapters(t *testing.T) {
	for _, root := range domainRoots {
		for _, file := range productionGoFiles(t, root) {
			for _, imported := range goImports(t, file) {
				if imported == "github.com/gofiber/fiber/v3" || strings.HasPrefix(imported, "github.com/gofiber/fiber/v3/") {
					t.Errorf("domain file %s imports Fiber: %s", file, imported)
				}
				if imported == "github.com/calypr/syfon/internal/httpapi" || strings.HasPrefix(imported, "github.com/calypr/syfon/internal/httpapi/") {
					t.Errorf("domain file %s imports HTTP adapter: %s", file, imported)
				}
			}
		}
	}
}

func productionGoFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipDir
			}
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(files)
	return files
}

func goImports(t *testing.T, file string) []string {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	imports := make([]string, 0, len(parsed.Imports))
	for _, spec := range parsed.Imports {
		imports = append(imports, strings.Trim(spec.Path.Value, `"`))
	}
	return imports
}
