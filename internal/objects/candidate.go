package objects

import (
	"strings"
	"time"

	clientaccess "github.com/calypr/syfon/client/access"
)

// CandidateToRecord converts an HTTP-neutral registration candidate into a
// persisted record. Candidate.Contents is intentionally not copied: the
// historical catalog contract stores object contents separately from the
// registration record.
func CandidateToRecord(c Candidate, now time.Time) (Record, error) {
	checksums := append([]Checksum(nil), candidateChecksums(c.Checksums)...)
	oid, ok := CanonicalSHA256(checksums)
	if !ok {
		return Record{}, ErrNoValidSHA256
	}
	if c.AccessMethods == nil || len(*c.AccessMethods) == 0 {
		return Record{}, ErrAccessMethodsRequired
	}
	authzList := clientaccess.ControlledAccessToAuthzMap(objectStringSliceValue(c.ControlledAccess))

	id := ""
	if c.Aliases != nil {
		for _, alias := range *c.Aliases {
			if strings.HasPrefix(alias, "id:") {
				id = strings.TrimPrefix(alias, "id:")
				break
			}
		}
	}
	if id == "" {
		mintedID, err := MintRecordIDFromChecksum(oid, clientaccess.AuthzMapToList(authzList))
		if err != nil {
			return Record{}, err
		}
		id = string(mintedID)
	}

	obj := Record{
		Id:          RecordID(id),
		Size:        objectInt64Value(c.Size),
		CreatedTime: now,
		UpdatedTime: &now,
		Version:     objectStringPtr("1"),
		MimeType:    c.MimeType,
		Description: c.Description,
		Aliases:     c.Aliases,
		Checksums:   []Checksum{{Type: "sha256", Checksum: oid}},
	}
	if c.ControlledAccess != nil {
		controlled := clientaccess.NormalizeAccessResources(*c.ControlledAccess)
		obj.ControlledAccess = &controlled
	}
	if c.Name != nil {
		name := CleanToBasename(*c.Name)
		if name != "" {
			obj.Name = objectStringPtr(name)
		}
	}
	if obj.Name == nil || strings.TrimSpace(*obj.Name) == "" {
		obj.Name = &oid
	}
	obj.SelfUri = "drs://" + string(obj.Id)

	methods := make([]AccessMethod, 0, len(*c.AccessMethods))
	for _, method := range *c.AccessMethods {
		if method.AccessId == nil || *method.AccessId == "" {
			method.AccessId = objectStringPtr(method.Type)
		}
		methods = append(methods, method)
	}
	obj.AccessMethods = &methods
	if len(methods) == 0 {
		return Record{}, ErrAccessMethodsRequired
	}
	obj.Authorizations = authzList
	return obj, nil
}

func candidateChecksums(value *[]Checksum) []Checksum {
	if value == nil {
		return nil
	}
	return *value
}
