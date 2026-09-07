package core

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/apigen/server/internalapi"
	"github.com/calypr/syfon/apigen/server/lfsapi"
	syfoncommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/models"
)

func EnforceCanonicalProjectScope(obj models.InternalObject, organization, project string) (models.InternalObject, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if project != "" && organization == "" {
		return models.InternalObject{}, fmt.Errorf("organization is required when project is set")
	}
	if organization == "" || project == "" {
		return obj, nil
	}

	resource, err := syfoncommon.ResourcePath(organization, project)
	if err != nil {
		return models.InternalObject{}, err
	}
	controlled := append(ObjectAccessResources(&obj), resource)
	controlled = syfoncommon.NormalizeAccessResources(controlled)
	obj.ControlledAccess = &controlled
	obj.Authorizations = syfoncommon.ControlledAccessToAuthzMap(controlled)
	return obj, nil
}

// LFSCandidateToDRS converts an LFS-specific candidate to a DRS-generic one.
func LFSCandidateToDRS(in lfsapi.DrsObjectCandidate) drs.DrsObjectCandidate {
	aliases := append([]string(nil), common.DerefStringSlice(in.Aliases)...)
	explicitID := strings.TrimSpace(common.DerefString(in.Id))
	var checksums []drs.Checksum
	if in.Checksums != nil {
		checksums = make([]drs.Checksum, len(*in.Checksums))
		for i, c := range *in.Checksums {
			checksums[i] = drs.Checksum{Type: c.Type, Checksum: c.Checksum}
		}
		if explicitID == "" {
			for _, c := range checksums {
				if strings.EqualFold(strings.TrimSpace(c.Type), "sha256") {
					explicitID = syfoncommon.NormalizeOid(c.Checksum)
					break
				}
			}
		}
	}
	if explicitID != "" {
		aliases = append([]string{"id:" + explicitID}, aliases...)
	}

	var ams *[]drs.AccessMethod
	if in.AccessMethods != nil {
		converted := make([]drs.AccessMethod, len(*in.AccessMethods))
		for i, am := range *in.AccessMethods {
			var accessURL *struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    "json:\"url\""
			}
			if am.AccessUrl != nil && am.AccessUrl.Url != nil {
				accessURL = &struct {
					Headers *[]string `json:"headers,omitempty"`
					Url     string    "json:\"url\""
				}{Url: *am.AccessUrl.Url}
			}

			converted[i] = drs.AccessMethod{
				AccessId:  am.AccessId,
				AccessUrl: accessURL,
				Cloud:     am.Region,
			}
			if am.Type != nil {
				converted[i].Type = drs.AccessMethodType(*am.Type)
			}
		}
		ams = &converted
	}

	return drs.DrsObjectCandidate{
		Name:          in.Name,
		Size:          *in.Size,
		MimeType:      in.MimeType,
		Description:   in.Description,
		Aliases:       common.Ptr(aliases),
		Checksums:     checksums,
		AccessMethods: ams,
	}
}

// FirstSupportedAccessURL returns the first URL from an object that Syfon can sign.
func FirstSupportedAccessURL(obj *models.InternalObject) string {
	if obj == nil || obj.AccessMethods == nil {
		return ""
	}
	for _, am := range *obj.AccessMethods {
		if am.AccessUrl == nil || am.AccessUrl.Url == "" {
			continue
		}
		scheme := common.SchemeFromURL(am.AccessUrl.Url)
		if scheme != "" && common.ProviderFromScheme(scheme) == "" {
			continue
		}
		return am.AccessUrl.Url
	}
	return ""
}

// CandidateToInternalObject converts a DRS registration candidate to our internal domain model.
func CandidateToInternalObject(c drs.DrsObjectCandidate, now time.Time) (models.InternalObject, error) {
	oid, ok := common.CanonicalSHA256(c.Checksums)
	if !ok {
		return models.InternalObject{}, common.ErrNoValidSHA256
	}
	if c.AccessMethods == nil || len(*c.AccessMethods) == 0 {
		return models.InternalObject{}, common.ErrAccessMethodsRequired
	}
	authzList := syfoncommon.ControlledAccessToAuthzMap(common.DerefStringSlice(c.ControlledAccess))

	id := ""
	if c.Aliases != nil {
		for _, a := range *c.Aliases {
			if strings.HasPrefix(a, "id:") {
				id = strings.TrimPrefix(a, "id:")
				break
			}
		}
	}

	if id == "" {
		mintedID, mintErr := common.MintObjectIDFromChecksum(oid, syfoncommon.AuthzMapToList(authzList))
		if mintErr != nil {
			return models.InternalObject{}, mintErr
		}
		id = mintedID
	}

	obj := drs.DrsObject{
		Id:          id,
		Size:        c.Size,
		CreatedTime: now,
		UpdatedTime: &now,
		Version:     common.Ptr("1"),
		MimeType:    c.MimeType,
		Description: c.Description,
		Aliases:     c.Aliases,
		Checksums:   []drs.Checksum{{Type: "sha256", Checksum: oid}},
	}
	if c.ControlledAccess != nil {
		controlled := syfoncommon.NormalizeAccessResources(*c.ControlledAccess)
		obj.ControlledAccess = &controlled
	}
	if c.Name != nil {
		obj.Name = normalizedObjectNamePtr(c.Name)
	}
	if obj.Name == nil || strings.TrimSpace(*obj.Name) == "" {
		obj.Name = &oid
	}
	obj.SelfUri = "drs://" + obj.Id

	// Re-construct access methods with clean IDs
	if c.AccessMethods != nil {
		newMethods := make([]drs.AccessMethod, 0, len(*c.AccessMethods))
		for _, am := range *c.AccessMethods {
			method := am
			if method.AccessId == nil || *method.AccessId == "" {
				method.AccessId = common.Ptr(string(method.Type))
			}
			newMethods = append(newMethods, method)
		}
		obj.AccessMethods = &newMethods
	}
	if obj.AccessMethods == nil || len(*obj.AccessMethods) == 0 {
		return models.InternalObject{}, common.ErrAccessMethodsRequired
	}

	return models.InternalObject{
		DrsObject:      obj,
		Authorizations: authzList,
	}, nil
}

// MergeInternalObjectUpdate merges an update into an existing object.
func MergeInternalObjectUpdate(existing models.InternalObject, update models.InternalObject, id string, now time.Time) (models.InternalObject, error) {
	merged := existing
	merged.DrsObject.Id = id
	merged.DrsObject.UpdatedTime = &now
	if update.Properties != nil {
		if merged.Properties == nil {
			merged.Properties = make(map[string]interface{}, len(update.Properties))
		}
		for k, v := range update.Properties {
			merged.Properties[k] = v
		}
	}

	if update.DrsObject.Name != nil {
		merged.DrsObject.Name = normalizedObjectNamePtr(update.DrsObject.Name)
	}
	if update.DrsObject.Description != nil {
		merged.DrsObject.Description = update.DrsObject.Description
	}
	if update.DrsObject.MimeType != nil {
		merged.DrsObject.MimeType = update.DrsObject.MimeType
	}
	if update.DrsObject.Version != nil {
		merged.DrsObject.Version = update.DrsObject.Version
	}
	if update.Aliases != nil {
		merged.Aliases = update.Aliases
	}
	if update.Authorizations != nil {
		merged.Authorizations = update.Authorizations
	}
	if update.ControlledAccess != nil {
		merged.ControlledAccess = update.ControlledAccess
		merged.Authorizations = syfoncommon.ControlledAccessToAuthzMap(*update.ControlledAccess)
	}
	if update.AccessMethods != nil {
		merged.AccessMethods = update.AccessMethods
	}
	if update.Checksums != nil {
		merged.Checksums = common.MergeAdditionalChecksums(existing.Checksums, update.Checksums)
	}

	return merged, nil
}

// InternalRecordToInternalObject converts an index/internal record to our internal domain model.
func InternalRecordToInternalObject(r internalapi.InternalRecord, now time.Time) (models.InternalObject, error) {
	id := strings.TrimSpace(r.Did)
	if id == "" {
		return models.InternalObject{}, fmt.Errorf("did is required")
	}

	obj := drs.DrsObject{
		Id:          id,
		Size:        common.Int64Val(r.Size),
		CreatedTime: parseInternalRecordTime(r.CreatedTime, now),
		Version:     common.Ptr("1"),
		Description: r.Description,
	}
	updatedTime := parseInternalRecordTime(r.UpdatedTime, obj.CreatedTime)
	obj.UpdatedTime = &updatedTime
	if r.Name != nil && strings.TrimSpace(*r.Name) != "" {
		obj.Name = normalizedObjectNamePtr(r.Name)
	}
	objectName := common.StringVal(obj.Name)
	if v := r.Version; v != nil {
		obj.Version = v
	}

	if r.Hashes != nil {
		checksums := make([]drs.Checksum, 0, len(*r.Hashes))
		for k, v := range *r.Hashes {
			if common.NormalizeChecksumType(k) == "sha256" {
				if normalized := syfoncommon.NormalizeOid(v); normalized != "" {
					k = "sha256"
					v = normalized
				}
			}
			checksums = append(checksums, drs.Checksum{Type: k, Checksum: v})
		}
		obj.Checksums = checksums
	}

	var authzMap map[string][]string
	if r.ControlledAccess != nil {
		controlled := syfoncommon.NormalizeAccessResources(*r.ControlledAccess)
		obj.ControlledAccess = &controlled
		authzMap = syfoncommon.ControlledAccessToAuthzMap(controlled)
	}
	if r.AccessMethods != nil {
		methods := append([]drs.AccessMethod(nil), (*r.AccessMethods)...)
		obj.AccessMethods = &methods
	}
	internalObj := models.InternalObject{
		DrsObject:      obj,
		NameAliases:    common.NormalizeNameAliases(objectName, common.DerefStringSlice(r.NameAliases)),
		Authorizations: authzMap,
		Properties:     map[string]interface{}{},
	}
	return EnforceCanonicalProjectScope(internalObj, common.StringVal(r.Organization), common.StringVal(r.Project))
}

func normalizedObjectNamePtr(name *string) *string {
	if name == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*name)
	if trimmed == "" {
		return nil
	}
	trimmed = strings.ReplaceAll(trimmed, "\\", "/")
	base := filepath.Base(trimmed)
	if base == "." || base == "/" || base == "" {
		base = trimmed
	}
	return common.Ptr(base)
}

func parseInternalRecordTime(raw *string, fallback time.Time) time.Time {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return fallback.UTC()
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999", "2006-01-02 15:04:05.999999", "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(*raw)); err == nil {
			return parsed.UTC()
		}
	}
	return fallback.UTC()
}

// InternalObjectToInternalRecord converts our internal domain model back to an API record.
func InternalObjectToInternalRecord(obj models.InternalObject) internalapi.InternalRecord {
	res := internalapi.InternalRecord{
		Did:           obj.Id,
		Size:          &obj.Size,
		CreatedTime:   common.Ptr(obj.CreatedTime.Format(time.RFC3339)),
		Description:   obj.Description,
		Name:          obj.Name,
		NameAliases:   common.Ptr(common.NormalizeNameAliases(common.StringVal(obj.Name), obj.NameAliases)),
		Version:       obj.Version,
		AccessMethods: obj.AccessMethods,
	}
	if controlled := ObjectAccessResources(&obj); len(controlled) > 0 {
		res.ControlledAccess = &controlled
	}
	if obj.UpdatedTime != nil {
		res.UpdatedTime = common.Ptr(obj.UpdatedTime.Format(time.RFC3339))
	}
	if len(obj.Checksums) > 0 {
		h := make(internalapi.HashInfo)
		for _, c := range obj.Checksums {
			h[c.Type] = c.Checksum
		}
		res.Hashes = &h
	}
	return res
}

// InternalObjectToInternalRecordResponse converts our internal domain model back to an API response.
func InternalObjectToInternalRecordResponse(obj models.InternalObject) internalapi.InternalRecordResponse {
	rec := InternalObjectToInternalRecord(obj)
	return internalapi.InternalRecordResponse{
		Did:              rec.Did,
		AccessMethods:    rec.AccessMethods,
		ControlledAccess: rec.ControlledAccess,
		Size:             rec.Size,
		CreatedTime:      rec.CreatedTime,
		Description:      rec.Description,
		Name:             rec.Name,
		NameAliases:      rec.NameAliases,
		Version:          rec.Version,
		UpdatedTime:      rec.UpdatedTime,
		Hashes:           rec.Hashes,
		Organization:     rec.Organization,
		Project:          rec.Project,
	}
}
