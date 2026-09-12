package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"

	"github.com/calypr/syfon/internal/objects"
)

// ReplaceObjects is the checked, atomic boundary for whole-object updates.
// Registration remains additive; replacement is reserved for callers that
// have update authority over every current grant.
func (db *Store) ReplaceObjects(ctx context.Context, objects []drs.DrsObject) error {
	if len(objects) == 0 {
		return nil
	}
	return db.withContentWrite(ctx, func(tx *sql.Tx) error {
		canonicalIDs := make([]string, 0, len(objects))
		seen := make(map[string]struct{}, len(objects))
		for i := range objects {
			canonicalID, err := db.replaceObjectTx(ctx, tx, &objects[i])
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
		return nil
	})
}
func (db *Store) replaceObjectTx(ctx context.Context, tx *sql.Tx, obj *drs.DrsObject) (string, error) {
	id := strings.TrimSpace(obj.Id)
	if id == "" {
		return "", fmt.Errorf("object id is required")
	}
	canonicalID, found, err := db.objectIDTx(ctx, tx, id)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errorapi.ErrObjectNotFound
	}
	if err := db.ensureNoLegacyDuplicateTx(ctx, tx, canonicalID); err != nil {
		return "", err
	}
	currentResources, err := db.resourcesTx(ctx, tx, canonicalID)
	if err != nil {
		return "", err
	}
	if !access.HasMethodAccess(ctx, "update", currentResources) {
		return "", errorapi.ErrAccessDenied
	}
	sha, hasSHA, err := objects.ValidateCanonicalSHA256(obj.Checksums)
	if err != nil {
		return "", err
	}
	storedSHAs, err := db.objectSHAsTx(ctx, tx, canonicalID)
	if err != nil {
		return "", err
	}
	if hasSHA && len(storedSHAs) == 0 {
		owners, err := db.objectIDsBySHATx(ctx, tx, sha)
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
	incomingResources := objects.AccessResources(obj)
	publicRead, err := db.publicReadTx(ctx, tx, canonicalID, len(currentResources) == 0)
	if err != nil {
		return "", err
	}
	if hasNewResource(incomingResources, currentResources) {
		if !publicRead && !canReadContent(ctx, currentResources) {
			return "", errorapi.ErrAccessDenied
		}
		if !canCreateResources(ctx, incomingResources, currentResources) {
			return "", errorapi.ErrAccessDenied
		}
	}
	row, exists, err := db.loadContentRowTx(ctx, tx, canonicalID)
	if err != nil || !exists {
		if err != nil {
			return "", err
		}
		return "", errorapi.ErrObjectNotFound
	}
	if row.size != 0 && obj.Size != 0 && row.size != obj.Size && len(storedSHAs) > 0 {
		return "", identityConflict("SHA %q has conflicting sizes %d and %d", storedSHAs[0], row.size, obj.Size)
	}
	if err := db.replaceMetadataTx(ctx, tx, row, obj); err != nil {
		return "", err
	}
	if err := db.replaceChildrenTx(ctx, tx, canonicalID, obj, sha, hasSHA); err != nil {
		return "", err
	}
	if err := db.replacePolicyTx(ctx, tx, canonicalID, currentResources, obj); err != nil {
		return "", err
	}
	if id != canonicalID {
		if err := db.insertObjectAliasTx(ctx, tx, id, canonicalID); err != nil {
			return "", err
		}
	}
	for _, alias := range identityAliases(obj) {
		if alias == canonicalID {
			continue
		}
		if err := db.insertObjectAliasTx(ctx, tx, alias, canonicalID); err != nil {
			return "", err
		}
	}
	return canonicalID, nil
}

func (db *Store) replaceMetadataTx(ctx context.Context, tx *sql.Tx, row contentRow, obj *drs.DrsObject) error {
	name := objects.CleanToBasename(stringVal(obj.Name))
	if name == "" {
		name = row.name
	}
	if row.name != "" && name != "" && row.name != name {
		if _, err := db.txExecContext(ctx, tx, `
			INSERT INTO drs_object_name_alias (object_id, name_alias) VALUES (?, ?) ON CONFLICT (object_id, name_alias) DO NOTHING`, row.id, row.name); err != nil {
			return fmt.Errorf("preserve replaced object name: %w", err)
		}
	}
	version := strings.TrimSpace(stringVal(obj.Version))
	if version == "" {
		version = row.version
	}
	description := strings.TrimSpace(stringVal(obj.Description))
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
	_, err := db.txExecContext(ctx, tx, `
		UPDATE drs_object SET size = ?, updated_time = ?, name = ?, version = ?, description = ?
		WHERE id = ?`, size, updated, name, version, description, row.id)
	if err != nil {
		return fmt.Errorf("replace object metadata: %w", err)
	}
	return nil
}

func (db *Store) replaceChildrenTx(ctx context.Context, tx *sql.Tx, id string, obj *drs.DrsObject, sha string, hasSHA bool) error {
	if obj.AccessMethods != nil {
		if _, err := db.txExecContext(ctx, tx, `DELETE FROM drs_object_access_method WHERE object_id = ?`, id); err != nil {
			return fmt.Errorf("replace access methods: %w", err)
		}
		if err := db.upsertAccessMethodsTx(ctx, tx, id, *obj.AccessMethods, false); err != nil {
			return fmt.Errorf("replace access method: %w", err)
		}
	}
	if obj.ControlledAccess != nil {
		resources := objects.AccessResources(obj)
		if _, err := db.txExecContext(ctx, tx, `DELETE FROM drs_object_controlled_access WHERE object_id = ?`, id); err != nil {
			return fmt.Errorf("replace controlled access: %w", err)
		}
		for _, resource := range resources {
			if _, err := db.txExecContext(ctx, tx, `INSERT INTO drs_object_controlled_access (object_id, resource) VALUES (?, ?)`, id, resource); err != nil {
				return fmt.Errorf("replace controlled grant: %w", err)
			}
		}
	}
	if obj.Checksums != nil {
		if hasSHA {
			if _, err := db.txExecContext(ctx, tx, `
				UPDATE drs_object_checksum SET checksum = ?
				WHERE object_id = ? AND replace(lower(trim(type)), '-', '') = 'sha256'`, sha, id); err != nil {
				return fmt.Errorf("replace SHA-256 checksum: %w", err)
			}
			if _, err := db.txExecContext(ctx, tx, `
				INSERT INTO drs_object_checksum (object_id, type, checksum)
				SELECT ?, 'sha256', ? WHERE NOT EXISTS (
					SELECT 1 FROM drs_object_checksum WHERE object_id = ?
					AND replace(lower(trim(type)), '-', '') = 'sha256')`, id, sha, id); err != nil {
				return fmt.Errorf("preserve SHA-256 checksum: %w", err)
			}
		}
		if _, err := db.txExecContext(ctx, tx, `
			DELETE FROM drs_object_checksum
			WHERE object_id = ? AND replace(lower(trim(type)), '-', '') <> 'sha256'`, id); err != nil {
			return fmt.Errorf("replace checksums: %w", err)
		}
		for _, checksum := range obj.Checksums {
			typ, value := strings.TrimSpace(checksum.Type), strings.TrimSpace(checksum.Checksum)
			if typ == "" || value == "" || (objects.NormalizeChecksumType(typ) == "sha256" && objects.NormalizeOID(value) != "") {
				continue
			}
			if _, err := db.txExecContext(ctx, tx, `INSERT INTO drs_object_checksum (object_id, type, checksum) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, id, typ, value); err != nil {
				return fmt.Errorf("replace checksum: %w", err)
			}
		}
	}
	for _, alias := range normalizeObjectNameAliases(obj) {
		if _, err := db.txExecContext(ctx, tx, `INSERT INTO drs_object_name_alias (object_id, name_alias) VALUES (?, ?) ON CONFLICT (object_id, name_alias) DO NOTHING`, id, alias); err != nil {
			return fmt.Errorf("replace name alias: %w", err)
		}
	}
	return nil
}

func (db *Store) replacePolicyTx(ctx context.Context, tx *sql.Tx, id string, currentResources []string, obj *drs.DrsObject) error {
	public, err := db.publicReadTx(ctx, tx, id, len(currentResources) == 0)
	if err != nil {
		return err
	}
	return db.setPublicReadTx(ctx, tx, id, public)
}

func (db *Store) DeleteObject(ctx context.Context, id string) error {
	requestedID := strings.TrimSpace(id)
	if requestedID == "" {
		return errorapi.ErrObjectNotFound
	}
	return db.withContentWrite(ctx, func(tx *sql.Tx) error {
		canonicalID, found, err := db.objectIDTx(ctx, tx, requestedID)
		if err != nil {
			return err
		}
		if !found {
			return errorapi.ErrObjectNotFound
		}
		if err := db.ensureNoLegacyDuplicateTx(ctx, tx, canonicalID); err != nil {
			return err
		}
		if err := db.requireContentMethodTx(ctx, tx, canonicalID, "delete"); err != nil {
			return err
		}
		if err := db.deletePendingUsageEventsTx(ctx, tx, []string{canonicalID}); err != nil {
			return err
		}

		result, err := db.txExecContext(ctx, tx, "DELETE FROM drs_object WHERE id = ?", canonicalID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return errorapi.ErrObjectNotFound
		}
		return nil
	})
}

func (db *Store) CreateObjectAlias(ctx context.Context, aliasID, canonicalObjectID string) error {
	aliasID = strings.TrimSpace(aliasID)
	canonicalObjectID = strings.TrimSpace(canonicalObjectID)
	if aliasID == "" || canonicalObjectID == "" {
		return fmt.Errorf("alias_id and canonical object id are required")
	}
	if aliasID == canonicalObjectID {
		return nil
	}

	return db.withContentWrite(ctx, func(tx *sql.Tx) error {
		var exists string
		err := db.txQueryRowContext(ctx, tx, "SELECT id FROM drs_object WHERE id = ?", canonicalObjectID).Scan(&exists)
		if err == sql.ErrNoRows {
			return errorapi.ErrObjectNotFound
		}
		if err != nil {
			return err
		}
		if err := db.ensureNoLegacyDuplicateTx(ctx, tx, canonicalObjectID); err != nil {
			return err
		}
		if err := db.requireContentMethodTx(ctx, tx, canonicalObjectID, "update"); err != nil {
			return err
		}
		var physicalAlias string
		physicalErr := db.txQueryRowContext(ctx, tx, "SELECT id FROM drs_object WHERE id = ?", aliasID).Scan(&physicalAlias)
		if physicalErr == nil {
			return fmt.Errorf("%w: alias %q is already a physical object", errorapi.ErrConflict, aliasID)
		}
		if physicalErr != sql.ErrNoRows {
			return physicalErr
		}

		var aliasTarget string
		aliasErr := db.txQueryRowContext(ctx, tx, "SELECT object_id FROM drs_object_alias WHERE alias_id = ?", aliasID).Scan(&aliasTarget)
		if aliasErr == nil && aliasTarget != canonicalObjectID {
			return fmt.Errorf("%w: alias %q already points to %q", errorapi.ErrConflict, aliasID, aliasTarget)
		}
		if aliasErr != nil && aliasErr != sql.ErrNoRows {
			return aliasErr
		}
		_, err = db.txExecContext(ctx, tx, `
			INSERT INTO drs_object_alias(alias_id, object_id)
			VALUES (?, ?)
			ON CONFLICT(alias_id) DO NOTHING
		`, aliasID, canonicalObjectID)
		if err != nil {
			return err
		}
		return nil
	})
}

func (db *Store) BulkDeleteObjects(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return db.withContentWrite(ctx, func(tx *sql.Tx) error {
		canonicalIDs := make([]string, 0, len(ids))
		seen := make(map[string]struct{}, len(ids))
		for _, rawID := range ids {
			canonicalID, found, resolveErr := db.objectIDTx(ctx, tx, strings.TrimSpace(rawID))
			if resolveErr != nil {
				return resolveErr
			}
			if !found {
				continue
			}
			if strings.TrimSpace(rawID) != canonicalID {
				if err := db.ensureNoLegacyDuplicateTx(ctx, tx, canonicalID); err != nil {
					return err
				}
			}
			if err := db.requireContentMethodTx(ctx, tx, canonicalID, "delete"); err != nil {
				return err
			}
			if _, ok := seen[canonicalID]; ok {
				continue
			}
			seen[canonicalID] = struct{}{}
			canonicalIDs = append(canonicalIDs, canonicalID)
		}
		if len(canonicalIDs) == 0 {
			return nil
		}
		if err := db.deletePendingUsageEventsTx(ctx, tx, canonicalIDs); err != nil {
			return err
		}

		condition, args := db.dialect.ListArgs("id", canonicalIDs)
		query := "DELETE FROM drs_object WHERE " + condition
		if _, err := db.txExecContext(ctx, tx, query, args...); err != nil {
			return err
		}
		return nil
	})
}

func (db *Store) deletePendingUsageEventsTx(ctx context.Context, tx *sql.Tx, objectIDs []string) error {
	condition, args := db.dialect.ListArgs("object_id", objectIDs)
	_, err := db.txExecContext(ctx, tx, "DELETE FROM object_usage_event WHERE "+condition, args...)
	return err
}

func (db *Store) UpdateObjectAccessMethods(ctx context.Context, objectID string, accessMethods []drs.AccessMethod) error {
	return db.withContentWrite(ctx, func(tx *sql.Tx) error {
		canonicalID, found, err := db.objectIDTx(ctx, tx, strings.TrimSpace(objectID))
		if err != nil {
			return err
		}
		if !found {
			return errorapi.ErrObjectNotFound
		}
		if err := db.requireContentMethodTx(ctx, tx, canonicalID, "update"); err != nil {
			return err
		}
		if _, err := db.txExecContext(ctx, tx, "DELETE FROM drs_object_access_method WHERE object_id = ?", canonicalID); err != nil {
			return err
		}
		if err := db.upsertAccessMethodsTx(ctx, tx, canonicalID, accessMethods, false); err != nil {
			return err
		}
		return nil
	})
}

func (db *Store) RemoveObjectControlledAccess(ctx context.Context, objectID, resource string) error {
	normalized := clientaccess.NormalizeAccessResources([]string{resource})
	if len(normalized) == 0 {
		return fmt.Errorf("resource is required")
	}
	resource = normalized[0]
	return db.withContentWrite(ctx, func(tx *sql.Tx) error {
		canonicalID, found, err := db.objectIDTx(ctx, tx, strings.TrimSpace(objectID))
		if err != nil {
			return err
		}
		if !found {
			return errorapi.ErrObjectNotFound
		}
		if err := db.ensureNoLegacyDuplicateTx(ctx, tx, canonicalID); err != nil {
			return err
		}
		if !access.HasMethodAccess(ctx, "update", []string{resource}) {
			return errorapi.ErrAccessDenied
		}
		var exists int
		if err := db.txQueryRowContext(ctx, tx, `SELECT COUNT(1) FROM drs_object_controlled_access WHERE object_id = ? AND resource = ?`, canonicalID, resource).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return errorapi.ErrObjectNotFound
		}
		currentResources, err := db.resourcesTx(ctx, tx, canonicalID)
		if err != nil {
			return err
		}
		publicRead, err := db.publicReadTx(ctx, tx, canonicalID, len(currentResources) == 0)
		if err != nil {
			return err
		}
		if err := db.setPublicReadTx(ctx, tx, canonicalID, publicRead); err != nil {
			return err
		}
		if _, err := db.txExecContext(ctx, tx, `DELETE FROM drs_object_controlled_access WHERE object_id = ? AND resource = ?`, canonicalID, resource); err != nil {
			return err
		}
		return nil
	})
}

func (db *Store) RemoveObjectControlledAccessBulk(ctx context.Context, objectIDs []string, resource string) (int, error) {
	if len(objectIDs) == 0 {
		return 0, nil
	}
	normalized := clientaccess.NormalizeAccessResources([]string{resource})
	if len(normalized) == 0 {
		return 0, fmt.Errorf("resource is required")
	}
	resource = normalized[0]
	orgWide := !strings.Contains(resource, "/project/")
	if !orgWide && !access.HasMethodAccess(ctx, "delete", []string{resource}) {
		return 0, errorapi.ErrAccessDenied
	}
	removed := 0
	err := db.withContentWrite(ctx, func(tx *sql.Tx) error {
		seen := make(map[string]struct{}, len(objectIDs))
		for _, rawID := range objectIDs {
			canonicalID, found, resolveErr := db.objectIDTx(ctx, tx, strings.TrimSpace(rawID))
			if resolveErr != nil {
				return resolveErr
			}
			if !found {
				continue
			}
			if _, ok := seen[canonicalID]; ok {
				continue
			}
			seen[canonicalID] = struct{}{}
			if err := db.ensureNoLegacyDuplicateTx(ctx, tx, canonicalID); err != nil {
				return err
			}
			currentResources, err := db.resourcesTx(ctx, tx, canonicalID)
			if err != nil {
				return err
			}
			objectRemoved := 0
			for _, currentResource := range currentResources {
				if currentResource != resource && (!orgWide || !strings.HasPrefix(currentResource, resource+"/project/")) {
					continue
				}
				if !access.HasMethodAccess(ctx, "delete", []string{currentResource}) {
					continue
				}
				if _, err := db.txExecContext(ctx, tx, `DELETE FROM drs_object_controlled_access WHERE object_id = ? AND resource = ?`, canonicalID, currentResource); err != nil {
					return err
				}
				removed++
				objectRemoved++
			}
			if objectRemoved == 0 {
				continue
			}
			currentResources, err = db.resourcesTx(ctx, tx, canonicalID)
			if err != nil {
				return err
			}
			publicRead, err := db.publicReadTx(ctx, tx, canonicalID, len(currentResources) == 0)
			if err != nil {
				return err
			}
			if err := db.setPublicReadTx(ctx, tx, canonicalID, publicRead); err != nil {
				return err
			}
		}
		return nil
	})
	return removed, err
}

func (db *Store) BulkUpdateAccessMethods(ctx context.Context, updates map[string][]drs.AccessMethod) error {
	return db.withContentWrite(ctx, func(tx *sql.Tx) error {
		for objectID, methods := range updates {
			canonicalID, found, resolveErr := db.objectIDTx(ctx, tx, strings.TrimSpace(objectID))
			if resolveErr != nil {
				return resolveErr
			}
			if !found {
				return errorapi.ErrObjectNotFound
			}
			if err := db.requireContentMethodTx(ctx, tx, canonicalID, "update"); err != nil {
				return err
			}
			if _, err := db.txExecContext(ctx, tx, "DELETE FROM drs_object_access_method WHERE object_id = ?", canonicalID); err != nil {
				return err
			}
			if err := db.upsertAccessMethodsTx(ctx, tx, canonicalID, methods, false); err != nil {
				return err
			}
		}
		return nil
	})
}
