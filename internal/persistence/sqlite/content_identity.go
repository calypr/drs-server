package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	clientaccess "github.com/calypr/syfon/client/access"
	clienthash "github.com/calypr/syfon/client/hash"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/faults"

	"github.com/calypr/syfon/internal/objects"
)

type sqliteContentRow struct {
	id, name, version, description string
	size                           int64
	created, updated               time.Time
}

// RegisterObjects is the content identity write boundary. Every SHA-bearing
// registration is merged while the SQLite writer lock is held, so the parent
// row, children, aliases, and public policy commit together.
func (db *SqliteDB) RegisterObjects(ctx context.Context, objects []objects.Record) error {
	if len(objects) == 0 {
		return nil
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin content registration: %w", err)
	}
	defer tx.Rollback()

	canonicalIDs := make([]string, 0, len(objects))
	seenIDs := make(map[string]struct{}, len(objects))
	for i := range objects {
		canonicalID, err := db.registerContentTx(ctx, tx, &objects[i])
		if err != nil {
			return fmt.Errorf("register object[%d]: %w", i, err)
		}
		if _, seen := seenIDs[canonicalID]; !seen {
			seenIDs[canonicalID] = struct{}{}
			canonicalIDs = append(canonicalIDs, canonicalID)
		}
	}
	if err := db.flushObjectUsageEventsForIDsTx(ctx, tx, canonicalIDs); err != nil {
		return fmt.Errorf("apply object usage events: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit content registration: %w", err)
	}
	return nil
}

func (db *SqliteDB) CreateObject(ctx context.Context, obj *objects.Record) error {
	if obj == nil {
		return fmt.Errorf("object is required")
	}
	return db.RegisterObjects(ctx, []objects.Record{*obj})
}

func (db *SqliteDB) registerContentTx(ctx context.Context, tx *sql.Tx, obj *objects.Record) (string, error) {
	id := strings.TrimSpace(string(obj.Id))
	if id == "" {
		return "", fmt.Errorf("object id is required")
	}
	sha, hasSHA, err := objects.ValidateCanonicalSHA256(obj.Checksums)
	if err != nil {
		return "", err
	}
	canonicalID, foundByID, err := sqliteObjectIDTx(ctx, tx, id)
	if err != nil {
		return "", err
	}
	if hasSHA {
		ids, err := sqliteObjectIDsBySHATx(ctx, tx, sha)
		if err != nil {
			return "", err
		}
		if len(ids) > 1 {
			return "", legacyDuplicateError(sha, ids)
		}
		if len(ids) == 1 {
			if foundByID && canonicalID != ids[0] {
				return "", identityConflict("UUID %q belongs to %q, not SHA %q", id, canonicalID, sha)
			}
			canonicalID = ids[0]
		} else if !foundByID {
			canonicalID = id
		} else {
			stored, err := sqliteObjectSHAsTx(ctx, tx, canonicalID)
			if err != nil {
				return "", err
			}
			for _, existingSHA := range stored {
				if existingSHA != sha {
					return "", identityConflict("UUID %q already identifies SHA %q", id, existingSHA)
				}
			}
		}
	} else if !foundByID {
		canonicalID = id
	}
	if canonicalID == "" {
		return "", identityConflict("empty canonical object id for %q", id)
	}
	if err := checkUUIDClaimTx(ctx, tx, id, canonicalID); err != nil {
		return "", err
	}

	row, exists, err := sqliteLoadContentRowTx(ctx, tx, canonicalID)
	if err != nil {
		return "", err
	}
	wasExisting := exists
	if !exists {
		if err := insertContentRowTx(ctx, tx, canonicalID, obj); err != nil {
			return "", err
		}
		row = sqliteContentRow{id: canonicalID, size: obj.Size, created: obj.CreatedTime, updated: valueTime(obj.UpdatedTime)}
		exists = true
	}
	if hasSHA && row.size != 0 && obj.Size != 0 && row.size != obj.Size {
		return "", identityConflict("SHA %q has conflicting sizes %d and %d", sha, row.size, obj.Size)
	}
	resources := sqliteObjectResources(obj)
	currentResources, err := sqliteResourcesTx(ctx, tx, canonicalID)
	if err != nil {
		return "", err
	}
	inferredPublic := len(resources) == 0
	if wasExisting {
		inferredPublic = len(currentResources) == 0
	}
	publicRead, err := sqlitePublicReadTx(ctx, tx, canonicalID, inferredPublic)
	if err != nil {
		return "", err
	}
	if wasExisting && !publicRead && (sqliteHasNewResource(resources, currentResources) || len(currentResources) == 0 || obj.AccessMethods != nil) && !sqliteCanReadContent(ctx, currentResources) {
		return "", faults.ErrUnauthorized
	}
	if !sqliteCanCreateResources(ctx, resources, currentResources) {
		return "", faults.ErrUnauthorized
	}
	if err := mergeContentRowTx(ctx, tx, row, obj, resources, currentResources); err != nil {
		return "", err
	}
	if err := mergeContentChildrenTx(ctx, tx, canonicalID, sha, hasSHA, resources, obj); err != nil {
		return "", err
	}
	if err := setPublicReadTx(ctx, tx, canonicalID, publicRead); err != nil {
		return "", err
	}
	if id != canonicalID {
		if err := insertObjectAliasTx(ctx, tx, id, canonicalID); err != nil {
			return "", err
		}
	}
	for _, alias := range identityAliases(obj) {
		if alias == canonicalID {
			continue
		}
		if err := insertObjectAliasTx(ctx, tx, alias, canonicalID); err != nil {
			return "", err
		}
	}
	return canonicalID, nil
}

func sqliteObjectIDTx(ctx context.Context, tx *sql.Tx, id string) (string, bool, error) {
	var found string
	err := tx.QueryRowContext(ctx, `SELECT id FROM drs_object WHERE id = ?`, id).Scan(&found)
	if err == nil {
		return found, true, nil
	}
	if err != sql.ErrNoRows {
		return "", false, err
	}
	err = tx.QueryRowContext(ctx, `SELECT object_id FROM drs_object_alias WHERE alias_id = ?`, id).Scan(&found)
	if err == nil {
		return found, true, nil
	}
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return "", false, err
}

func sqliteObjectIDsBySHATx(ctx context.Context, tx *sql.Tx, sha string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT c.object_id
		FROM drs_object_checksum c
		WHERE replace(lower(trim(c.type)), '-', '') = 'sha256'
		  AND replace(lower(trim(c.checksum)), 'sha256:', '') = ?
		ORDER BY c.object_id`, sha)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0, 1)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, strings.TrimSpace(id))
	}
	return ids, rows.Err()
}

func sqliteLoadContentRowTx(ctx context.Context, tx *sql.Tx, id string) (sqliteContentRow, bool, error) {
	var row sqliteContentRow
	err := tx.QueryRowContext(ctx, `
		SELECT id, COALESCE(size, 0), COALESCE(name, ''), COALESCE(version, ''),
		       COALESCE(description, ''), created_time, updated_time
		FROM drs_object WHERE id = ?`, id).Scan(
		&row.id, &row.size, &row.name, &row.version, &row.description, &row.created, &row.updated,
	)
	if err == sql.ErrNoRows {
		return sqliteContentRow{}, false, nil
	}
	if err != nil {
		return sqliteContentRow{}, false, err
	}
	return row, true, nil
}

func insertContentRowTx(ctx context.Context, tx *sql.Tx, id string, obj *objects.Record) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO drs_object (id, size, created_time, updated_time, name, version, description)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, id, obj.Size, obj.CreatedTime, sqliteTimeVal(obj.UpdatedTime),
		objects.CleanToBasename(sqliteStringVal(obj.Name)), sqliteStringVal(obj.Version), sqliteStringVal(obj.Description))
	if err != nil {
		return fmt.Errorf("insert canonical object: %w", err)
	}
	return nil
}

func mergeContentRowTx(ctx context.Context, tx *sql.Tx, row sqliteContentRow, obj *objects.Record, resources, currentResources []string) error {
	merged := objects.MergeRegistrationMetadata(objects.RegistrationMergeInput{
		ExistingName:        row.name,
		ExistingVersion:     row.version,
		ExistingDescription: row.description,
		ExistingSize:        row.size,
		ExistingUpdated:     row.updated,
		IncomingName:        sqliteStringVal(obj.Name),
		IncomingVersion:     sqliteStringVal(obj.Version),
		IncomingDescription: sqliteStringVal(obj.Description),
		IncomingSize:        obj.Size,
		IncomingUpdated:     valueTime(obj.UpdatedTime),
		IncomingResources:   resources,
		CurrentResources:    currentResources,
	})
	if merged.NameAlias != "" {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO drs_object_name_alias (object_id, name_alias) VALUES (?, ?)`, row.id, merged.NameAlias); err != nil {
			return fmt.Errorf("preserve object name alias: %w", err)
		}
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE drs_object
		SET size = ?, updated_time = ?, name = ?, version = ?, description = ?
		WHERE id = ?`, merged.Size, merged.Updated, merged.Name, merged.Version, merged.Description, row.id)
	if err != nil {
		return fmt.Errorf("merge canonical metadata: %w", err)
	}
	return nil
}

func mergeContentChildrenTx(ctx context.Context, tx *sql.Tx, id, sha string, hasSHA bool, resources []string, obj *objects.Record) error {
	for _, resource := range resources {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO drs_object_controlled_access (object_id, resource)
			SELECT ?, ? WHERE NOT EXISTS (
				SELECT 1 FROM drs_object_controlled_access WHERE object_id = ? AND resource = ?
			)`, id, resource, id, resource); err != nil {
			return fmt.Errorf("merge controlled access: %w", err)
		}
	}
	if obj.AccessMethods != nil {
		for _, method := range *obj.AccessMethods {
			if method.AccessUrl == nil || strings.TrimSpace(method.AccessUrl.Url) == "" {
				continue
			}
			typ, rawURL := strings.TrimSpace(string(method.Type)), strings.TrimSpace(method.AccessUrl.Url)
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO drs_object_access_method (object_id, url, type)
				SELECT ?, ?, ? WHERE NOT EXISTS (
					SELECT 1 FROM drs_object_access_method
					WHERE object_id = ? AND lower(trim(type)) = lower(trim(?)) AND url = ?
				)`, id, rawURL, typ, id, typ, rawURL); err != nil {
				return fmt.Errorf("merge access method: %w", err)
			}
		}
	}
	for _, alias := range normalizeObjectNameAliases(obj) {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO drs_object_name_alias (object_id, name_alias) VALUES (?, ?)`, id, alias); err != nil {
			return fmt.Errorf("merge name alias: %w", err)
		}
	}
	if hasSHA {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO drs_object_checksum (object_id, type, checksum)
			SELECT ?, 'sha256', ? WHERE NOT EXISTS (
				SELECT 1 FROM drs_object_checksum
				WHERE object_id = ? AND replace(lower(trim(type)), '-', '') = 'sha256'
				  AND replace(lower(trim(checksum)), 'sha256:', '') = ?
			)`, id, sha, id, sha); err != nil {
			return fmt.Errorf("merge SHA-256 checksum: %w", err)
		}
	}
	for _, checksum := range obj.Checksums {
		typ, value := strings.TrimSpace(checksum.Type), strings.TrimSpace(checksum.Checksum)
		if typ == "" || value == "" || (objects.NormalizeChecksumType(typ) == "sha256" && clienthash.NormalizeOid(value) != "") {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO drs_object_checksum (object_id, type, checksum)
			SELECT ?, ?, ? WHERE NOT EXISTS (
				SELECT 1 FROM drs_object_checksum WHERE object_id = ? AND type = ? AND checksum = ?
			)`, id, typ, value, id, typ, value); err != nil {
			return fmt.Errorf("merge checksum: %w", err)
		}
	}
	return nil
}

func sqliteResourcesTx(ctx context.Context, tx *sql.Tx, id string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT resource FROM drs_object_controlled_access WHERE object_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	resources := make([]string, 0)
	for rows.Next() {
		var resource string
		if err := rows.Scan(&resource); err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}
	return clientaccess.NormalizeAccessResources(resources), rows.Err()
}

func sqlitePublicReadTx(ctx context.Context, tx *sql.Tx, id string, inferred bool) (bool, error) {
	var public bool
	err := tx.QueryRowContext(ctx, `SELECT public_read FROM drs_object_read_policy WHERE object_id = ?`, id).Scan(&public)
	if err == sql.ErrNoRows {
		return inferred, nil
	}
	if err != nil {
		return false, err
	}
	return public, nil
}

func (db *SqliteDB) publicReadForObject(ctx context.Context, id string, inferred bool) (bool, bool, error) {
	var public bool
	err := db.db.QueryRowContext(ctx, `SELECT public_read FROM drs_object_read_policy WHERE object_id = ?`, id).Scan(&public)
	if err == sql.ErrNoRows {
		return inferred, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return public, true, nil
}

func setPublicReadTx(ctx context.Context, tx *sql.Tx, id string, public bool) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO drs_object_read_policy (object_id, public_read) VALUES (?, ?)
		ON CONFLICT(object_id) DO UPDATE SET public_read = excluded.public_read OR drs_object_read_policy.public_read`, id, public)
	if err != nil {
		return fmt.Errorf("persist public-read policy: %w", err)
	}
	return nil
}

func checkUUIDClaimTx(ctx context.Context, tx *sql.Tx, requested, canonical string) error {
	if requested == canonical {
		var aliasTarget string
		err := tx.QueryRowContext(ctx, `SELECT object_id FROM drs_object_alias WHERE alias_id = ?`, requested).Scan(&aliasTarget)
		if err == nil && aliasTarget != canonical {
			return identityConflict("UUID %q is already an alias for %q", requested, aliasTarget)
		}
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		return nil
	}
	var aliasTarget string
	err := tx.QueryRowContext(ctx, `SELECT object_id FROM drs_object_alias WHERE alias_id = ?`, requested).Scan(&aliasTarget)
	if err == nil && aliasTarget != canonical {
		return identityConflict("UUID %q is already an alias for %q", requested, aliasTarget)
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	return nil
}

func insertObjectAliasTx(ctx context.Context, tx *sql.Tx, alias, canonical string) error {
	if alias == canonical {
		return nil
	}
	if err := checkUUIDClaimTx(ctx, tx, alias, canonical); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO drs_object_alias (alias_id, object_id) VALUES (?, ?)`, alias, canonical); err != nil {
		return fmt.Errorf("insert object alias %q: %w", alias, err)
	}
	return nil
}

func sqliteObjectSHAsTx(ctx context.Context, tx *sql.Tx, id string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT replace(lower(trim(checksum)), 'sha256:', '')
		FROM drs_object_checksum
		WHERE object_id = ? AND replace(lower(trim(type)), '-', '') = 'sha256'`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]string, 0, 1)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		if normalized := clienthash.NormalizeOid(value); normalized != "" {
			values = append(values, normalized)
		}
	}
	return values, rows.Err()
}

func sqliteObjectResources(obj *objects.Record) []string {
	if obj == nil {
		return nil
	}
	if obj.ControlledAccess != nil {
		return clientaccess.NormalizeAccessResources(*obj.ControlledAccess)
	}
	return clientaccess.NormalizeAccessResources(clientaccess.AuthzMapToList(obj.Authorizations))
}

func identityAliases(obj *objects.Record) []string {
	if obj == nil || obj.Aliases == nil {
		return nil
	}
	seen := make(map[string]struct{})
	aliases := make([]string, 0)
	for _, raw := range *obj.Aliases {
		if !strings.HasPrefix(raw, "id:") {
			continue
		}
		alias := strings.TrimSpace(strings.TrimPrefix(raw, "id:"))
		if alias == "" {
			continue
		}
		if _, exists := seen[alias]; exists {
			continue
		}
		seen[alias] = struct{}{}
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	return aliases
}

func sqliteCanReadContent(ctx context.Context, resources []string) bool {
	if !access.IsAuthzEnforced(ctx) {
		return true
	}
	if len(resources) == 0 {
		return false
	}
	return access.HasObjectMethodAccess(ctx, "read", resources)
}

func sqliteCanCreateResources(ctx context.Context, resources, current []string) bool {
	currentSet := make(map[string]struct{}, len(current))
	for _, resource := range current {
		currentSet[resource] = struct{}{}
	}
	for _, resource := range resources {
		if _, exists := currentSet[resource]; exists {
			continue
		}
		if !access.HasMethodAccess(ctx, "create", []string{resource}) {
			return false
		}
	}
	return true
}

func sqliteRequireContentMethodTx(ctx context.Context, tx *sql.Tx, id, method string) error {
	resources, err := sqliteResourcesTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if !access.HasMethodAccess(ctx, method, resources) {
		return faults.ErrUnauthorized
	}
	return nil
}

func sqliteEnsureNoLegacyDuplicateTx(ctx context.Context, tx *sql.Tx, id string) error {
	shas, err := sqliteObjectSHAsTx(ctx, tx, id)
	if err != nil {
		return err
	}
	for _, sha := range shas {
		ids, err := sqliteObjectIDsBySHATx(ctx, tx, sha)
		if err != nil {
			return err
		}
		if len(ids) > 1 {
			return legacyDuplicateError(sha, ids)
		}
	}
	return nil
}

func sqliteHasNewResource(resources, current []string) bool {
	set := make(map[string]struct{}, len(current))
	for _, resource := range current {
		set[resource] = struct{}{}
	}
	for _, resource := range resources {
		if _, exists := set[resource]; !exists {
			return true
		}
	}
	return false
}

func valueTime(value *time.Time) time.Time {
	if value == nil || value.IsZero() {
		return time.Time{}
	}
	return value.UTC()
}

func normalizeChecksumLookup(value string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "sha256:")
}

func identityConflict(format string, args ...interface{}) error {
	params := make([]interface{}, 0, len(args)+1)
	params = append(params, faults.ErrConflict)
	params = append(params, args...)
	return fmt.Errorf("%w: "+format, params...)
}

func legacyDuplicateError(sha string, ids []string) error {
	return identityConflict("legacy duplicate physical rows for SHA %q: %s", sha, strings.Join(ids, ", "))
}
