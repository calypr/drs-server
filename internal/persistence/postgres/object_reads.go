package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sycommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
	"github.com/lib/pq"
)

type objectRow struct {
	ID          string
	Size        int64
	CreatedTime time.Time
	UpdatedTime time.Time
	Name        string
	Version     string
	Description string
}

func (db *PostgresDB) ResolveObjectAlias(ctx context.Context, aliasID string) (string, error) {
	aliasID = strings.TrimSpace(aliasID)
	if aliasID == "" {
		return "", fmt.Errorf("%w: object not found", faults.ErrNotFound)
	}
	var canonicalID string
	err := db.db.QueryRowContext(ctx, "SELECT object_id FROM drs_object_alias WHERE alias_id = $1", aliasID).Scan(&canonicalID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("%w: object not found", faults.ErrNotFound)
	}
	if err != nil {
		return "", err
	}
	return canonicalID, nil
}

func (db *PostgresDB) GetObject(ctx context.Context, id string) (*objects.Record, error) {
	requestID := strings.TrimSpace(id)
	lookupID := requestID
	resolvedAlias := false

retryLookup:
	// 1. Fetch main record
	var r objectRow
	var name, version, description sql.NullString
	err := db.db.QueryRowContext(ctx, `
		SELECT id, size, created_time, updated_time, name, version, description
		FROM drs_object WHERE id = $1`, lookupID).Scan(
		&r.ID, &r.Size, &r.CreatedTime, &r.UpdatedTime, &name, &version, &description,
	)
	if err == sql.ErrNoRows {
		if !resolvedAlias {
			canonicalID, aliasErr := db.ResolveObjectAlias(ctx, requestID)
			if aliasErr == nil && strings.TrimSpace(canonicalID) != "" {
				lookupID = strings.TrimSpace(canonicalID)
				resolvedAlias = true
				goto retryLookup
			}
			if aliasErr != nil && !errors.Is(aliasErr, faults.ErrNotFound) {
				return nil, aliasErr
			}
		}
		return nil, fmt.Errorf("%w: object not found", faults.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to fetch record: %w", err)
	}
	r.Name = strings.TrimSpace(name.String)
	r.Version = version.String
	r.Description = description.String
	nameAliases, err := db.nameAliasesForObject(ctx, lookupID)
	if err != nil {
		return nil, err
	}
	objectID := r.ID

	obj := &objects.Record{
		Id:          objects.RecordID(objectID),
		Size:        r.Size,
		CreatedTime: r.CreatedTime,
		UpdatedTime: postgresPtr(r.UpdatedTime),
		Version:     postgresPtr(r.Version),
		Description: postgresPtr(r.Description),
		Name:        postgresPtr(r.Name),
		SelfUri:     "drs://" + objectID,
		NameAliases: nameAliases,
		Properties:  map[string]json.RawMessage{},
	}
	// 2. Fetch storage access methods.
	urlRows, err := db.db.QueryContext(ctx, "SELECT url, type FROM drs_object_access_method WHERE object_id = $1", lookupID)
	if err != nil {
		return nil, err
	}
	defer urlRows.Close()
	seenAccess := make(map[string]struct{})
	for urlRows.Next() {
		var u, t string
		if err := urlRows.Scan(&u, &t); err != nil {
			return nil, err
		}
		key := t + "|" + u
		if _, ok := seenAccess[key]; ok {
			continue
		}
		seenAccess[key] = struct{}{}
		if obj.AccessMethods == nil {
			obj.AccessMethods = &[]objects.AccessMethod{}
		}
		am := objects.AccessMethod{
			AccessUrl: &objects.AccessURL{Url: u},
			Type:      t,
			AccessId:  postgresPtr(objects.AccessMethodID(t, u)),
		}
		*obj.AccessMethods = append(*obj.AccessMethods, am)
	}
	controlled, err := db.controlledAccessForObject(ctx, lookupID)
	if err != nil {
		return nil, err
	}
	if len(controlled) > 0 {
		obj.ControlledAccess = &controlled
		obj.Authorizations = sycommon.ControlledAccessToAuthzMap(controlled)
	}
	obj.PublicRead, obj.PublicReadPolicyKnown, err = db.publicReadForObject(ctx, lookupID, len(controlled) == 0)
	if err != nil {
		return nil, err
	}

	// 3. Fetch Checksums
	hashRows, err := db.db.QueryContext(ctx, "SELECT type, checksum FROM drs_object_checksum WHERE object_id = $1", lookupID)
	if err != nil {
		return nil, err
	}
	defer hashRows.Close()
	seenChecksum := make(map[string]struct{})
	for hashRows.Next() {
		var t, v string
		if err := hashRows.Scan(&t, &v); err != nil {
			return nil, err
		}
		key := t + "|" + v
		if _, ok := seenChecksum[key]; ok {
			continue
		}
		seenChecksum[key] = struct{}{}
		obj.Checksums = append(obj.Checksums, objects.Checksum{Type: t, Checksum: v})
	}

	return obj, nil
}

func (db *PostgresDB) GetBulkObjects(ctx context.Context, ids []string) ([]objects.Record, error) {
	if len(ids) == 0 {
		return []objects.Record{}, nil
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
			} else if !errors.Is(resolveErr, faults.ErrNotFound) {
				return nil, resolveErr
			}
			if resolveErr != nil && !errors.Is(resolveErr, faults.ErrNotFound) {
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

func (db *PostgresDB) GetObjectsByChecksum(ctx context.Context, checksum string) ([]objects.Record, error) {
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

func (db *PostgresDB) GetObjectsByChecksums(ctx context.Context, checksums []string) (map[string][]objects.Record, error) {
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

func (db *PostgresDB) ListScopedObjectIDsByChecksums(ctx context.Context, organization, project string, checksums []string) (map[string][]string, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization == "" || project == "" || len(checksums) == 0 {
		return map[string][]string{}, nil
	}
	resource, err := sycommon.ResourcePath(organization, project)
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
	rows, err := db.db.QueryContext(ctx, `
		SELECT DISTINCT replace(lower(trim(c.checksum)), 'sha256:', ''), c.object_id
		FROM drs_object_checksum c
		INNER JOIN drs_object_controlled_access ca ON ca.object_id = c.object_id
		WHERE ca.resource = $1 AND replace(lower(trim(c.type)), '-', '') = $2
		  AND replace(lower(trim(c.checksum)), 'sha256:', '') = ANY($3)
		ORDER BY 1, 2`, resource, "sha256", pq.Array(normalized))
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

func (db *PostgresDB) ListObjectIDsByScope(ctx context.Context, organization, project string) ([]string, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization == "" {
		rows, err := db.db.QueryContext(ctx, `SELECT id FROM drs_object ORDER BY id`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

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

	var (
		rows *sql.Rows
		err  error
	)
	if project != "" {
		resource, err := sycommon.ResourcePath(organization, project)
		if err != nil {
			return nil, err
		}
		rows, err = db.db.QueryContext(ctx, `
			SELECT DISTINCT ca.object_id
			FROM drs_object_controlled_access ca
			INNER JOIN drs_object o ON o.id = ca.object_id
			WHERE ca.resource = $1
			ORDER BY ca.object_id`, resource)
	} else {
		scopeCondition, scopeArgs, scopeErr := postgresScopeResourceCondition("ca.resource", organization, "")
		if scopeErr != nil {
			return nil, scopeErr
		}
		rows, err = db.db.QueryContext(ctx, `
				SELECT DISTINCT ca.object_id
				FROM drs_object_controlled_access ca
				INNER JOIN drs_object o ON o.id = ca.object_id
				WHERE `+postgresRebindQuestionPlaceholders(scopeCondition, 1)+`
				ORDER BY ca.object_id`, scopeArgs...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (db *PostgresDB) ListObjectIDsPageByScope(ctx context.Context, organization, project, startAfter string, limit, offset int) ([]string, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	startAfter = strings.TrimSpace(startAfter)
	if limit <= 0 {
		return []string{}, nil
	}
	if offset < 0 {
		offset = 0
	}

	queryArgs := make([]any, 0, 4)
	conditions := make([]string, 0, 2)
	baseQuery := `SELECT id FROM drs_object`
	orderBy := ` ORDER BY id`
	objectIDExpr := "id"

	if organization != "" {
		scopeCondition, scopeArgs, err := postgresScopeResourceCondition("ca.resource", organization, project)
		if err != nil {
			return nil, err
		}
		queryArgs = append(queryArgs, scopeArgs...)
		baseQuery = `
			SELECT DISTINCT ca.object_id AS id
			FROM drs_object_controlled_access ca
			INNER JOIN drs_object o ON o.id = ca.object_id
		`
		objectIDExpr = "ca.object_id"
		conditions = append(conditions, postgresRebindQuestionPlaceholders(scopeCondition, len(queryArgs)-len(scopeArgs)+1))
		orderBy = ` ORDER BY ca.object_id`
	}
	if startAfter != "" {
		queryArgs = append(queryArgs, startAfter)
		conditions = append(conditions, fmt.Sprintf("%s > $%d", objectIDExpr, len(queryArgs)))
	}
	query := baseQuery
	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	query += orderBy
	queryArgs = append(queryArgs, limit, offset)
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(queryArgs)-1, len(queryArgs))
	rows, err := db.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanObjectIDs(rows)
}

func (db *PostgresDB) ListObjectIDsByResources(ctx context.Context, resources []string, includeUnscoped bool) ([]string, error) {
	resources = sycommon.NormalizeAccessResources(resources)
	if len(resources) == 0 && !includeUnscoped {
		return []string{}, nil
	}
	rows, err := db.db.QueryContext(ctx, `
		SELECT DISTINCT o.id
		FROM drs_object o
		WHERE (
			COALESCE(array_length($1::text[], 1), 0) > 0
			AND EXISTS (
				SELECT 1
				FROM drs_object_controlled_access ca
				WHERE ca.object_id = o.id AND ca.resource = ANY($1)
			)
		) OR (
			$2
			AND NOT EXISTS (
				SELECT 1
				FROM drs_object_controlled_access ca
				WHERE ca.object_id = o.id
			)
		)
		ORDER BY o.id`, pq.Array(resources), includeUnscoped)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (db *PostgresDB) ListObjectIDsPageByResources(ctx context.Context, resources []string, includeUnscoped bool, startAfter string, limit, offset int) ([]string, error) {
	resources = sycommon.NormalizeAccessResources(resources)
	startAfter = strings.TrimSpace(startAfter)
	if limit <= 0 || (len(resources) == 0 && !includeUnscoped) {
		return []string{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	queryArgs := []any{pq.Array(resources), includeUnscoped}
	query := `
		SELECT DISTINCT o.id
		FROM drs_object o
		WHERE ((
			COALESCE(array_length($1::text[], 1), 0) > 0
			AND EXISTS (
				SELECT 1
				FROM drs_object_controlled_access ca
				WHERE ca.object_id = o.id AND ca.resource = ANY($1)
			)
		) OR (
			$2
			AND NOT EXISTS (
				SELECT 1
				FROM drs_object_controlled_access ca
				WHERE ca.object_id = o.id
			)
		))
	`
	if startAfter != "" {
		queryArgs = append(queryArgs, startAfter)
		query += fmt.Sprintf(" AND o.id > $%d", len(queryArgs))
	}
	query += " ORDER BY o.id"
	queryArgs = append(queryArgs, limit, offset)
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(queryArgs)-1, len(queryArgs))
	rows, err := db.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanObjectIDs(rows)
}

func (db *PostgresDB) ListObjectIDsByScopeAndResources(ctx context.Context, organization, project string, resources []string, restrictToResources bool) ([]string, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization == "" {
		if !restrictToResources {
			rows, err := db.db.QueryContext(ctx, `SELECT id FROM drs_object ORDER BY id`)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			return scanObjectIDs(rows)
		}
		return db.ListObjectIDsByResources(ctx, resources, false)
	}

	scopeCondition, scopeArgs, err := postgresScopeResourceCondition("ca_scope.resource", organization, project)
	if err != nil {
		return nil, err
	}
	args := make([]any, 0, 3)
	args = append(args, scopeArgs...)
	query := `
		SELECT DISTINCT o.id
		FROM drs_object o
		WHERE EXISTS (
			SELECT 1
			FROM drs_object_controlled_access ca_scope
			WHERE ca_scope.object_id = o.id AND ` + postgresRebindQuestionPlaceholders(scopeCondition, 1) + `
		)`
	if restrictToResources {
		resources = sycommon.NormalizeAccessResources(resources)
		if len(resources) == 0 {
			return []string{}, nil
		}
		args = append(args, pq.Array(resources))
		query += fmt.Sprintf(`
		AND EXISTS (
			SELECT 1
			FROM drs_object_controlled_access ca_auth
			WHERE ca_auth.object_id = o.id AND ca_auth.resource = ANY($%d)
		)`, len(args))
	}
	query += ` ORDER BY o.id`
	rows, err := db.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanObjectIDs(rows)
}

func (db *PostgresDB) ListObjectIDsByChecksumsAndResources(ctx context.Context, checksums []string, resources []string, includeUnscoped, restrictToResources bool) (map[string][]string, error) {
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

	args := make([]any, 0, 4)
	args = append(args, pq.Array(checksums))
	query := `
		WITH matched AS (
			SELECT id AS object_id, id AS match_key
			FROM drs_object
			WHERE id = ANY($1)
			UNION
			SELECT c.object_id, c.checksum AS match_key
			FROM drs_object_checksum c
			WHERE c.checksum = ANY($1)
		)
		SELECT m.match_key, m.object_id
		FROM matched m
		INNER JOIN drs_object o ON o.id = m.object_id`
	if restrictToResources {
		resources = sycommon.NormalizeAccessResources(resources)
		if len(resources) == 0 && !includeUnscoped {
			return map[string][]string{}, nil
		}
		args = append(args, pq.Array(resources), includeUnscoped)
		query += `
		WHERE (
			COALESCE(array_length($2::text[], 1), 0) > 0
			AND EXISTS (
				SELECT 1
				FROM drs_object_controlled_access ca_auth
				WHERE ca_auth.object_id = o.id AND ca_auth.resource = ANY($2)
			)
		) OR (
			$3
			AND NOT EXISTS (
				SELECT 1
				FROM drs_object_controlled_access ca_auth
				WHERE ca_auth.object_id = o.id
			)
		)`
	}
	query += ` ORDER BY m.match_key, m.object_id`
	rows, err := db.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanChecksumMatchRows(rows)
}

func (db *PostgresDB) ListObjectIDsPageByURL(ctx context.Context, objectURL, organization, project, startAfter string, limit, offset int, resources []string, includeUnscoped, restrictToResources bool) ([]string, error) {
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
	conditions := []string{"am.url = $1"}
	if organization != "" {
		scopeCondition, scopeArgs, err := postgresScopeResourceCondition("ca_scope.resource", organization, project)
		if err != nil {
			return nil, err
		}
		args = append(args, scopeArgs...)
		conditions = append(conditions, `EXISTS (
			SELECT 1
			FROM drs_object_controlled_access ca_scope
			WHERE ca_scope.object_id = o.id AND `+postgresRebindQuestionPlaceholders(scopeCondition, len(args)-len(scopeArgs)+1)+`
		)`)
	}
	if restrictToResources {
		resources = sycommon.NormalizeAccessResources(resources)
		if len(resources) == 0 && !includeUnscoped {
			return []string{}, nil
		}
		args = append(args, pq.Array(resources), includeUnscoped)
		resourcesArg := len(args) - 1
		includeUnscopedArg := len(args)
		conditions = append(conditions, fmt.Sprintf(`(
			(
				COALESCE(array_length($%d::text[], 1), 0) > 0
				AND EXISTS (
					SELECT 1
					FROM drs_object_controlled_access ca_auth
					WHERE ca_auth.object_id = o.id AND ca_auth.resource = ANY($%d)
				)
			) OR (
				$%d
				AND NOT EXISTS (
					SELECT 1
					FROM drs_object_controlled_access ca_auth
					WHERE ca_auth.object_id = o.id
				)
			)
		)`, resourcesArg, resourcesArg, includeUnscopedArg))
	}
	if startAfter != "" {
		args = append(args, startAfter)
		conditions = append(conditions, fmt.Sprintf("o.id > $%d", len(args)))
	}

	args = append(args, limit, offset)
	query := `
		SELECT DISTINCT o.id
		FROM drs_object o
		INNER JOIN drs_object_access_method am ON am.object_id = o.id
		WHERE ` + strings.Join(conditions, " AND ") + fmt.Sprintf(`
		ORDER BY o.id
		LIMIT $%d OFFSET $%d`, len(args)-1, len(args))
	rows, err := db.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanObjectIDs(rows)
}

func postgresScopeResourceCondition(column, organization, project string) (string, []any, error) {
	resource, err := sycommon.ResourcePath(organization, project)
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(project) != "" {
		return column + " = ?", []any{resource}, nil
	}
	return "(" + column + " = ? OR " + column + " LIKE ? ESCAPE '\\')", []any{resource, postgresLikeEscape(resource+"/project/") + "%"}, nil
}

func postgresLikeEscape(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

func postgresRebindQuestionPlaceholders(query string, start int) string {
	var b strings.Builder
	next := start
	for _, r := range query {
		if r == '?' {
			b.WriteString(fmt.Sprintf("$%d", next))
			next++
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
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
