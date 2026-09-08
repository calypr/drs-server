package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/objects"
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

// queryContext and queryRowContext apply the selected SQL dialect to shared statements.
func (db *Store) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.db.QueryContext(ctx, db.dialect.Rebind(query), args...)
}

func (db *Store) queryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.db.QueryRowContext(ctx, db.dialect.Rebind(query), args...)
}

type bulkObjectDialect interface {
	BulkObjectCondition([]string, []string, []string, []string, int) (string, []any)
}

func ptr[T any](value T) *T {
	return &value
}

func stringVal(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func normalizeChecksumLookup(value string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "sha256:")
}

func uniqueObjectsByID(objs []objects.Record) []objects.Record {
	seen := make(map[string]struct{}, len(objs))
	out := make([]objects.Record, 0, len(objs))
	for _, obj := range objs {
		if _, ok := seen[string(obj.Id)]; ok {
			continue
		}
		seen[string(obj.Id)] = struct{}{}
		out = append(out, obj)
	}
	return out
}

func safeSliceCapacity(parts ...int) (int, error) {
	total := int64(0)
	for _, part := range parts {
		if part < 0 {
			return 0, fmt.Errorf("negative capacity component: %d", part)
		}
		total += int64(part)
		if total > int64(math.MaxInt) {
			return 0, fmt.Errorf("capacity too large: %d", total)
		}
	}
	return int(total), nil
}

func (db *Store) publicReadForObject(ctx context.Context, id string, inferred bool) (bool, bool, error) {
	var public bool
	err := db.queryRowContext(ctx, `SELECT public_read FROM drs_object_read_policy WHERE object_id = ?`, id).Scan(&public)
	if err == sql.ErrNoRows {
		return inferred, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return public, true, nil
}

func makePlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

func (db *Store) GetObject(ctx context.Context, id string) (*objects.Record, error) {
	requestID := strings.TrimSpace(id)
	lookupID := requestID
	resolvedAlias := false

retryLookup:
	// 1. Fetch main record
	var r objectRow
	var name, version, description sql.NullString
	err := db.queryRowContext(ctx, `
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
			if aliasErr != nil && !errors.Is(aliasErr, errorapi.ErrNotFound) {
				return nil, aliasErr
			}
		}
		return nil, errorapi.ErrObjectNotFound
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
		UpdatedTime: ptr(r.UpdatedTime),
		Version:     ptr(r.Version),
		Description: ptr(r.Description),
		Name:        ptr(r.Name),
		SelfUri:     "drs://" + objectID,
		NameAliases: nameAliases,
	}

	// 2. Fetch storage access methods.
	urlRows, err := db.queryContext(ctx, "SELECT url, type FROM drs_object_access_method WHERE object_id = ?", lookupID)
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
			obj.AccessMethods = &[]objects.AccessMethod{}
		}
		am := objects.AccessMethod{
			AccessUrl: &objects.AccessURL{Url: u},
			Type:      t,
			AccessId:  ptr(objects.AccessMethodID(t, u)),
		}
		*obj.AccessMethods = append(*obj.AccessMethods, am)
	}
	controlled, err := db.controlledAccessForObject(ctx, lookupID)
	if err != nil {
		return nil, err
	}
	if len(controlled) > 0 {
		obj.ControlledAccess = &controlled
	}
	obj.PublicRead, obj.PublicReadPolicyKnown, err = db.publicReadForObject(ctx, lookupID, len(controlled) == 0)
	if err != nil {
		return nil, err
	}

	// 3. Fetch Checksums
	hashRows, err := db.queryContext(ctx, "SELECT type, checksum FROM drs_object_checksum WHERE object_id = ?", lookupID)
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

func (db *Store) fetchObjectsByIDsOrChecksums(ctx context.Context, ids []string, checksums []string) (map[string]*objects.Record, error) {
	if len(ids) == 0 && len(checksums) == 0 {
		return map[string]*objects.Record{}, nil
	}

	shaQueries := make([]string, 0, len(checksums))
	genericQueries := make([]string, 0, len(checksums))
	for _, checksum := range checksums {
		if normalized, ok := objects.NormalizeSHA256Query(checksum); ok {
			shaQueries = append(shaQueries, normalized)
		} else {
			genericQueries = append(genericQueries, strings.TrimSpace(checksum))
		}
	}
	var condition string
	var args []any
	if d, ok := db.dialect.(bulkObjectDialect); ok {
		condition, args = d.BulkObjectCondition(ids, checksums, shaQueries, genericQueries, 1)
	} else {
		capArgs, capErr := safeSliceCapacity(len(ids), len(checksums), len(shaQueries)+len(genericQueries))
		if capErr != nil {
			return nil, capErr
		}
		args = make([]any, 0, capArgs)
		conditions := make([]string, 0, 2)
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
		condition = strings.Join(conditions, " OR ")
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
		WHERE %s`, condition)

	rows, err := db.queryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch bulk objects: %w", err)
	}
	defer rows.Close()

	objectsByID := make(map[string]*objects.Record)

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
		objectsByID[id] = &objects.Record{
			Id:          objects.RecordID(id),
			Size:        size,
			CreatedTime: createdTime,
			UpdatedTime: ptr(updatedTime),
			Name:        ptr(strings.TrimSpace(name.String)),
			Version:     ptr(version.String),
			Description: ptr(description.String),
			SelfUri:     "drs://" + id,
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

func (db *Store) attachBulkAccessMethods(ctx context.Context, objectsByID map[string]*objects.Record) error {
	ids := sortedObjectIDs(objectsByID)
	condition, args := db.dialect.ListArgs("object_id", ids)
	query := fmt.Sprintf(`
		SELECT object_id, url, type
		FROM drs_object_access_method
		WHERE %s
		ORDER BY object_id`, condition)
	rows, err := db.queryContext(ctx, query, args...)
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
		if obj.AccessMethods == nil {
			obj.AccessMethods = &[]objects.AccessMethod{}
		}
		*obj.AccessMethods = append(*obj.AccessMethods, objects.AccessMethod{
			AccessUrl: &objects.AccessURL{Url: accessURL},
			Type:      accessType,
			AccessId:  ptr(objects.AccessMethodID(accessType, accessURL)),
		})
	}
	return rows.Err()
}

func (db *Store) attachBulkChecksums(ctx context.Context, objectsByID map[string]*objects.Record) error {
	ids := sortedObjectIDs(objectsByID)
	condition, args := db.dialect.ListArgs("object_id", ids)
	query := fmt.Sprintf(`
		SELECT object_id, type, checksum
		FROM drs_object_checksum
		WHERE %s
		ORDER BY object_id`, condition)
	rows, err := db.queryContext(ctx, query, args...)
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
		obj.Checksums = append(obj.Checksums, objects.Checksum{Type: checksumType, Checksum: checksumValue})
	}
	return rows.Err()
}

func objectAccessResources(obj *objects.Record) []string {
	if obj == nil {
		return nil
	}
	if obj.ControlledAccess != nil {
		return clientaccess.NormalizeAccessResources(*obj.ControlledAccess)
	}
	return nil
}

func sortedObjectIDs(objectsByID map[string]*objects.Record) []string {
	ids := make([]string, 0, len(objectsByID))
	for id := range objectsByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (db *Store) controlledAccessForObject(ctx context.Context, objectID string) ([]string, error) {
	rows, err := db.queryContext(ctx, `SELECT resource FROM drs_object_controlled_access WHERE object_id = ? ORDER BY resource`, objectID)
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
	return clientaccess.NormalizeAccessResources(resources), nil
}

func (db *Store) nameAliasesForObject(ctx context.Context, objectID string) ([]string, error) {
	rows, err := db.queryContext(ctx, `SELECT name_alias FROM drs_object_name_alias WHERE object_id = ? ORDER BY name_alias`, objectID)
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
	return objects.NormalizeNameAliases("", aliases), nil
}

func (db *Store) attachControlledAccess(ctx context.Context, objectsByID map[string]*objects.Record) error {
	if len(objectsByID) == 0 {
		return nil
	}
	ids := sortedObjectIDs(objectsByID)
	condition, args := db.dialect.ListArgs("object_id", ids)
	rows, err := db.queryContext(ctx, `
		SELECT object_id, resource
		FROM drs_object_controlled_access
		WHERE `+condition+`
		ORDER BY object_id, resource`, args...)
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
		controlled := clientaccess.NormalizeAccessResources(resources)
		if len(controlled) == 0 {
			continue
		}
		obj.ControlledAccess = &controlled
	}
	return nil
}

func (db *Store) attachPublicRead(ctx context.Context, objectsByID map[string]*objects.Record) error {
	if len(objectsByID) == 0 {
		return nil
	}
	ids := sortedObjectIDs(objectsByID)
	condition, args := db.dialect.ListArgs("object_id", ids)
	rows, err := db.queryContext(ctx, fmt.Sprintf(`
		SELECT object_id, public_read
		FROM drs_object_read_policy
		WHERE %s`, condition), args...)
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

func (db *Store) attachNameAliases(ctx context.Context, objectsByID map[string]*objects.Record) error {
	if len(objectsByID) == 0 {
		return nil
	}
	ids := sortedObjectIDs(objectsByID)
	condition, args := db.dialect.ListArgs("object_id", ids)
	query := fmt.Sprintf(`
		SELECT object_id, name_alias
		FROM drs_object_name_alias
		WHERE %s
		ORDER BY object_id, name_alias`, condition)
	rows, err := db.queryContext(ctx, query, args...)
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
		obj.NameAliases = objects.NormalizeNameAliases(stringVal(obj.Name), aliases)
	}
	return nil
}
