package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
	sycommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/models"
	"github.com/lib/pq"
)

func (db *PostgresDB) fetchObjectsByIDsOrChecksums(ctx context.Context, ids []string, checksums []string) (map[string]*models.InternalObject, error) {
	if len(ids) == 0 && len(checksums) == 0 {
		return map[string]*models.InternalObject{}, nil
	}

	shaQueries := make([]string, 0, len(checksums))
	genericQueries := make([]string, 0, len(checksums))
	for _, checksum := range checksums {
		if normalized, ok := common.NormalizeSHA256Query(checksum); ok {
			shaQueries = append(shaQueries, normalized)
		} else {
			genericQueries = append(genericQueries, strings.TrimSpace(checksum))
		}
	}
	rows, err := db.db.QueryContext(ctx, `
		SELECT
			o.id,
			o.size,
			o.created_time,
			o.updated_time,
			o.name,
			o.version,
			o.description
		FROM drs_object o
		WHERE (
			(COALESCE(array_length($1::text[], 1), 0) > 0 AND o.id = ANY($1))
			OR
			(COALESCE(array_length($2::text[], 1), 0) > 0 AND (
				o.id = ANY($2)
				OR EXISTS (SELECT 1 FROM drs_object_checksum c2
					WHERE c2.object_id = o.id
					  AND replace(lower(trim(c2.type)), '-', '') = 'sha256'
					  AND replace(lower(trim(c2.checksum)), 'sha256:', '') = ANY($3))
				OR EXISTS (SELECT 1 FROM drs_object_checksum c2
					WHERE c2.object_id = o.id AND c2.checksum = ANY($4))
			))
		)`,
		pq.Array(ids), pq.Array(checksums), pq.Array(shaQueries), pq.Array(genericQueries),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch bulk objects: %w", err)
	}
	defer rows.Close()

	objectsByID := make(map[string]*models.InternalObject)

	for rows.Next() {
		var (
			id                       string
			name                     sql.NullString
			version, description     sql.NullString
			size                     int64
			createdTime, updatedTime time.Time
		)
		if err := rows.Scan(
			&id, &size, &createdTime, &updatedTime, &name, &version, &description,
		); err != nil {
			return nil, err
		}
		objectsByID[id] = &models.InternalObject{
			DrsObject: drs.DrsObject{
				Id:          id,
				Size:        size,
				CreatedTime: createdTime,
				UpdatedTime: common.Ptr(updatedTime),
				Name:        common.Ptr(strings.TrimSpace(name.String)),
				Version:     common.Ptr(version.String),
				Description: common.Ptr(description.String),
				SelfUri:     "drs://" + id,
			},
			Properties: map[string]interface{}{},
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(objectsByID) == 0 {
		return objectsByID, nil
	}
	if err := db.attachBulkAccessMethods(ctx, objectsByID); err != nil {
		return nil, err
	}
	if err := db.attachBulkChecksums(ctx, objectsByID); err != nil {
		return nil, err
	}
	if err := db.attachControlledAccess(ctx, objectsByID); err != nil {
		return nil, err
	}
	if err := db.attachPublicRead(ctx, objectsByID); err != nil {
		return nil, err
	}
	if err := db.attachNameAliases(ctx, objectsByID); err != nil {
		return nil, err
	}
	return objectsByID, nil
}

func (db *PostgresDB) attachBulkAccessMethods(ctx context.Context, objectsByID map[string]*models.InternalObject) error {
	rows, err := db.db.QueryContext(ctx, `
		SELECT object_id, url, type
		FROM drs_object_access_method
		WHERE object_id = ANY($1)
		ORDER BY object_id`, pq.Array(sortedObjectIDs(objectsByID)))
	if err != nil {
		return fmt.Errorf("failed to fetch bulk object access methods: %w", err)
	}
	defer rows.Close()

	seenAccess := make(map[string]map[string]struct{}, len(objectsByID))
	for rows.Next() {
		var objectID, accessURL, accessType string
		if err := rows.Scan(&objectID, &accessURL, &accessType); err != nil {
			return err
		}
		obj := objectsByID[objectID]
		if obj == nil {
			continue
		}
		if _, ok := seenAccess[objectID]; !ok {
			seenAccess[objectID] = make(map[string]struct{})
		}
		key := accessType + "|" + accessURL
		if _, exists := seenAccess[objectID][key]; exists {
			continue
		}
		seenAccess[objectID][key] = struct{}{}
		if obj.DrsObject.AccessMethods == nil {
			obj.DrsObject.AccessMethods = &[]drs.AccessMethod{}
		}
		am := drs.AccessMethod{
			AccessUrl: &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{Url: accessURL},
			Type:     drs.AccessMethodType(accessType),
			AccessId: common.Ptr(common.AccessMethodID(accessType, accessURL)),
		}
		*obj.DrsObject.AccessMethods = append(*obj.DrsObject.AccessMethods, am)
	}
	return rows.Err()
}

func (db *PostgresDB) attachBulkChecksums(ctx context.Context, objectsByID map[string]*models.InternalObject) error {
	rows, err := db.db.QueryContext(ctx, `
		SELECT object_id, type, checksum
		FROM drs_object_checksum
		WHERE object_id = ANY($1)
		ORDER BY object_id`, pq.Array(sortedObjectIDs(objectsByID)))
	if err != nil {
		return fmt.Errorf("failed to fetch bulk object checksums: %w", err)
	}
	defer rows.Close()

	seenChecksums := make(map[string]map[string]struct{}, len(objectsByID))
	for rows.Next() {
		var objectID, checksumType, checksumValue string
		if err := rows.Scan(&objectID, &checksumType, &checksumValue); err != nil {
			return err
		}
		obj := objectsByID[objectID]
		if obj == nil {
			continue
		}
		if _, ok := seenChecksums[objectID]; !ok {
			seenChecksums[objectID] = make(map[string]struct{})
		}
		key := checksumType + "|" + checksumValue
		if _, exists := seenChecksums[objectID][key]; exists {
			continue
		}
		seenChecksums[objectID][key] = struct{}{}
		obj.DrsObject.Checksums = append(obj.DrsObject.Checksums, drs.Checksum{Type: checksumType, Checksum: checksumValue})
	}
	return rows.Err()
}

func sortedObjectIDs(objectsByID map[string]*models.InternalObject) []string {
	ids := make([]string, 0, len(objectsByID))
	for id := range objectsByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.TrimSpace(value)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func objectAccessResources(obj *models.InternalObject) []string {
	if obj == nil {
		return nil
	}
	if obj.ControlledAccess != nil {
		return sycommon.NormalizeAccessResources(*obj.ControlledAccess)
	}
	return sycommon.AuthzMapToList(obj.Authorizations)
}

func normalizeObjectNameAliases(obj *models.InternalObject) []string {
	if obj == nil {
		return nil
	}
	return common.NormalizeNameAliases(common.StringVal(obj.Name), obj.NameAliases)
}

func (db *PostgresDB) controlledAccessForObject(ctx context.Context, objectID string) ([]string, error) {
	rows, err := db.db.QueryContext(ctx, `SELECT resource FROM drs_object_controlled_access WHERE object_id = $1 ORDER BY resource`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var resources []string
	for rows.Next() {
		var resource string
		if err := rows.Scan(&resource); err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sycommon.NormalizeAccessResources(resources), nil
}

func (db *PostgresDB) nameAliasesForObject(ctx context.Context, objectID string) ([]string, error) {
	rows, err := db.db.QueryContext(ctx, `SELECT name_alias FROM drs_object_name_alias WHERE object_id = $1 ORDER BY name_alias`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	aliases := make([]string, 0)
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			return nil, err
		}
		aliases = append(aliases, alias)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return common.NormalizeNameAliases("", aliases), nil
}

func (db *PostgresDB) attachControlledAccess(ctx context.Context, objectsByID map[string]*models.InternalObject) error {
	if len(objectsByID) == 0 {
		return nil
	}
	ids := make([]string, 0, len(objectsByID))
	for id := range objectsByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	rows, err := db.db.QueryContext(ctx, `
		SELECT object_id, resource
		FROM drs_object_controlled_access
		WHERE object_id = ANY($1)
		ORDER BY object_id, resource`, pq.Array(ids))
	if err != nil {
		return err
	}
	defer rows.Close()

	byObject := make(map[string][]string, len(objectsByID))
	for rows.Next() {
		var objectID, resource string
		if err := rows.Scan(&objectID, &resource); err != nil {
			return err
		}
		byObject[objectID] = append(byObject[objectID], resource)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for id, resources := range byObject {
		obj, ok := objectsByID[id]
		if !ok {
			continue
		}
		controlled := sycommon.NormalizeAccessResources(resources)
		if len(controlled) == 0 {
			continue
		}
		obj.ControlledAccess = &controlled
		obj.Authorizations = sycommon.ControlledAccessToAuthzMap(controlled)
	}
	return nil
}

func (db *PostgresDB) attachPublicRead(ctx context.Context, objectsByID map[string]*models.InternalObject) error {
	if len(objectsByID) == 0 {
		return nil
	}
	rows, err := db.db.QueryContext(ctx, `
		SELECT object_id, public_read
		FROM drs_object_read_policy
		WHERE object_id = ANY($1)`, pq.Array(sortedObjectIDs(objectsByID)))
	if err != nil {
		return err
	}
	defer rows.Close()
	known := make(map[string]bool, len(objectsByID))
	for rows.Next() {
		var id string
		var public bool
		if err := rows.Scan(&id, &public); err != nil {
			return err
		}
		known[id] = public
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for id, obj := range objectsByID {
		public, ok := known[id]
		if !ok {
			public = len(objectAccessResources(obj)) == 0
		}
		obj.PublicRead = public
		obj.PublicReadPolicyKnown = ok
	}
	return nil
}

func (db *PostgresDB) attachNameAliases(ctx context.Context, objectsByID map[string]*models.InternalObject) error {
	if len(objectsByID) == 0 {
		return nil
	}
	rows, err := db.db.QueryContext(ctx, `
		SELECT object_id, name_alias
		FROM drs_object_name_alias
		WHERE object_id = ANY($1)
		ORDER BY object_id, name_alias`, pq.Array(sortedObjectIDs(objectsByID)))
	if err != nil {
		return fmt.Errorf("failed to fetch bulk object name aliases: %w", err)
	}
	defer rows.Close()

	byObject := make(map[string][]string, len(objectsByID))
	for rows.Next() {
		var objectID, alias string
		if err := rows.Scan(&objectID, &alias); err != nil {
			return err
		}
		byObject[objectID] = append(byObject[objectID], alias)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for objectID, aliases := range byObject {
		obj := objectsByID[objectID]
		if obj == nil {
			continue
		}
		obj.NameAliases = common.NormalizeNameAliases(common.StringVal(obj.Name), aliases)
	}
	return nil
}
