package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"

	"github.com/calypr/syfon/internal/objects"
)

type resourceFilterDialect interface {
	ResourceFilter(string, []string, bool, int) (string, []any)
}

func (db *Store) ResolveObjectAlias(ctx context.Context, aliasID string) (string, error) {
	aliasID = strings.TrimSpace(aliasID)
	if aliasID == "" {
		return "", errorapi.ErrObjectNotFound
	}
	var canonicalID string
	err := db.queryRowContext(ctx, "SELECT object_id FROM drs_object_alias WHERE alias_id = ?", aliasID).Scan(&canonicalID)
	if err == sql.ErrNoRows {
		return "", errorapi.ErrObjectNotFound
	}
	if err != nil {
		return "", err
	}
	return canonicalID, nil
}

func (db *Store) GetBulkObjects(ctx context.Context, ids []string) ([]objects.Record, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	objectsByID, err := db.fetchObjectsByIDsOrChecksums(ctx, ids, nil)
	if err != nil {
		return nil, err
	}
	objects := make([]objects.Record, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		obj, ok := objectsByID[id]
		if !ok {
			resolved, resolveErr := db.ResolveObjectAlias(ctx, id)
			if resolveErr == nil {
				obj, ok = objectsByID[resolved]
				if !ok {
					obj, resolveErr = db.GetObject(ctx, resolved)
					ok = resolveErr == nil
				}
			} else if !errors.Is(resolveErr, errorapi.ErrNotFound) {
				return nil, resolveErr
			}
			if resolveErr != nil && !errors.Is(resolveErr, errorapi.ErrNotFound) {
				return nil, resolveErr
			}
		}
		if !ok || obj == nil {
			continue
		}
		if _, already := seen[string(obj.Id)]; already {
			continue
		}
		seen[string(obj.Id)] = struct{}{}
		objects = append(objects, *obj)
	}
	return objects, nil
}

func (db *Store) GetObjectsByChecksums(ctx context.Context, checksums []string) (map[string][]objects.Record, error) {
	if len(checksums) == 0 {
		return nil, nil
	}
	objectsByID, err := db.fetchObjectsByIDsOrChecksums(ctx, nil, checksums)
	if err != nil {
		return nil, err
	}
	index := make(map[string][]objects.Record, len(objectsByID)*2)
	for _, obj := range objectsByID {
		index[string(obj.Id)] = append(index[string(obj.Id)], *obj)
		for _, cs := range obj.Checksums {
			value := strings.TrimSpace(cs.Checksum)
			if value == "" {
				continue
			}
			index[value] = append(index[value], *obj)
			if objects.NormalizeChecksumType(cs.Type) == "sha256" {
				if normalized, ok := objects.NormalizeSHA256Query(value); ok {
					index[normalized] = append(index[normalized], *obj)
				}
			}
		}
	}
	result := make(map[string][]objects.Record, len(checksums))
	for _, cs := range checksums {
		requested := strings.TrimSpace(cs)
		if requested == "" {
			continue
		}
		lookup := requested
		if normalized, ok := objects.NormalizeSHA256Query(requested); ok {
			lookup = normalized
		}
		if objs := index[lookup]; len(objs) > 0 {
			result[requested] = uniqueObjectsByID(objs)
		}
	}
	return result, nil
}

func (db *Store) ListScopedObjectIDsByChecksums(ctx context.Context, organization, project string, checksums []string) (map[string][]string, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization == "" || project == "" || len(checksums) == 0 {
		return map[string][]string{}, nil
	}
	resource, err := clientaccess.ResourcePath(organization, project)
	if err != nil {
		return nil, err
	}
	normalized := make([]string, 0, len(checksums))
	seenChecksums := make(map[string]struct{}, len(checksums))
	for _, checksum := range checksums {
		value := normalizeChecksumLookup(checksum)
		if value == "" {
			continue
		}
		if _, exists := seenChecksums[value]; exists {
			continue
		}
		seenChecksums[value] = struct{}{}
		normalized = append(normalized, value)
	}
	if len(normalized) == 0 {
		return map[string][]string{}, nil
	}
	// Keep the checksum predicate below within SQLite's bound-parameter limit.
	// The two fixed parameters are the project resource and checksum type.
	maxChecksums := db.dialect.MaxParameters() - 2
	if len(normalized) > maxChecksums {
		out := make(map[string][]string, len(normalized))
		for start := 0; start < len(normalized); start += maxChecksums {
			end := start + maxChecksums
			if end > len(normalized) {
				end = len(normalized)
			}
			part, err := db.ListScopedObjectIDsByChecksums(ctx, organization, project, normalized[start:end])
			if err != nil {
				return nil, err
			}
			for checksum, ids := range part {
				out[checksum] = append(out[checksum], ids...)
			}
		}
		return out, nil
	}
	args := make([]any, 0, len(normalized)+2)
	args = append(args, resource, "sha256")
	checksumCondition, checksumArgs := db.dialect.ListArgs("replace(lower(trim(c.checksum)), 'sha256:', '')", normalized)
	args = append(args, checksumArgs...)
	rows, err := db.queryContext(ctx, fmt.Sprintf(`
		SELECT DISTINCT replace(lower(trim(c.checksum)), 'sha256:', ''), c.object_id
		FROM drs_object_checksum c
		INNER JOIN drs_object_controlled_access ca ON ca.object_id = c.object_id
		WHERE ca.resource = ? AND replace(lower(trim(c.type)), '-', '') = ?
		  AND %s
		ORDER BY 1, 2`, checksumCondition), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]string, len(normalized))
	for _, checksum := range normalized {
		out[checksum] = []string{}
	}
	seen := make(map[string]map[string]struct{}, len(normalized))
	for rows.Next() {
		var checksum string
		var objectID string
		if err := rows.Scan(&checksum, &objectID); err != nil {
			return nil, err
		}
		checksum = strings.TrimSpace(checksum)
		objectID = strings.TrimSpace(objectID)
		if checksum == "" || objectID == "" {
			continue
		}
		if seen[checksum] == nil {
			seen[checksum] = make(map[string]struct{})
		}
		if _, ok := seen[checksum][objectID]; ok {
			continue
		}
		seen[checksum][objectID] = struct{}{}
		out[checksum] = append(out[checksum], objectID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (db *Store) ListObjectIDsByScope(ctx context.Context, organization, project string) ([]string, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization == "" {
		rows, err := db.queryContext(ctx, `SELECT id FROM drs_object ORDER BY id`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanObjectIDs(rows)
	}

	var (
		rows *sql.Rows
		err  error
	)
	if project != "" {
		resource, resourceErr := clientaccess.ResourcePath(organization, project)
		if resourceErr != nil {
			return nil, resourceErr
		}
		rows, err = db.queryContext(ctx, `
			SELECT DISTINCT ca.object_id
			FROM drs_object_controlled_access ca
			INNER JOIN drs_object o ON o.id = ca.object_id
			WHERE ca.resource = ?
			ORDER BY ca.object_id`, resource)
	} else {
		condition, scopeArgs, scopeErr := scopeResourceCondition("ca.resource", organization, "")
		if scopeErr != nil {
			return nil, scopeErr
		}
		rows, err = db.queryContext(ctx, `
			SELECT DISTINCT ca.object_id
			FROM drs_object_controlled_access ca
			INNER JOIN drs_object o ON o.id = ca.object_id
			WHERE `+condition+`
			ORDER BY ca.object_id`, scopeArgs...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanObjectIDs(rows)
}

func (db *Store) ListObjectIDsByResources(ctx context.Context, resources []string, includeUnscoped bool) ([]string, error) {
	resources = clientaccess.NormalizeAccessResources(resources)
	if len(resources) == 0 && !includeUnscoped {
		return []string{}, nil
	}

	args := make([]any, 0, len(resources))
	parts := make([]string, 0, 2)
	if len(resources) > 0 {
		placeholders := make([]string, 0, len(resources))
		for _, resource := range resources {
			args = append(args, resource)
			placeholders = append(placeholders, "?")
		}
		parts = append(parts, `EXISTS (
			SELECT 1
			FROM drs_object_controlled_access ca
			WHERE ca.object_id = o.id AND ca.resource IN (`+strings.Join(placeholders, ",")+`)
		)`)
	}
	if includeUnscoped {
		parts = append(parts, `NOT EXISTS (
			SELECT 1
			FROM drs_object_controlled_access ca
			WHERE ca.object_id = o.id
		)`)
	}

	rows, err := db.queryContext(ctx, `
		SELECT DISTINCT o.id
		FROM drs_object o
		WHERE `+strings.Join(parts, " OR ")+`
		ORDER BY o.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanObjectIDs(rows)
}

func (db *Store) ListObjectIDsPageByScope(ctx context.Context, organization, project, startAfter string, limit, offset int) ([]string, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	startAfter = strings.TrimSpace(startAfter)
	if limit <= 0 {
		return []string{}, nil
	}
	if offset < 0 {
		offset = 0
	}

	args := make([]any, 0, 4)
	conditions := make([]string, 0, 2)
	baseQuery := `SELECT id FROM drs_object`
	orderBy := ` ORDER BY id`
	objectIDExpr := "id"

	if organization != "" {
		condition, scopeArgs, err := scopeResourceCondition("ca.resource", organization, project)
		if err != nil {
			return nil, err
		}
		args = append(args, scopeArgs...)
		baseQuery = `
			SELECT DISTINCT ca.object_id AS id
			FROM drs_object_controlled_access ca
			INNER JOIN drs_object o ON o.id = ca.object_id
		`
		objectIDExpr = "ca.object_id"
		conditions = append(conditions, condition)
		orderBy = ` ORDER BY ca.object_id`
	}
	if startAfter != "" {
		args = append(args, startAfter)
		conditions = append(conditions, objectIDExpr+" > ?")
	}
	query := baseQuery
	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	query += orderBy + ` LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := db.queryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanObjectIDs(rows)
}

func (db *Store) ListObjectIDsByScopeAndResources(ctx context.Context, organization, project string, resources []string, restrictToResources bool) ([]string, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization == "" {
		if !restrictToResources {
			rows, err := db.queryContext(ctx, `SELECT id FROM drs_object ORDER BY id`)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			return scanObjectIDs(rows)
		}
		return db.ListObjectIDsByResources(ctx, resources, false)
	}

	scopeCondition, scopeArgs, err := scopeResourceCondition("ca_scope.resource", organization, project)
	if err != nil {
		return nil, err
	}
	args := make([]any, 0, len(resources)+1)
	args = append(args, scopeArgs...)
	query := `
		SELECT DISTINCT o.id
		FROM drs_object o
		WHERE EXISTS (
			SELECT 1
			FROM drs_object_controlled_access ca_scope
		WHERE ca_scope.object_id = o.id AND ` + scopeCondition + `
		)`
	if restrictToResources {
		resources = clientaccess.NormalizeAccessResources(resources)
		if len(resources) == 0 {
			return []string{}, nil
		}
		placeholders := make([]string, 0, len(resources))
		for _, resource := range resources {
			args = append(args, resource)
			placeholders = append(placeholders, "?")
		}
		query += `
		AND EXISTS (
			SELECT 1
			FROM drs_object_controlled_access ca_auth
			WHERE ca_auth.object_id = o.id AND ca_auth.resource IN (` + strings.Join(placeholders, ",") + `)
		)`
	}
	query += ` ORDER BY o.id`
	rows, err := db.queryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanObjectIDs(rows)
}

func (db *Store) ListObjectIDsByChecksumsAndResources(ctx context.Context, checksums []string, resources []string, includeUnscoped, restrictToResources bool) (map[string][]string, error) {
	normalized := make([]string, 0, len(checksums))
	for _, checksum := range checksums {
		if trimmed := strings.TrimSpace(checksum); trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	checksums = normalized
	if len(checksums) == 0 {
		return map[string][]string{}, nil
	}

	args := make([]any, 0, len(checksums)*2+len(resources))
	idPlaceholders := make([]string, 0, len(checksums))
	checksumPlaceholders := make([]string, 0, len(checksums))
	for _, checksum := range checksums {
		args = append(args, checksum)
		idPlaceholders = append(idPlaceholders, "?")
	}
	for _, checksum := range checksums {
		args = append(args, checksum)
		checksumPlaceholders = append(checksumPlaceholders, "?")
	}
	query := `
		WITH matched AS (
			SELECT id AS object_id, id AS match_key
			FROM drs_object
			WHERE id IN (` + strings.Join(idPlaceholders, ",") + `)
			UNION
			SELECT c.object_id, c.checksum AS match_key
			FROM drs_object_checksum c
			WHERE replace(lower(trim(c.checksum)), 'sha256:', '') IN (` + strings.Join(checksumPlaceholders, ",") + `)
		)
		SELECT m.match_key, m.object_id
		FROM matched m
		INNER JOIN drs_object o ON o.id = m.object_id`
	if restrictToResources {
		resources = clientaccess.NormalizeAccessResources(resources)
		if len(resources) == 0 && !includeUnscoped {
			return map[string][]string{}, nil
		}
		parts := make([]string, 0, 2)
		if len(resources) > 0 {
			placeholders := make([]string, 0, len(resources))
			for _, resource := range resources {
				args = append(args, resource)
				placeholders = append(placeholders, "?")
			}
			parts = append(parts, `EXISTS (
				SELECT 1
				FROM drs_object_controlled_access ca_auth
				WHERE ca_auth.object_id = o.id AND ca_auth.resource IN (`+strings.Join(placeholders, ",")+`)
			)`)
		}
		if includeUnscoped {
			parts = append(parts, `NOT EXISTS (
				SELECT 1
				FROM drs_object_controlled_access ca_auth
				WHERE ca_auth.object_id = o.id
			)`)
		}
		query += ` WHERE (` + strings.Join(parts, " OR ") + `)`
	}
	query += ` ORDER BY m.match_key, m.object_id`
	rows, err := db.queryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanChecksumMatchRows(rows)
}

func (db *Store) ListObjectIDsPageByURL(ctx context.Context, objectURL, organization, project, startAfter string, limit, offset int, resources []string, includeUnscoped, restrictToResources bool) ([]string, error) {
	objectURL = strings.TrimSpace(objectURL)
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	startAfter = strings.TrimSpace(startAfter)
	if objectURL == "" || limit <= 0 {
		return []string{}, nil
	}
	if offset < 0 {
		offset = 0
	}

	args := []any{objectURL}
	conditions := []string{"am.url = ?"}
	if organization != "" {
		scopeCondition, scopeArgs, err := scopeResourceCondition("ca_scope.resource", organization, project)
		if err != nil {
			return nil, err
		}
		args = append(args, scopeArgs...)
		conditions = append(conditions, `EXISTS (
			SELECT 1
			FROM drs_object_controlled_access ca_scope
			WHERE ca_scope.object_id = o.id AND `+scopeCondition+`
		)`)
	}
	if restrictToResources {
		resources = clientaccess.NormalizeAccessResources(resources)
		if len(resources) == 0 && !includeUnscoped {
			return []string{}, nil
		}
		if filter, ok := db.dialect.(resourceFilterDialect); ok {
			filterSQL, filterArgs := filter.ResourceFilter("ca_auth.resource", resources, includeUnscoped, len(args)+1)
			conditions = append(conditions, filterSQL)
			args = append(args, filterArgs...)
		} else {
			parts := make([]string, 0, 2)
			if len(resources) > 0 {
				resourceCondition, resourceArgs := db.dialect.ListArgs("ca_auth.resource", resources)
				parts = append(parts, `EXISTS (
					SELECT 1
					FROM drs_object_controlled_access ca_auth
					WHERE ca_auth.object_id = o.id AND `+resourceCondition+`
				)`)
				args = append(args, resourceArgs...)
			}
			if includeUnscoped {
				parts = append(parts, `NOT EXISTS (
					SELECT 1
					FROM drs_object_controlled_access ca_auth
					WHERE ca_auth.object_id = o.id
				)`)
			}
			conditions = append(conditions, "("+strings.Join(parts, " OR ")+")")
		}
	}
	if startAfter != "" {
		args = append(args, startAfter)
		conditions = append(conditions, "o.id > ?")
	}

	query := `
		SELECT DISTINCT o.id
		FROM drs_object o
		INNER JOIN drs_object_access_method am ON am.object_id = o.id
		WHERE ` + strings.Join(conditions, " AND ") + `
		ORDER BY o.id
		LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := db.queryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanObjectIDs(rows)
}

func (db *Store) GetObjectsByChecksum(ctx context.Context, checksum string) ([]objects.Record, error) {
	checksum = strings.TrimSpace(checksum)
	if checksum == "" {
		return []objects.Record{}, nil
	}
	objectsByID, err := db.fetchObjectsByIDsOrChecksums(ctx, nil, []string{checksum})
	if err != nil {
		return nil, err
	}
	if len(objectsByID) == 0 {
		return []objects.Record{}, nil
	}
	out := make([]objects.Record, 0, len(objectsByID))
	for _, obj := range objectsByID {
		out = append(out, *obj)
	}
	return uniqueObjectsByID(out), nil
}

func scopeResourceCondition(column, organization, project string) (string, []any, error) {
	resource, err := clientaccess.ResourcePath(organization, project)
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(project) != "" {
		return column + " = ?", []any{resource}, nil
	}
	return "(" + column + " = ? OR " + column + " LIKE ? ESCAPE '\\')", []any{resource, likeEscape(resource+"/project/") + "%"}, nil
}

func likeEscape(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

func scanObjectIDs(rows *sql.Rows) ([]string, error) {
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func scanChecksumMatchRows(rows *sql.Rows) (map[string][]string, error) {
	out := make(map[string][]string)
	for rows.Next() {
		var checksum, objectID string
		if err := rows.Scan(&checksum, &objectID); err != nil {
			return nil, err
		}
		ids := out[checksum]
		if len(ids) > 0 && ids[len(ids)-1] == objectID {
			continue
		}
		out[checksum] = append(ids, objectID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
