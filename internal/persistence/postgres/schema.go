package postgres

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed object_schema.sql
var objectSchemaSQL string

func objectSchemaStatements() ([]string, error) {
	raw := strings.TrimSpace(objectSchemaSQL)
	if raw == "" {
		return nil, fmt.Errorf("object schema is empty")
	}

	parts := strings.Split(raw, ";")
	stmts := make([]string, 0, len(parts))
	for _, part := range parts {
		stmt := strings.TrimSpace(part)
		if stmt == "" {
			continue
		}
		stmts = append(stmts, stmt)
	}
	return stmts, nil
}
