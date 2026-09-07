package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/faults"

	"github.com/calypr/syfon/internal/objects"
)

func (db *SqliteDB) DeleteObject(ctx context.Context, id string) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	requestedID := strings.TrimSpace(id)
	if requestedID == "" {
		return fmt.Errorf("%w: object not found", faults.ErrNotFound)
	}
	canonicalID, found, err := sqliteObjectIDTx(ctx, tx, requestedID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: object not found", faults.ErrNotFound)
	}
	if err := sqliteEnsureNoLegacyDuplicateTx(ctx, tx, canonicalID); err != nil {
		return err
	}
	if err := sqliteRequireContentMethodTx(ctx, tx, canonicalID, "delete"); err != nil {
		return err
	}

	result, err := tx.ExecContext(ctx, "DELETE FROM drs_object WHERE id = ?", canonicalID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("%w: object not found", faults.ErrNotFound)
	}
	return tx.Commit()
}

func (db *SqliteDB) DeleteObjectAlias(ctx context.Context, aliasID string) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "DELETE FROM drs_object_alias WHERE alias_id = ?", aliasID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("%w: object not found", faults.ErrNotFound)
	}
	return tx.Commit()
}

func (db *SqliteDB) CreateObjectAlias(ctx context.Context, aliasID, canonicalObjectID string) error {
	aliasID = strings.TrimSpace(aliasID)
	canonicalObjectID = strings.TrimSpace(canonicalObjectID)
	if aliasID == "" || canonicalObjectID == "" {
		return fmt.Errorf("alias_id and canonical object id are required")
	}
	if aliasID == canonicalObjectID {
		return nil
	}

	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists string
	err = tx.QueryRowContext(ctx, "SELECT id FROM drs_object WHERE id = ?", canonicalObjectID).Scan(&exists)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: object not found", faults.ErrNotFound)
	}
	if err != nil {
		return err
	}
	if err := sqliteEnsureNoLegacyDuplicateTx(ctx, tx, canonicalObjectID); err != nil {
		return err
	}
	if err := sqliteRequireContentMethodTx(ctx, tx, canonicalObjectID, "update"); err != nil {
		return err
	}
	var physicalAlias string
	physicalErr := tx.QueryRowContext(ctx, "SELECT id FROM drs_object WHERE id = ?", aliasID).Scan(&physicalAlias)
	if physicalErr == nil {
		return fmt.Errorf("%w: alias %q is already a physical object", faults.ErrConflict, aliasID)
	}
	if physicalErr != sql.ErrNoRows {
		return physicalErr
	}

	var aliasTarget string
	aliasErr := tx.QueryRowContext(ctx, "SELECT object_id FROM drs_object_alias WHERE alias_id = ?", aliasID).Scan(&aliasTarget)
	if aliasErr == nil && aliasTarget != canonicalObjectID {
		return fmt.Errorf("%w: alias %q already points to %q", faults.ErrConflict, aliasID, aliasTarget)
	}
	if aliasErr != nil && aliasErr != sql.ErrNoRows {
		return aliasErr
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO drs_object_alias(alias_id, object_id)
		VALUES (?, ?)
		ON CONFLICT(alias_id) DO NOTHING
	`, aliasID, canonicalObjectID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (db *SqliteDB) createObjectLegacy(ctx context.Context, obj *objects.Record) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Insert main record
	_, err = tx.ExecContext(ctx, `
		INSERT INTO drs_object (id, size, created_time, updated_time, name, version, description)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(obj.Id), obj.Size, obj.CreatedTime, sqliteTimeVal(obj.UpdatedTime), objects.CleanToBasename(sqliteStringVal(obj.Name)), sqliteStringVal(obj.Version), sqliteStringVal(obj.Description),
	)
	if err != nil {
		return fmt.Errorf("failed to insert drs_object: %w", err)
	}

	if err := insertControlledAccessTx(ctx, tx, string(obj.Id), objectAccessResources(obj)); err != nil {
		return err
	}
	for _, alias := range normalizeObjectNameAliases(obj) {
		_, err = tx.ExecContext(ctx, `INSERT INTO drs_object_name_alias (object_id, name_alias) VALUES (?, ?)`, string(obj.Id), alias)
		if err != nil {
			return fmt.Errorf("failed to insert name alias: %w", err)
		}
	}

	// Insert storage access methods.
	if obj.AccessMethods != nil {
		for _, am := range *obj.AccessMethods {
			if am.AccessUrl == nil || am.AccessUrl.Url == "" {
				continue
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO drs_object_access_method (object_id, url, type) VALUES (?, ?, ?)`, string(obj.Id), am.AccessUrl.Url, am.Type)
			if err != nil {
				return fmt.Errorf("failed to insert access method: %w", err)
			}
		}
	}

	// Insert Checksums
	for _, cs := range obj.Checksums {
		if strings.TrimSpace(cs.Type) == "" || strings.TrimSpace(cs.Checksum) == "" {
			continue
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO drs_object_checksum (object_id, type, checksum) VALUES (?, ?, ?)`, string(obj.Id), cs.Type, cs.Checksum)
		if err != nil {
			return fmt.Errorf("failed to insert checksum: %w", err)
		}
	}

	if err := db.flushObjectUsageEventsForIDsTx(ctx, tx, []string{string(obj.Id)}); err != nil {
		return fmt.Errorf("failed to apply object usage events: %w", err)
	}

	return tx.Commit()
}

func (db *SqliteDB) registerObjectsLegacy(ctx context.Context, records []objects.Record) error {
	if len(records) == 0 {
		return nil
	}

	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	ids := make([]string, 0, len(records))
	mainCap, err := safeSliceCapacity(len(records), len(records), len(records), len(records), len(records), len(records), len(records), len(records))
	if err != nil {
		return err
	}
	mainArgs := make([]interface{}, 0, mainCap)

	accessArgs := make([]interface{}, 0)
	controlledArgs := make([]interface{}, 0)
	checksumArgs := make([]interface{}, 0)
	nameAliasArgs := make([]interface{}, 0)

	for _, obj := range records {
		ids = append(ids, string(obj.Id))
		mainArgs = append(mainArgs, string(obj.Id), obj.Size, obj.CreatedTime, sqliteTimeVal(obj.UpdatedTime), objects.CleanToBasename(sqliteStringVal(obj.Name)), sqliteStringVal(obj.Version), sqliteStringVal(obj.Description))

		seenAccess := make(map[string]struct{})
		for _, resource := range objectAccessResources(&obj) {
			controlledArgs = append(controlledArgs, string(obj.Id), resource)
		}
		for _, alias := range normalizeObjectNameAliases(&obj) {
			nameAliasArgs = append(nameAliasArgs, string(obj.Id), alias)
		}
		if obj.AccessMethods != nil {
			for _, am := range *obj.AccessMethods {
				if am.AccessUrl == nil || am.AccessUrl.Url == "" {
					continue
				}
				key := string(am.Type) + "|" + am.AccessUrl.Url
				if _, ok := seenAccess[key]; ok {
					continue
				}
				seenAccess[key] = struct{}{}
				accessArgs = append(accessArgs, string(obj.Id), am.AccessUrl.Url, am.Type)
			}
		}

		seenChecksum := make(map[string]struct{})
		for _, cs := range obj.Checksums {
			key := cs.Type + "|" + cs.Checksum
			if _, ok := seenChecksum[key]; ok {
				continue
			}
			seenChecksum[key] = struct{}{}
			checksumArgs = append(checksumArgs, string(obj.Id), cs.Type, cs.Checksum)
		}
	}

	mainPrefix := `INSERT INTO drs_object (id, size, created_time, updated_time, name, version, description) VALUES `
	mainSuffix := ` ON CONFLICT(id) DO UPDATE SET
		size=excluded.size,
		created_time=excluded.created_time,
		updated_time=excluded.updated_time,
		name=excluded.name,
		version=excluded.version,
		description=excluded.description`
	if err := execSQLiteBulkInsert(tx, mainPrefix, "(?, ?, ?, ?, ?, ?, ?)", 7, mainArgs, mainSuffix); err != nil {
		return fmt.Errorf("failed bulk upsert drs_object: %w", err)
	}

	if err := execSQLiteDeleteByIDs(tx, "drs_object_access_method", ids); err != nil {
		return fmt.Errorf("failed bulk clear access methods: %w", err)
	}
	if err := execSQLiteDeleteByIDs(tx, "drs_object_controlled_access", ids); err != nil {
		return fmt.Errorf("failed bulk clear controlled access: %w", err)
	}
	if err := execSQLiteDeleteByIDs(tx, "drs_object_checksum", ids); err != nil {
		return fmt.Errorf("failed bulk clear checksums: %w", err)
	}
	if err := execSQLiteDeleteByIDs(tx, "drs_object_name_alias", ids); err != nil {
		return fmt.Errorf("failed bulk clear name aliases: %w", err)
	}

	if len(accessArgs) > 0 {
		if err := execSQLiteBulkInsert(
			tx,
			"INSERT INTO drs_object_access_method (object_id, url, type) VALUES ",
			"(?, ?, ?)",
			3,
			accessArgs,
			"",
		); err != nil {
			return fmt.Errorf("failed bulk insert access methods: %w", err)
		}
	}
	if len(controlledArgs) > 0 {
		if err := execSQLiteBulkInsert(
			tx,
			"INSERT INTO drs_object_controlled_access (object_id, resource) VALUES ",
			"(?, ?)",
			2,
			controlledArgs,
			"",
		); err != nil {
			return fmt.Errorf("failed bulk insert controlled access: %w", err)
		}
	}
	if len(nameAliasArgs) > 0 {
		if err := execSQLiteBulkInsert(
			tx,
			"INSERT INTO drs_object_name_alias (object_id, name_alias) VALUES ",
			"(?, ?)",
			2,
			nameAliasArgs,
			"",
		); err != nil {
			return fmt.Errorf("failed bulk insert name aliases: %w", err)
		}
	}
	if len(checksumArgs) > 0 {
		if err := execSQLiteBulkInsert(
			tx,
			"INSERT INTO drs_object_checksum (object_id, type, checksum) VALUES ",
			"(?, ?, ?)",
			3,
			checksumArgs,
			"",
		); err != nil {
			return fmt.Errorf("failed bulk insert checksums: %w", err)
		}
	}
	if err := db.flushObjectUsageEventsForIDsTx(ctx, tx, ids); err != nil {
		return fmt.Errorf("failed to apply object usage events: %w", err)
	}

	return tx.Commit()
}

func (db *SqliteDB) BulkDeleteObjects(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	canonicalIDs := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, rawID := range ids {
		canonicalID, found, resolveErr := sqliteObjectIDTx(ctx, tx, strings.TrimSpace(rawID))
		if resolveErr != nil {
			return resolveErr
		}
		if !found {
			continue
		}
		if strings.TrimSpace(rawID) != canonicalID {
			if err := sqliteEnsureNoLegacyDuplicateTx(ctx, tx, canonicalID); err != nil {
				return err
			}
		}
		if err := sqliteRequireContentMethodTx(ctx, tx, canonicalID, "delete"); err != nil {
			return err
		}
		if _, ok := seen[canonicalID]; ok {
			continue
		}
		seen[canonicalID] = struct{}{}
		canonicalIDs = append(canonicalIDs, canonicalID)
	}
	if len(canonicalIDs) == 0 {
		return tx.Commit()
	}

	placeholders := makePlaceholders(len(canonicalIDs))
	args := make([]interface{}, 0, len(canonicalIDs))
	for _, id := range canonicalIDs {
		args = append(args, id)
	}
	query := fmt.Sprintf("DELETE FROM drs_object WHERE id IN (%s)", placeholders)
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	return tx.Commit()
}

func normalizeObjectNameAliases(obj *objects.Record) []string {
	if obj == nil {
		return nil
	}
	return objects.NormalizeNameAliases(sqliteStringVal(obj.Name), obj.NameAliases)
}

func insertControlledAccessTx(ctx context.Context, tx *sql.Tx, objectID string, resources []string) error {
	for _, resource := range clientaccess.NormalizeAccessResources(resources) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO drs_object_controlled_access (object_id, resource) VALUES (?, ?)`, objectID, resource); err != nil {
			return fmt.Errorf("failed to insert controlled access: %w", err)
		}
	}
	return nil
}

func (db *SqliteDB) UpdateObjectAccessMethods(ctx context.Context, objectID string, accessMethods []objects.AccessMethod) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	canonicalID, found, err := sqliteObjectIDTx(ctx, tx, strings.TrimSpace(objectID))
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: object not found", faults.ErrNotFound)
	}
	if err := sqliteRequireContentMethodTx(ctx, tx, canonicalID, "update"); err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, "DELETE FROM drs_object_access_method WHERE object_id = ?", canonicalID)
	if err != nil {
		return err
	}

	for _, am := range accessMethods {
		if am.AccessUrl == nil || am.AccessUrl.Url == "" {
			continue
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO drs_object_access_method (object_id, url, type) VALUES (?, ?, ?)`, canonicalID, am.AccessUrl.Url, am.Type)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *SqliteDB) RemoveObjectControlledAccess(ctx context.Context, objectID, resource string) error {
	normalized := clientaccess.NormalizeAccessResources([]string{resource})
	if len(normalized) == 0 {
		return fmt.Errorf("resource is required")
	}
	resource = normalized[0]
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	canonicalID, found, err := sqliteObjectIDTx(ctx, tx, strings.TrimSpace(objectID))
	if err != nil {
		return err
	}
	if !found {
		return faults.ErrNotFound
	}
	if err := sqliteEnsureNoLegacyDuplicateTx(ctx, tx, canonicalID); err != nil {
		return err
	}
	if !access.HasMethodAccess(ctx, "update", []string{resource}) {
		return faults.ErrUnauthorized
	}

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM drs_object_controlled_access WHERE object_id = ? AND resource = ?`, canonicalID, resource).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return faults.ErrNotFound
	}
	currentResources, err := sqliteResourcesTx(ctx, tx, canonicalID)
	if err != nil {
		return err
	}
	publicRead, err := sqlitePublicReadTx(ctx, tx, canonicalID, len(currentResources) == 0)
	if err != nil {
		return err
	}
	if err := setPublicReadTx(ctx, tx, canonicalID, publicRead); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM drs_object_controlled_access WHERE object_id = ? AND resource = ?`, canonicalID, resource); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *SqliteDB) RemoveObjectControlledAccessBulk(ctx context.Context, objectIDs []string, resource string) (int, error) {
	if len(objectIDs) == 0 {
		return 0, nil
	}
	normalized := clientaccess.NormalizeAccessResources([]string{resource})
	if len(normalized) == 0 {
		return 0, fmt.Errorf("resource is required")
	}
	resource = normalized[0]
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	orgWide := !strings.Contains(resource, "/project/")
	if !orgWide && !access.HasMethodAccess(ctx, "delete", []string{resource}) {
		return 0, faults.ErrUnauthorized
	}
	seen := make(map[string]struct{}, len(objectIDs))
	removed := 0
	for _, rawID := range objectIDs {
		canonicalID, found, resolveErr := sqliteObjectIDTx(ctx, tx, strings.TrimSpace(rawID))
		if resolveErr != nil {
			return 0, resolveErr
		}
		if !found {
			continue
		}
		if _, ok := seen[canonicalID]; ok {
			continue
		}
		seen[canonicalID] = struct{}{}
		if err := sqliteEnsureNoLegacyDuplicateTx(ctx, tx, canonicalID); err != nil {
			return 0, err
		}
		currentResources, err := sqliteResourcesTx(ctx, tx, canonicalID)
		if err != nil {
			return 0, err
		}
		objectRemoved := 0
		for _, currentResource := range currentResources {
			if currentResource != resource && (!orgWide || !strings.HasPrefix(currentResource, resource+"/project/")) {
				continue
			}
			if !access.HasMethodAccess(ctx, "delete", []string{currentResource}) {
				continue
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM drs_object_controlled_access WHERE object_id = ? AND resource = ?`, canonicalID, currentResource); err != nil {
				return 0, err
			}
			removed++
			objectRemoved++
		}
		if objectRemoved == 0 {
			continue
		}
		currentResources, err = sqliteResourcesTx(ctx, tx, canonicalID)
		if err != nil {
			return 0, err
		}
		publicRead, err := sqlitePublicReadTx(ctx, tx, canonicalID, len(currentResources) == 0)
		if err != nil {
			return 0, err
		}
		if err := setPublicReadTx(ctx, tx, canonicalID, publicRead); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return removed, nil
}

func (db *SqliteDB) BulkUpdateAccessMethods(ctx context.Context, updates map[string][]objects.AccessMethod) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for objectID, methods := range updates {
		canonicalID, found, resolveErr := sqliteObjectIDTx(ctx, tx, strings.TrimSpace(objectID))
		if resolveErr != nil {
			return resolveErr
		}
		if !found {
			return faults.ErrNotFound
		}
		if err := sqliteRequireContentMethodTx(ctx, tx, canonicalID, "update"); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM drs_object_access_method WHERE object_id = ?", canonicalID)
		if err != nil {
			return err
		}
		for _, am := range methods {
			if am.AccessUrl == nil || am.AccessUrl.Url == "" {
				continue
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO drs_object_access_method (object_id, url, type) VALUES (?, ?, ?)`, canonicalID, am.AccessUrl.Url, am.Type)
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
