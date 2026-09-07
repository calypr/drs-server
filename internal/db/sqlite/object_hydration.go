package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
	sycommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/models"
)

func (db *SqliteDB) GetObject(ctx context.Context, id string) (*models.InternalObject, error) {
	requestID := strings.TrimSpace(id)
	lookupID := requestID
	resolvedAlias := false

retryLookup:
	// 1. Fetch main record
	var r models.DrsObjectRecord
	var name, version, description sql.NullString
	err := db.db.QueryRowContext(ctx, `
		SELECT id, size, created_time, updated_time, name, version, description
		FROM drs_object WHERE id = ?`, lookupID).Scan(
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

	obj := &models.InternalObject{
		DrsObject: drs.DrsObject{
			Id:          objectID,
			Size:        r.Size,
			CreatedTime: r.CreatedTime,
			UpdatedTime: common.Ptr(r.UpdatedTime),
			Version:     common.Ptr(r.Version),
			Description: common.Ptr(r.Description),
			Name:        common.Ptr(r.Name),
			SelfUri:     "drs://" + objectID,
		},
		NameAliases: nameAliases,
		Properties:  map[string]interface{}{},
	}

	// 2. Fetch storage access methods.
	urlRows, err := db.db.QueryContext(ctx, "SELECT url, type FROM drs_object_access_method WHERE object_id = ?", lookupID)
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
		k := t + "|" + u
		if _, ok := seenAccess[k]; ok {
			continue
		}
		seenAccess[k] = struct{}{}
		if obj.AccessMethods == nil {
			obj.AccessMethods = &[]drs.AccessMethod{}
		}
		am := drs.AccessMethod{
			AccessUrl: &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{Url: u},
			Type:     drs.AccessMethodType(t),
			AccessId: common.Ptr(common.AccessMethodID(t, u)),
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
	hashRows, err := db.db.QueryContext(ctx, "SELECT type, checksum FROM drs_object_checksum WHERE object_id = ?", lookupID)
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
		obj.Checksums = append(obj.Checksums, drs.Checksum{Type: t, Checksum: v})
	}

	return obj, nil
}

func (db *SqliteDB) fetchObjectsByIDsOrChecksums(ctx context.Context, ids []string, checksums []string) (map[string]*models.InternalObject, error) {
	if len(ids) == 0 && len(checksums) == 0 {
		return map[string]*models.InternalObject{}, nil
	}

	conditions := make([]string, 0, 2)
	shaQueries := make([]string, 0, len(checksums))
	genericQueries := make([]string, 0, len(checksums))
	for _, checksum := range checksums {
		if normalized, ok := common.NormalizeSHA256Query(checksum); ok {
			shaQueries = append(shaQueries, normalized)
		} else {
			genericQueries = append(genericQueries, strings.TrimSpace(checksum))
		}
	}
	capArgs, err := safeSliceCapacity(len(ids), len(checksums), len(shaQueries)+len(genericQueries))
	if err != nil {
		return nil, err
	}
	args := make([]interface{}, 0, capArgs)
	if len(ids) > 0 {
		conditions = append(conditions, fmt.Sprintf("o.id IN (%s)", makePlaceholders(len(ids))))
		for _, id := range ids {
			args = append(args, id)
		}
	}
	if len(checksums) > 0 {
		parts := make([]string, 0, 3)
		parts = append(parts, fmt.Sprintf("o.id IN (%s)", makePlaceholders(len(checksums))))
		for _, cs := range checksums {
			args = append(args, strings.TrimSpace(cs))
		}
		if len(shaQueries) > 0 {
			parts = append(parts, fmt.Sprintf(`EXISTS (SELECT 1 FROM drs_object_checksum c2
				WHERE c2.object_id = o.id AND replace(lower(trim(c2.type)), '-', '') = 'sha256'
				AND replace(lower(trim(c2.checksum)), 'sha256:', '') IN (%s))`, makePlaceholders(len(shaQueries))))
			for _, checksum := range shaQueries {
				args = append(args, checksum)
			}
		}
		if len(genericQueries) > 0 {
			parts = append(parts, fmt.Sprintf(`EXISTS (SELECT 1 FROM drs_object_checksum c2
				WHERE c2.object_id = o.id AND c2.checksum IN (%s))`, makePlaceholders(len(genericQueries))))
			for _, checksum := range genericQueries {
				args = append(args, checksum)
			}
		}
		conditions = append(conditions, "("+strings.Join(parts, " OR ")+")")
	}

	query := fmt.Sprintf(`
		SELECT
			o.id,
			o.size,
			o.created_time,
			o.updated_time,
			o.name,
			o.version,
			o.description
		FROM drs_object o
		WHERE %s`, strings.Join(conditions, " OR "))

	rows, err := db.db.QueryContext(ctx, query, args...)
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

func (db *SqliteDB) attachBulkAccessMethods(ctx context.Context, objectsByID map[string]*models.InternalObject) error {
	ids := sortedObjectIDs(objectsByID)
	query := fmt.Sprintf(`
		SELECT object_id, url, type
		FROM drs_object_access_method
		WHERE object_id IN (%s)
		ORDER BY object_id`, makePlaceholders(len(ids)))
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.db.QueryContext(ctx, query, args...)
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
		*obj.DrsObject.AccessMethods = append(*obj.DrsObject.AccessMethods, drs.AccessMethod{
			AccessUrl: &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{Url: accessURL},
			Type:     drs.AccessMethodType(accessType),
			AccessId: common.Ptr(common.AccessMethodID(accessType, accessURL)),
		})
	}
	return rows.Err()
}

func (db *SqliteDB) attachBulkChecksums(ctx context.Context, objectsByID map[string]*models.InternalObject) error {
	ids := sortedObjectIDs(objectsByID)
	query := fmt.Sprintf(`
		SELECT object_id, type, checksum
		FROM drs_object_checksum
		WHERE object_id IN (%s)
		ORDER BY object_id`, makePlaceholders(len(ids)))
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.db.QueryContext(ctx, query, args...)
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

func objectAccessResources(obj *models.InternalObject) []string {
	if obj == nil {
		return nil
	}
	if obj.ControlledAccess != nil {
		return sycommon.NormalizeAccessResources(*obj.ControlledAccess)
	}
	return sycommon.AuthzMapToList(obj.Authorizations)
}

func sortedObjectIDs(objectsByID map[string]*models.InternalObject) []string {
	ids := make([]string, 0, len(objectsByID))
	for id := range objectsByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (db *SqliteDB) controlledAccessForObject(ctx context.Context, objectID string) ([]string, error) {
	rows, err := db.db.QueryContext(ctx, `SELECT resource FROM drs_object_controlled_access WHERE object_id = ? ORDER BY resource`, objectID)
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

func (db *SqliteDB) nameAliasesForObject(ctx context.Context, objectID string) ([]string, error) {
	rows, err := db.db.QueryContext(ctx, `SELECT name_alias FROM drs_object_name_alias WHERE object_id = ? ORDER BY name_alias`, objectID)
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

func (db *SqliteDB) attachControlledAccess(ctx context.Context, objectsByID map[string]*models.InternalObject) error {
	if len(objectsByID) == 0 {
		return nil
	}
	ids := make([]any, 0, len(objectsByID))
	placeholders := make([]string, 0, len(objectsByID))
	for id := range objectsByID {
		ids = append(ids, id)
		placeholders = append(placeholders, "?")
	}
	rows, err := db.db.QueryContext(ctx, `
		SELECT object_id, resource
		FROM drs_object_controlled_access
		WHERE object_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY object_id, resource`, ids...)
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

func (db *SqliteDB) attachPublicRead(ctx context.Context, objectsByID map[string]*models.InternalObject) error {
	if len(objectsByID) == 0 {
		return nil
	}
	ids := sortedObjectIDs(objectsByID)
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT object_id, public_read
		FROM drs_object_read_policy
		WHERE object_id IN (%s)`, makePlaceholders(len(ids))), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	known := make(map[string]bool, len(ids))
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

func (db *SqliteDB) attachNameAliases(ctx context.Context, objectsByID map[string]*models.InternalObject) error {
	if len(objectsByID) == 0 {
		return nil
	}
	ids := sortedObjectIDs(objectsByID)
	query := fmt.Sprintf(`
		SELECT object_id, name_alias
		FROM drs_object_name_alias
		WHERE object_id IN (%s)
		ORDER BY object_id, name_alias`, makePlaceholders(len(ids)))
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.db.QueryContext(ctx, query, args...)
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
