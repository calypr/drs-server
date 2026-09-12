package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/objects"
)

type storedAccessMethod struct {
	method  drs.AccessMethod
	payload string
	rawURL  string
	rawType string
}

func normalizeAccessMethod(method drs.AccessMethod) (drs.AccessMethod, bool) {
	typ := strings.TrimSpace(string(method.Type))
	rawURL := ""
	if method.AccessUrl != nil {
		rawURL = strings.TrimSpace(method.AccessUrl.Url)
	}
	accessID := ""
	if method.AccessId != nil {
		accessID = strings.TrimSpace(*method.AccessId)
	}
	if typ == "" || (rawURL == "" && accessID == "") {
		return drs.AccessMethod{}, false
	}
	method.Type = drs.AccessMethodType(typ)
	if method.AccessUrl != nil {
		method.AccessUrl = &drs.AccessURL{Url: rawURL, Headers: method.AccessUrl.Headers}
	}
	if method.AccessId != nil {
		method.AccessId = &accessID
	}
	return method, true
}

func accessMethodKey(method drs.AccessMethod) string {
	return "id\x00" + strings.ToLower(effectiveAccessMethodID(method))
}

func effectiveAccessMethodID(method drs.AccessMethod) string {
	if method.AccessId != nil {
		if accessID := strings.TrimSpace(*method.AccessId); accessID != "" {
			return accessID
		}
	}
	return objects.AccessMethodID(string(method.Type), accessMethodURL(method))
}

func accessMethodTarget(method drs.AccessMethod) string {
	return strings.ToLower(strings.TrimSpace(string(method.Type))) + "\x00" + accessMethodURL(method)
}

func accessMethodPayloadWithoutID(method drs.AccessMethod) drs.AccessMethod {
	normalized, ok := normalizeAccessMethod(method)
	if !ok {
		return drs.AccessMethod{}
	}
	normalized.AccessId = nil
	normalized.Type = drs.AccessMethodType(strings.ToLower(strings.TrimSpace(string(normalized.Type))))
	return normalized
}

func accessMethodIdentity(item storedAccessMethod) (string, string, drs.AccessMethod) {
	return effectiveAccessMethodID(item.method), accessMethodTarget(item.method), accessMethodPayloadWithoutID(item.method)
}

func validateAccessMethodIDCollisions(methods []storedAccessMethod) error {
	for i := range methods {
		id, target, payload := accessMethodIdentity(methods[i])
		for j := 0; j < i; j++ {
			previousID, previousTarget, previousPayload := accessMethodIdentity(methods[j])
			if !strings.EqualFold(id, previousID) {
				continue
			}
			if target != previousTarget || (strings.TrimSpace(methods[i].payload) != "" && strings.TrimSpace(methods[j].payload) != "" && !reflect.DeepEqual(payload, previousPayload)) {
				return fmt.Errorf("%w: access ID %q identifies conflicting access methods", errorapi.ErrInvalidInput, id)
			}
		}
	}
	return nil
}

func sameAccessMethodIdentity(left, right storedAccessMethod) bool {
	leftID, leftTarget, leftPayload := accessMethodIdentity(left)
	rightID, rightTarget, rightPayload := accessMethodIdentity(right)
	if !strings.EqualFold(leftID, rightID) || leftTarget != rightTarget {
		return false
	}
	// A legacy row has no payload to compare. It is safe to upgrade it with a
	// richer incoming record as long as its effective ID and target match.
	return strings.TrimSpace(left.payload) == "" || strings.TrimSpace(right.payload) == "" || reflect.DeepEqual(leftPayload, rightPayload)
}

func encodeAccessMethod(method drs.AccessMethod) (storedAccessMethod, bool, error) {
	normalized, ok := normalizeAccessMethod(method)
	if !ok {
		return storedAccessMethod{}, false, nil
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return storedAccessMethod{}, false, fmt.Errorf("encode access method: %w", err)
	}
	return storedAccessMethod{method: normalized, payload: string(payload)}, true, nil
}

func decodeAccessMethod(rawURL, rawType, payload string) (drs.AccessMethod, error) {
	rawURL = strings.TrimSpace(rawURL)
	rawType = strings.TrimSpace(rawType)
	if strings.TrimSpace(payload) != "" {
		var method drs.AccessMethod
		if err := json.Unmarshal([]byte(payload), &method); err != nil {
			return drs.AccessMethod{}, fmt.Errorf("decode access method: %w", err)
		}
		if strings.TrimSpace(string(method.Type)) == "" {
			method.Type = drs.AccessMethodType(rawType)
		}
		if method.AccessUrl != nil {
			method.AccessUrl.Url = rawURL
		} else if rawURL != "" {
			method.AccessUrl = &drs.AccessURL{Url: rawURL}
		}
		if method.AccessId == nil || strings.TrimSpace(*method.AccessId) == "" {
			accessID := objects.AccessMethodID(string(method.Type), rawURL)
			method.AccessId = &accessID
		} else {
			accessID := strings.TrimSpace(*method.AccessId)
			method.AccessId = &accessID
		}
		return method, nil
	}
	accessID := objects.AccessMethodID(rawType, rawURL)
	return drs.AccessMethod{
		AccessId:  &accessID,
		AccessUrl: &drs.AccessURL{Url: rawURL},
		Type:      drs.AccessMethodType(rawType),
	}, nil
}

func (db *Store) loadAccessMethodsTx(ctx context.Context, tx *sql.Tx, objectID string) ([]storedAccessMethod, error) {
	rows, err := db.txQueryContext(ctx, tx, `
		SELECT url, type, access_method_json
		FROM drs_object_access_method
		WHERE object_id = ?`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	methods := make([]storedAccessMethod, 0)
	for rows.Next() {
		var rawURL, rawType string
		var payload sql.NullString
		if err := rows.Scan(&rawURL, &rawType, &payload); err != nil {
			return nil, err
		}
		method, err := decodeAccessMethod(rawURL, rawType, payload.String)
		if err != nil {
			return nil, err
		}
		methods = append(methods, storedAccessMethod{method: method, payload: payload.String, rawURL: rawURL, rawType: rawType})
	}
	return methods, rows.Err()
}

func (db *Store) insertAccessMethodTx(ctx context.Context, tx *sql.Tx, objectID string, stored storedAccessMethod) error {
	if _, err := db.txExecContext(ctx, tx, `
		INSERT INTO drs_object_access_method (object_id, url, type, access_method_json)
		VALUES (?, ?, ?, ?)`, objectID, accessMethodURL(stored.method), string(stored.method.Type), stored.payload); err != nil {
		return err
	}
	return nil
}

func (db *Store) upsertAccessMethodsTx(ctx context.Context, tx *sql.Tx, objectID string, methods []drs.AccessMethod, merge bool) error {
	seen := make([]storedAccessMethod, 0, len(methods))
	if merge {
		stored, err := db.loadAccessMethodsTx(ctx, tx, objectID)
		if err != nil {
			return err
		}
		seen = append(seen, stored...)
	}
	incomingMethods := make([]storedAccessMethod, 0, len(methods))
	for _, method := range methods {
		incoming, valid, err := encodeAccessMethod(method)
		if err != nil {
			return err
		}
		if valid {
			incomingMethods = append(incomingMethods, incoming)
		}
	}
	allMethods := make([]storedAccessMethod, 0, len(seen)+len(incomingMethods))
	allMethods = append(allMethods, seen...)
	allMethods = append(allMethods, incomingMethods...)
	if err := validateAccessMethodIDCollisions(allMethods); err != nil {
		return err
	}
	for _, incoming := range incomingMethods {
		matched := false
		upgradedCoordinates := make(map[struct{ url, typ string }]struct{})
		for i := range seen {
			if !sameAccessMethodIdentity(seen[i], incoming) {
				continue
			}
			matched = true
			existing := seen[i]
			if strings.TrimSpace(existing.payload) == "" {
				coordinates := struct{ url, typ string }{url: existing.rawURL, typ: existing.rawType}
				if _, upgraded := upgradedCoordinates[coordinates]; upgraded {
					seen[i] = incoming
					continue
				}
				result, updateErr := db.txExecContext(ctx, tx, `
					UPDATE drs_object_access_method
					SET access_method_json = ?
					WHERE object_id = ? AND url = ? AND type = ? AND (access_method_json IS NULL OR access_method_json = '')`,
					incoming.payload, objectID, existing.rawURL, existing.rawType)
				if updateErr != nil {
					return updateErr
				}
				affected, rowsErr := result.RowsAffected()
				if rowsErr != nil {
					return rowsErr
				}
				if affected == 0 {
					return fmt.Errorf("upgrade legacy access method: no matching row")
				}
				upgradedCoordinates[coordinates] = struct{}{}
			}
			seen[i] = incoming
		}
		if matched {
			continue
		}
		if err := db.insertAccessMethodTx(ctx, tx, objectID, incoming); err != nil {
			return err
		}
		seen = append(seen, incoming)
	}
	return nil
}

func accessMethodURL(method drs.AccessMethod) string {
	if method.AccessUrl == nil {
		return ""
	}
	return strings.TrimSpace(method.AccessUrl.Url)
}
