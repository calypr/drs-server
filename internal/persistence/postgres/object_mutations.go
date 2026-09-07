package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	sycommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/faults"

	"github.com/calypr/syfon/internal/objects"
	"github.com/lib/pq"
)

func (db *PostgresDB) DeleteObject(ctx context.Context, id string) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockContentWriteTx(ctx, tx); err != nil {
		return err
	}

	requestedID := strings.TrimSpace(id)
	if requestedID == "" {
		return fmt.Errorf("%w: object not found", faults.ErrNotFound)
	}
	canonicalID, found, err := postgresObjectIDTx(ctx, tx, requestedID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: object not found", faults.ErrNotFound)
	}
	if err := postgresEnsureNoLegacyDuplicateTx(ctx, tx, canonicalID); err != nil {
		return err
	}
	if err := postgresRequireContentMethodTx(ctx, tx, canonicalID, "delete"); err != nil {
		return err
	}

	result, err := tx.ExecContext(ctx, "DELETE FROM drs_object WHERE id = $1", canonicalID)
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

func (db *PostgresDB) DeleteObjectAlias(ctx context.Context, aliasID string) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockContentWriteTx(ctx, tx); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM drs_object_alias WHERE alias_id = $1", aliasID)
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

func (db *PostgresDB) CreateObjectAlias(ctx context.Context, aliasID, canonicalObjectID string) error {
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
	if err := lockContentWriteTx(ctx, tx); err != nil {
		return err
	}
	var exists string
	err = tx.QueryRowContext(ctx, "SELECT id FROM drs_object WHERE id = $1", canonicalObjectID).Scan(&exists)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: object not found", faults.ErrNotFound)
	}
	if err != nil {
		return err
	}
	if err := postgresEnsureNoLegacyDuplicateTx(ctx, tx, canonicalObjectID); err != nil {
		return err
	}
	if err := postgresRequireContentMethodTx(ctx, tx, canonicalObjectID, "update"); err != nil {
		return err
	}
	var physicalAlias string
	physicalErr := tx.QueryRowContext(ctx, "SELECT id FROM drs_object WHERE id = $1", aliasID).Scan(&physicalAlias)
	if physicalErr == nil {
		return fmt.Errorf("%w: alias %q is already a physical object", faults.ErrConflict, aliasID)
	}
	if physicalErr != sql.ErrNoRows {
		return physicalErr
	}
	var aliasTarget string
	aliasErr := tx.QueryRowContext(ctx, "SELECT object_id FROM drs_object_alias WHERE alias_id = $1", aliasID).Scan(&aliasTarget)
	if aliasErr == nil && aliasTarget != canonicalObjectID {
		return fmt.Errorf("%w: alias %q already points to %q", faults.ErrConflict, aliasID, aliasTarget)
	}
	if aliasErr != nil && aliasErr != sql.ErrNoRows {
		return aliasErr
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO drs_object_alias(alias_id, object_id)
		VALUES ($1, $2)
		ON CONFLICT(alias_id) DO NOTHING
	`, aliasID, canonicalObjectID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (db *PostgresDB) createObjectLegacy(ctx context.Context, obj *objects.Record) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Insert main record
	_, err = tx.ExecContext(ctx, `
		INSERT INTO drs_object (id, size, created_time, updated_time, name, version, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		string(obj.Id), obj.Size, obj.CreatedTime, postgresTimeVal(obj.UpdatedTime), objects.CleanToBasename(postgresStringVal(obj.Name)), postgresStringVal(obj.Version), postgresStringVal(obj.Description),
	)
	if err != nil {
		return fmt.Errorf("failed to insert drs_object: %w", err)
	}

	if err := insertControlledAccessTx(ctx, tx, string(obj.Id), objectAccessResources(obj)); err != nil {
		return err
	}
	for _, alias := range normalizeObjectNameAliases(obj) {
		_, err = tx.ExecContext(ctx, `INSERT INTO drs_object_name_alias (object_id, name_alias) VALUES ($1, $2)`, string(obj.Id), alias)
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
			_, err = tx.ExecContext(ctx, `INSERT INTO drs_object_access_method (object_id, url, type) VALUES ($1, $2, $3)`, string(obj.Id), am.AccessUrl.Url, am.Type)
			if err != nil {
				return fmt.Errorf("failed to insert access method: %w", err)
			}
		}
	}

	// Insert checksums
	for _, cs := range obj.Checksums {
		if strings.TrimSpace(cs.Type) == "" || strings.TrimSpace(cs.Checksum) == "" {
			continue
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO drs_object_checksum (object_id, type, checksum) VALUES ($1, $2, $3)`, string(obj.Id), cs.Type, cs.Checksum)
		if err != nil {
			return fmt.Errorf("failed to insert checksum: %w", err)
		}
	}

	if err := db.flushObjectUsageEventsForIDsTx(ctx, tx, []string{string(obj.Id)}); err != nil {
		return fmt.Errorf("failed to apply object usage events: %w", err)
	}

	return tx.Commit()
}

func (db *PostgresDB) registerObjectsLegacy(ctx context.Context, records []objects.Record) error {
	if len(records) == 0 {
		return nil
	}

	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	ids := make([]string, 0, len(records))
	sizes := make([]int64, 0, len(records))
	createdTimes := make([]time.Time, 0, len(records))
	updatedTimes := make([]time.Time, 0, len(records))
	names := make([]string, 0, len(records))
	versions := make([]string, 0, len(records))
	descriptions := make([]string, 0, len(records))

	accessObjectIDs := make([]string, 0)
	accessURLs := make([]string, 0)
	accessTypes := make([]string, 0)
	controlledObjectIDs := make([]string, 0)
	controlledResources := make([]string, 0)

	checksumObjectIDs := make([]string, 0)
	checksumTypes := make([]string, 0)
	checksumValues := make([]string, 0)
	nameAliasObjectIDs := make([]string, 0)
	nameAliasValues := make([]string, 0)

	for _, obj := range records {
		ids = append(ids, string(obj.Id))
		sizes = append(sizes, obj.Size)
		createdTimes = append(createdTimes, obj.CreatedTime)
		updatedTimes = append(updatedTimes, postgresTimeVal(obj.UpdatedTime))
		names = append(names, objects.CleanToBasename(postgresStringVal(obj.Name)))
		versions = append(versions, postgresStringVal(obj.Version))
		descriptions = append(descriptions, postgresStringVal(obj.Description))

		seenAccess := make(map[string]struct{})
		for _, resource := range objectAccessResources(&obj) {
			controlledObjectIDs = append(controlledObjectIDs, string(obj.Id))
			controlledResources = append(controlledResources, resource)
		}
		for _, alias := range normalizeObjectNameAliases(&obj) {
			nameAliasObjectIDs = append(nameAliasObjectIDs, string(obj.Id))
			nameAliasValues = append(nameAliasValues, alias)
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
				accessObjectIDs = append(accessObjectIDs, string(obj.Id))
				accessURLs = append(accessURLs, am.AccessUrl.Url)
				accessTypes = append(accessTypes, string(am.Type))
			}
		}

		seenChecksum := make(map[string]struct{})
		for _, cs := range obj.Checksums {
			key := cs.Type + "|" + cs.Checksum
			if _, ok := seenChecksum[key]; ok {
				continue
			}
			seenChecksum[key] = struct{}{}
			checksumObjectIDs = append(checksumObjectIDs, string(obj.Id))
			checksumTypes = append(checksumTypes, cs.Type)
			checksumValues = append(checksumValues, cs.Checksum)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO drs_object (id, size, created_time, updated_time, name, version, description)
		SELECT * FROM UNNEST($1::text[], $2::bigint[], $3::timestamp[], $4::timestamp[], $5::text[], $6::text[], $7::text[])
		ON CONFLICT (id) DO UPDATE SET
			size = EXCLUDED.size,
			created_time = EXCLUDED.created_time,
			updated_time = EXCLUDED.updated_time,
			name = EXCLUDED.name,
			version = EXCLUDED.version,
			description = EXCLUDED.description`,
		pq.Array(ids), pq.Array(sizes), pq.Array(createdTimes), pq.Array(updatedTimes),
		pq.Array(names), pq.Array(versions), pq.Array(descriptions),
	); err != nil {
		return fmt.Errorf("failed bulk upsert drs_object: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM drs_object_access_method WHERE object_id = ANY($1)`, pq.Array(ids)); err != nil {
		return fmt.Errorf("failed bulk clear access methods: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM drs_object_controlled_access WHERE object_id = ANY($1)`, pq.Array(ids)); err != nil {
		return fmt.Errorf("failed bulk clear controlled access: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM drs_object_checksum WHERE object_id = ANY($1)`, pq.Array(ids)); err != nil {
		return fmt.Errorf("failed bulk clear checksums: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM drs_object_name_alias WHERE object_id = ANY($1)`, pq.Array(ids)); err != nil {
		return fmt.Errorf("failed bulk clear name aliases: %w", err)
	}

	if len(accessObjectIDs) > 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO drs_object_access_method (object_id, url, type)
			SELECT * FROM UNNEST($1::text[], $2::text[], $3::text[])`,
			pq.Array(accessObjectIDs), pq.Array(accessURLs), pq.Array(accessTypes),
		); err != nil {
			return fmt.Errorf("failed bulk insert access methods: %w", err)
		}
	}
	if len(controlledObjectIDs) > 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO drs_object_controlled_access (object_id, resource)
			SELECT * FROM UNNEST($1::text[], $2::text[])`,
			pq.Array(controlledObjectIDs), pq.Array(controlledResources),
		); err != nil {
			return fmt.Errorf("failed bulk insert controlled access: %w", err)
		}
	}
	if len(nameAliasObjectIDs) > 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO drs_object_name_alias (object_id, name_alias)
			SELECT * FROM UNNEST($1::text[], $2::text[])`,
			pq.Array(nameAliasObjectIDs), pq.Array(nameAliasValues),
		); err != nil {
			return fmt.Errorf("failed bulk insert name aliases: %w", err)
		}
	}

	if len(checksumObjectIDs) > 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO drs_object_checksum (object_id, type, checksum)
			SELECT * FROM UNNEST($1::text[], $2::text[], $3::text[])`,
			pq.Array(checksumObjectIDs), pq.Array(checksumTypes), pq.Array(checksumValues),
		); err != nil {
			return fmt.Errorf("failed bulk insert checksums: %w", err)
		}
	}

	if err := db.flushObjectUsageEventsForIDsTx(ctx, tx, ids); err != nil {
		return fmt.Errorf("failed to apply object usage events: %w", err)
	}

	return tx.Commit()
}

func (db *PostgresDB) BulkDeleteObjects(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockContentWriteTx(ctx, tx); err != nil {
		return err
	}
	canonicalIDs := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, rawID := range ids {
		canonicalID, found, resolveErr := postgresObjectIDTx(ctx, tx, strings.TrimSpace(rawID))
		if resolveErr != nil {
			return resolveErr
		}
		if !found {
			continue
		}
		if strings.TrimSpace(rawID) != canonicalID {
			if err := postgresEnsureNoLegacyDuplicateTx(ctx, tx, canonicalID); err != nil {
				return err
			}
		}
		if err := postgresRequireContentMethodTx(ctx, tx, canonicalID, "delete"); err != nil {
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

	if _, err := tx.ExecContext(ctx, "DELETE FROM drs_object WHERE id = ANY($1)", pq.Array(canonicalIDs)); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *PostgresDB) UpdateObjectAccessMethods(ctx context.Context, objectID string, accessMethods []objects.AccessMethod) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockContentWriteTx(ctx, tx); err != nil {
		return err
	}
	canonicalID, found, err := postgresObjectIDTx(ctx, tx, strings.TrimSpace(objectID))
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: object not found", faults.ErrNotFound)
	}
	if err := postgresRequireContentMethodTx(ctx, tx, canonicalID, "update"); err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, "DELETE FROM drs_object_access_method WHERE object_id = $1", canonicalID)
	if err != nil {
		return err
	}

	for _, am := range accessMethods {
		if am.AccessUrl == nil || am.AccessUrl.Url == "" {
			continue
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO drs_object_access_method (object_id, url, type) VALUES ($1, $2, $3)`, canonicalID, am.AccessUrl.Url, am.Type)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *PostgresDB) RemoveObjectControlledAccess(ctx context.Context, objectID, resource string) error {
	normalized := sycommon.NormalizeAccessResources([]string{resource})
	if len(normalized) == 0 {
		return fmt.Errorf("resource is required")
	}
	resource = normalized[0]
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockContentWriteTx(ctx, tx); err != nil {
		return err
	}
	canonicalID, found, err := postgresObjectIDTx(ctx, tx, strings.TrimSpace(objectID))
	if err != nil {
		return err
	}
	if !found {
		return faults.ErrNotFound
	}
	if err := postgresEnsureNoLegacyDuplicateTx(ctx, tx, canonicalID); err != nil {
		return err
	}
	if !access.HasMethodAccess(ctx, "update", []string{resource}) {
		return faults.ErrUnauthorized
	}

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM drs_object_controlled_access WHERE object_id = $1 AND resource = $2`, canonicalID, resource).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return faults.ErrNotFound
	}
	currentResources, err := postgresResourcesTx(ctx, tx, canonicalID)
	if err != nil {
		return err
	}
	publicRead, err := postgresPublicReadTx(ctx, tx, canonicalID, len(currentResources) == 0)
	if err != nil {
		return err
	}
	if err := postgresSetPublicReadTx(ctx, tx, canonicalID, publicRead); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM drs_object_controlled_access WHERE object_id = $1 AND resource = $2`, canonicalID, resource); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *PostgresDB) RemoveObjectControlledAccessBulk(ctx context.Context, objectIDs []string, resource string) (int, error) {
	if len(objectIDs) == 0 {
		return 0, nil
	}
	normalized := sycommon.NormalizeAccessResources([]string{resource})
	if len(normalized) == 0 {
		return 0, fmt.Errorf("resource is required")
	}
	resource = normalized[0]
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := lockContentWriteTx(ctx, tx); err != nil {
		return 0, err
	}
	orgWide := !strings.Contains(resource, "/project/")
	if !orgWide && !access.HasMethodAccess(ctx, "delete", []string{resource}) {
		return 0, faults.ErrUnauthorized
	}
	seen := make(map[string]struct{}, len(objectIDs))
	removed := 0
	for _, rawID := range objectIDs {
		canonicalID, found, resolveErr := postgresObjectIDTx(ctx, tx, strings.TrimSpace(rawID))
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
		if err := postgresEnsureNoLegacyDuplicateTx(ctx, tx, canonicalID); err != nil {
			return 0, err
		}
		currentResources, err := postgresResourcesTx(ctx, tx, canonicalID)
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
			if _, err := tx.ExecContext(ctx, `DELETE FROM drs_object_controlled_access WHERE object_id = $1 AND resource = $2`, canonicalID, currentResource); err != nil {
				return 0, err
			}
			removed++
			objectRemoved++
		}
		if objectRemoved == 0 {
			continue
		}
		currentResources, err = postgresResourcesTx(ctx, tx, canonicalID)
		if err != nil {
			return 0, err
		}
		publicRead, err := postgresPublicReadTx(ctx, tx, canonicalID, len(currentResources) == 0)
		if err != nil {
			return 0, err
		}
		if err := postgresSetPublicReadTx(ctx, tx, canonicalID, publicRead); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return removed, nil
}

func (db *PostgresDB) BulkUpdateAccessMethods(ctx context.Context, updates map[string][]objects.AccessMethod) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockContentWriteTx(ctx, tx); err != nil {
		return err
	}

	for objectID, methods := range updates {
		canonicalID, found, resolveErr := postgresObjectIDTx(ctx, tx, strings.TrimSpace(objectID))
		if resolveErr != nil {
			return resolveErr
		}
		if !found {
			return faults.ErrNotFound
		}
		if err := postgresRequireContentMethodTx(ctx, tx, canonicalID, "update"); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM drs_object_access_method WHERE object_id = $1", canonicalID)
		if err != nil {
			return err
		}
		for _, am := range methods {
			if am.AccessUrl == nil || am.AccessUrl.Url == "" {
				continue
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO drs_object_access_method (object_id, url, type) VALUES ($1, $2, $3)`, canonicalID, am.AccessUrl.Url, am.Type)
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func insertControlledAccessTx(ctx context.Context, tx *sql.Tx, objectID string, resources []string) error {
	for _, resource := range sycommon.NormalizeAccessResources(resources) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO drs_object_controlled_access (object_id, resource) VALUES ($1, $2)`, objectID, resource); err != nil {
			return fmt.Errorf("failed to insert controlled access: %w", err)
		}
	}
	return nil
}
