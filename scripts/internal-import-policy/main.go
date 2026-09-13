// Command internal-import-policy reports generated-package imports and
// selectors used by a production Go package.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: internal-import-policy PACKAGE_DIR IMPORT_PATH")
		os.Exit(2)
	}

	packageDir, importPath := os.Args[1], os.Args[2]
	entries, err := filepath.Glob(filepath.Join(packageDir, "*.go"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	sort.Strings(entries)

	fset := token.NewFileSet()
	for _, filename := range entries {
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filename, nil, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", filename, err)
			os.Exit(2)
		}
		aliases := importedAliases(file, importPath)
		if len(aliases) == 0 {
			continue
		}
		for _, alias := range aliases {
			fmt.Printf("IMPORT\t%s\t%s\n", filename, alias)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			expression, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			identifier, ok := expression.X.(*ast.Ident)
			if !ok || !slices.Contains(aliases, identifier.Name) {
				return true
			}
			position := fset.Position(expression.Sel.Pos())
			fmt.Printf("SELECTOR\t%s:%d:%d\t%s.%s\n", position.Filename, position.Line, position.Column, identifier.Name, expression.Sel.Name)
			return true
		})
	}
}

func importedAliases(file *ast.File, importPath string) []string {
	var aliases []string
	for _, declaration := range file.Imports {
		path := strings.Trim(declaration.Path.Value, `"`)
		if path != importPath {
			continue
		}
		if declaration.Name == nil {
			parts := strings.Split(path, "/")
			aliases = append(aliases, parts[len(parts)-1])
			continue
		}
		aliases = append(aliases, declaration.Name.Name)
	}
	return aliases
}
