package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	sycommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/faults"

	"github.com/calypr/syfon/internal/objects"
)

// ReplaceObjects is the checked, atomic boundary for whole-object updates.
// Registration remains additive; replacement is reserved for callers that
// have update authority over every current grant.
func (db *SqliteDB) ReplaceObjects(ctx context.Context, objects []objects.Record) error {
	if len(objects) == 0 {
		return nil
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin object replacement: %w", err)
	}
	defer tx.Rollback()
	canonicalIDs := make([]string, 0, len(objects))
	seen := make(map[string]struct{}, len(objects))
	for i := range objects {
		canonicalID, err := replaceObjectTx(ctx, tx, &objects[i])
		if err != nil {
			return fmt.Errorf("replace object[%d]: %w", i, err)
		}
		if _, ok := seen[canonicalID]; !ok {
			seen[canonicalID] = struct{}{}
			canonicalIDs = append(canonicalIDs, canonicalID)
		}
	}
	if err := db.flushObjectUsageEventsForIDsTx(ctx, tx, canonicalIDs); err != nil {
		return fmt.Errorf("apply object usage events: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit object replacement: %w", err)
	}
	return nil
}

func replaceObjectTx(ctx context.Context, tx *sql.Tx, obj *objects.Record) (string, error) {
	id := strings.TrimSpace(string(obj.Id))
	if id == "" {
		return "", fmt.Errorf("object id is required")
	}
	canonicalID, found, err := sqliteObjectIDTx(ctx, tx, id)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("%w: object not found", faults.ErrNotFound)
	}
	if err := sqliteEnsureNoLegacyDuplicateTx(ctx, tx, canonicalID); err != nil {
		return "", err
	}
	currentResources, err := sqliteResourcesTx(ctx, tx, canonicalID)
	if err != nil {
		return "", err
	}
	if !access.HasMethodAccess(ctx, "update", currentResources) {
		return "", faults.ErrUnauthorized
	}
	sha, hasSHA, err := objects.ValidateCanonicalSHA256(obj.Checksums)
	if err != nil {
		return "", err
	}
	storedSHAs, err := sqliteObjectSHAsTx(ctx, tx, canonicalID)
	if err != nil {
		return "", err
	}
	if hasSHA && len(storedSHAs) == 0 {
		owners, err := sqliteObjectIDsBySHATx(ctx, tx, sha)
		if err != nil {
			return "", err
		}
		if len(owners) > 1 {
			return "", legacyDuplicateError(sha, owners)
		}
		if len(owners) == 1 && owners[0] != canonicalID {
			return "", identityConflict("SHA %q already belongs to object %q", sha, owners[0])
		}
	}
	if hasSHA {
		for _, storedSHA := range storedSHAs {
			if storedSHA != sha {
				return "", identityConflict("UUID %q already identifies SHA %q", id, storedSHA)
			}
		}
	}
	incomingResources := sqliteObjectResources(obj)
	publicRead, err := sqlitePublicReadTx(ctx, tx, canonicalID, len(currentResources) == 0)
	if err != nil {
		return "", err
	}
	if sqliteHasNewResource(incomingResources, currentResources) {
		if !publicRead && !sqliteCanReadContent(ctx, currentResources) {
			return "", faults.ErrUnauthorized
		}
		if !sqliteCanCreateResources(ctx, incomingResources, currentResources) {
			return "", faults.ErrUnauthorized
		}
	}
	row, exists, err := sqliteLoadContentRowTx(ctx, tx, canonicalID)
	if err != nil || !exists {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("%w: object not found", faults.ErrNotFound)
	}
	if row.size != 0 && obj.Size != 0 && row.size != obj.Size && len(storedSHAs) > 0 {
		return "", identityConflict("SHA %q has conflicting sizes %d and %d", storedSHAs[0], row.size, obj.Size)
	}
	if err := replaceMetadataTx(ctx, tx, row, obj); err != nil {
		return "", err
	}
	if err := replaceChildrenTx(ctx, tx, canonicalID, obj, sha, hasSHA); err != nil {
		return "", err
	}
	if err := replacePolicyTx(ctx, tx, canonicalID, currentResources, obj); err != nil {
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

func replaceMetadataTx(ctx context.Context, tx *sql.Tx, row sqliteContentRow, obj *objects.Record) error {
	name := objects.CleanToBasename(sqliteStringVal(obj.Name))
	if name == "" {
		name = row.name
	}
	if row.name != "" && name != "" && row.name != name {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO drs_object_name_alias (object_id, name_alias) VALUES (?, ?)`, row.id, row.name); err != nil {
			return fmt.Errorf("preserve replaced object name: %w", err)
		}
	}
	version := strings.TrimSpace(sqliteStringVal(obj.Version))
	if version == "" {
		version = row.version
	}
	description := strings.TrimSpace(sqliteStringVal(obj.Description))
	if description == "" {
		description = row.description
	}
	size := row.size
	if obj.Size != 0 {
		size = obj.Size
	}
	updated := row.updated
	if incoming := valueTime(obj.UpdatedTime); incoming.After(updated) {
		updated = incoming
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE drs_object SET size = ?, updated_time = ?, name = ?, version = ?, description = ?
		WHERE id = ?`, size, updated, name, version, description, row.id)
	if err != nil {
		return fmt.Errorf("replace object metadata: %w", err)
	}
	return nil
}

func replaceChildrenTx(ctx context.Context, tx *sql.Tx, id string, obj *objects.Record, sha string, hasSHA bool) error {
	if obj.AccessMethods != nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM drs_object_access_method WHERE object_id = ?`, id); err != nil {
			return fmt.Errorf("replace access methods: %w", err)
		}
		seenMethods := make(map[string]struct{}, len(*obj.AccessMethods))
		for _, method := range *obj.AccessMethods {
			if method.AccessUrl == nil || strings.TrimSpace(method.AccessUrl.Url) == "" {
				continue
			}
			methodKey := strings.ToLower(strings.TrimSpace(string(method.Type))) + "\x00" + strings.TrimSpace(method.AccessUrl.Url)
			if _, ok := seenMethods[methodKey]; ok {
				continue
			}
			seenMethods[methodKey] = struct{}{}
			if _, err := tx.ExecContext(ctx, `
				INSERT OR IGNORE INTO drs_object_access_method (object_id, url, type) VALUES (?, ?, ?)`,
				id, strings.TrimSpace(method.AccessUrl.Url), strings.TrimSpace(string(method.Type))); err != nil {
				return fmt.Errorf("replace access method: %w", err)
			}
		}
	}
	if obj.ControlledAccess != nil || obj.Authorizations != nil {
		resources := sqliteObjectResources(obj)
		if _, err := tx.ExecContext(ctx, `DELETE FROM drs_object_controlled_access WHERE object_id = ?`, id); err != nil {
			return fmt.Errorf("replace controlled access: %w", err)
		}
		for _, resource := range resources {
			if _, err := tx.ExecContext(ctx, `INSERT INTO drs_object_controlled_access (object_id, resource) VALUES (?, ?)`, id, resource); err != nil {
				return fmt.Errorf("replace controlled grant: %w", err)
			}
		}
	}
	if obj.Checksums != nil {
		if hasSHA {
			if _, err := tx.ExecContext(ctx, `
				UPDATE drs_object_checksum SET checksum = ?
				WHERE object_id = ? AND replace(lower(trim(type)), '-', '') = 'sha256'`, sha, id); err != nil {
				return fmt.Errorf("replace SHA-256 checksum: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT OR IGNORE INTO drs_object_checksum (object_id, type, checksum)
				SELECT ?, 'sha256', ? WHERE NOT EXISTS (
					SELECT 1 FROM drs_object_checksum WHERE object_id = ?
					AND replace(lower(trim(type)), '-', '') = 'sha256')`, id, sha, id); err != nil {
				return fmt.Errorf("preserve SHA-256 checksum: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM drs_object_checksum
			WHERE object_id = ? AND replace(lower(trim(type)), '-', '') <> 'sha256'`, id); err != nil {
			return fmt.Errorf("replace checksums: %w", err)
		}
		for _, checksum := range obj.Checksums {
			typ, value := strings.TrimSpace(checksum.Type), strings.TrimSpace(checksum.Checksum)
			if typ == "" || value == "" || (objects.NormalizeChecksumType(typ) == "sha256" && sycommon.NormalizeOid(value) != "") {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO drs_object_checksum (object_id, type, checksum) VALUES (?, ?, ?)`, id, typ, value); err != nil {
				return fmt.Errorf("replace checksum: %w", err)
			}
		}
	}
	for _, alias := range normalizeObjectNameAliases(obj) {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO drs_object_name_alias (object_id, name_alias) VALUES (?, ?)`, id, alias); err != nil {
			return fmt.Errorf("replace name alias: %w", err)
		}
	}
	return nil
}

func replacePolicyTx(ctx context.Context, tx *sql.Tx, id string, currentResources []string, obj *objects.Record) error {
	public, err := sqlitePublicReadTx(ctx, tx, id, len(currentResources) == 0)
	if err != nil {
		return err
	}
	if obj.PublicRead {
		public = true
	}
	return setPublicReadTx(ctx, tx, id, public)
}
