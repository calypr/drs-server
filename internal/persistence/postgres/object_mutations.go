package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	clientaccess "github.com/calypr/syfon/client/access"
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
