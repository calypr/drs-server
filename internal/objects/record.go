// Package objects owns the policies that normalize, identify, authorize, and
// merge generated DRS objects.
package objects

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	drs "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/google/uuid"
)

// Scope identifies the optional organization/project boundary for a record
// query or mutation. An empty Scope represents an unscoped operation.
type Scope struct {
	Organization string
	Project      string
}

type ChecksumQuery struct {
	Type  string
	Value string
}

// RecordListQuery describes the selection and pagination policy for a record
// listing. Only domain values cross into the records service.
type RecordListQuery struct {
	Scope          Scope
	Checksum       *ChecksumQuery
	ObjectURL      string
	StartAfter     string
	Limit          int
	Page           int
	RequiredMethod string
}

// RecordInput carries mutation intent separately from the stored record.
// ExplicitSize distinguishes an omitted update from an explicit zero.
type RecordInput struct {
	Record       drs.DrsObject
	Scope        Scope
	ExplicitSize *int64
}

// NewScope validates and normalizes an organization/project scope. A project
// cannot be supplied without an organization; an omitted scope is valid.
func NewScope(organization, project string) (Scope, error) {
	scope := Scope{
		Organization: strings.TrimSpace(organization),
		Project:      strings.TrimSpace(project),
	}
	if scope.Project != "" && scope.Organization == "" {
		return Scope{}, fmt.Errorf("organization is required when project is set")
	}
	return scope, nil
}

func AccessResources(obj *drs.DrsObject) []string {
	if obj == nil {
		return nil
	}
	if obj.ControlledAccess != nil {
		return clientaccess.NormalizeAccessResources(*obj.ControlledAccess)
	}
	return nil
}

var sha256Like = regexp.MustCompile(`^[A-Fa-f0-9]{64}$`)

func LooksLikeSHA256(v string) bool { return sha256Like.MatchString(strings.TrimSpace(v)) }

func NormalizeOID(oid string) string {
	value := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(oid)), "sha256:")
	if !sha256Like.MatchString(value) {
		return ""
	}
	return value
}

func normalizeChecksum(cs string) string {
	if parts := strings.SplitN(cs, ":", 2); len(parts) == 2 {
		return parts[1]
	}
	return cs
}

func NormalizeChecksumType(checksumType string) string {
	normalized := strings.ToLower(strings.TrimSpace(checksumType))
	return strings.ReplaceAll(normalized, "-", "")
}

func ParseHashQuery(rawHash string, rawType string) (string, string) {
	hashType := NormalizeChecksumType(rawType)
	hashValue := strings.Trim(strings.TrimSpace(normalizeChecksum(rawHash)), `"'`)
	if hashType == "" {
		if parts := strings.SplitN(strings.Trim(strings.TrimSpace(rawHash), `"'`), ":", 2); len(parts) == 2 {
			hashType = NormalizeChecksumType(parts[0])
		}
	}
	return hashType, hashValue
}

func RecordHasChecksumTypeAndValue(obj drs.DrsObject, hashType, hashValue string) bool {
	if hashType == "" {
		return true
	}
	targetType := NormalizeChecksumType(hashType)
	targetValue := strings.Trim(strings.TrimSpace(normalizeChecksum(hashValue)), `"'`)
	if targetType == "" || targetValue == "" {
		return false
	}
	for _, checksum := range obj.Checksums {
		if NormalizeChecksumType(checksum.Type) == targetType && strings.Trim(strings.TrimSpace(normalizeChecksum(checksum.Checksum)), `"'`) == targetValue {
			return true
		}
	}
	return false
}

func mergeAdditionalChecksums(existing, additions []drs.Checksum) []drs.Checksum {
	out := make([]drs.Checksum, 0, len(existing)+len(additions))
	seenTypes := make(map[string]struct{}, len(existing)+len(additions))
	for _, cs := range existing {
		if t := NormalizeChecksumType(cs.Type); t != "" {
			seenTypes[t] = struct{}{}
		}
		out = append(out, cs)
	}
	for _, cs := range additions {
		t := NormalizeChecksumType(cs.Type)
		v := strings.TrimSpace(normalizeChecksum(cs.Checksum))
		if t == "" || v == "" {
			continue
		}
		if _, exists := seenTypes[t]; exists {
			continue
		}
		out = append(out, drs.Checksum{Type: strings.TrimSpace(cs.Type), Checksum: v})
		seenTypes[t] = struct{}{}
	}
	return out
}

func CanonicalSHA256(checksums []drs.Checksum) (string, bool) {
	values := sha256Values(checksums)
	if len(values) == 0 {
		return "", false
	}
	return values[0], true
}

func sha256Values(checksums []drs.Checksum) []string {
	seen := make(map[string]struct{})
	values := make([]string, 0, 1)
	for _, cs := range checksums {
		if NormalizeChecksumType(cs.Type) != "sha256" {
			continue
		}
		normalized := NormalizeOID(cs.Checksum)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		values = append(values, normalized)
	}
	return values
}

func ValidateCanonicalSHA256(checksums []drs.Checksum) (string, bool, error) {
	values := sha256Values(checksums)
	if len(values) > 1 {
		return "", false, fmt.Errorf("%w: %s", errorapi.ErrConflictingSHA256, strings.Join(values, ", "))
	}
	if len(values) == 0 {
		return "", false, nil
	}
	return values[0], true, nil
}

func NormalizeSHA256Query(value string) (string, bool) {
	normalized := NormalizeOID(value)
	if normalized == "" {
		return "", false
	}
	return normalized, true
}

// CleanToBasename extracts a portable basename from either Windows or Unix
// path syntax.
func CleanToBasename(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "\\", "/")
	base := filepath.Base(trimmed)
	if base == "." || base == "/" || base == "" {
		base = trimmed
	}
	return base
}

func NormalizeNameAliases(primary string, aliases []string) []string {
	primary = CleanToBasename(primary)
	seen := make(map[string]struct{}, len(aliases)+1)
	out := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		name := CleanToBasename(alias)
		if name == "" || name == primary {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// AccessMethodID is stable for a type/URL pair and is safe to expose as the
// selector used by the DRS access endpoint.
func AccessMethodID(accessType, accessURL string) string {
	accessType = strings.ToLower(strings.TrimSpace(accessType))
	accessURL = strings.TrimSpace(accessURL)
	digest := sha256.Sum256([]byte(accessType + "\x00" + accessURL))
	return accessType + "-" + hex.EncodeToString(digest[:12])
}

var drsObjectIDNamespace = uuid.NewMD5(uuid.NameSpaceURL, []byte("calypr.org"))

func normalizeSHA256Checksum(raw string) string {
	v := strings.TrimSpace(strings.ToLower(raw))
	return strings.TrimPrefix(v, "sha256:")
}

func canonicalProjectScope(authz []string) (string, error) {
	normalized := clientaccess.NormalizeAccessResources(authz)
	if len(normalized) == 0 {
		return "", fmt.Errorf("%w: project scope is required when object id is not provided", errorapi.ErrInvalidInput)
	}
	projectScopes := make([]string, 0, len(normalized))
	for _, resource := range normalized {
		org, project, ok := clientaccess.ResourceScope(resource)
		if !ok || strings.TrimSpace(org) == "" || strings.TrimSpace(project) == "" {
			continue
		}
		projectScopes = append(projectScopes, resource)
	}
	if len(projectScopes) == 0 {
		return "", fmt.Errorf("%w: project scope is required when object id is not provided", errorapi.ErrInvalidInput)
	}
	if len(projectScopes) > 1 {
		return "", fmt.Errorf("%w: exactly one project scope is required when object id is not provided", errorapi.ErrInvalidInput)
	}
	return projectScopes[0], nil
}

// MintRecordIDFromChecksum returns a deterministic record ID for a checksum
// and one canonical project scope.
func MintRecordIDFromChecksum(checksum string, authz []string) (string, error) {
	checksum = normalizeSHA256Checksum(checksum)
	if checksum == "" {
		return "", fmt.Errorf("%w: sha256 checksum is required when object id is not provided", errorapi.ErrInvalidInput)
	}
	scope, err := canonicalProjectScope(authz)
	if err != nil {
		return "", err
	}
	seed := fmt.Sprintf("sha256:%s|%s", checksum, scope)
	return uuid.NewSHA1(drsObjectIDNamespace, []byte(seed)).String(), nil
}

func MaterializeCandidate(c drs.DrsObjectCandidate, now time.Time) (drs.DrsObject, error) {
	checksums := append([]drs.Checksum(nil), c.Checksums...)
	oid, ok := CanonicalSHA256(checksums)
	if !ok {
		return drs.DrsObject{}, errorapi.ErrNoValidSHA256
	}
	if c.AccessMethods == nil || len(*c.AccessMethods) == 0 {
		return drs.DrsObject{}, errorapi.ErrAccessMethodsRequired
	}
	var controlled []string
	if c.ControlledAccess != nil {
		controlled = clientaccess.NormalizeAccessResources(*c.ControlledAccess)
	}

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
		mintedID, err := MintRecordIDFromChecksum(oid, controlled)
		if err != nil {
			return drs.DrsObject{}, err
		}
		id = mintedID
	}

	obj := drs.DrsObject{
		Id:          id,
		Size:        c.Size,
		Name:        c.Name,
		Description: c.Description,
		Aliases:     c.Aliases,
		Checksums:   []drs.Checksum{{Type: "sha256", Checksum: oid}},
	}
	if c.ControlledAccess != nil {
		obj.ControlledAccess = &controlled
	}
	obj, err := NormalizeRecord(obj, now)
	if err != nil {
		return drs.DrsObject{}, err
	}
	if obj.Name == nil {
		obj.Name = objectStringPtr(oid)
	}
	obj.SelfUri = "drs://" + obj.Id

	methods := make([]drs.AccessMethod, 0, len(*c.AccessMethods))
	for _, method := range *c.AccessMethods {
		if method.AccessId == nil || *method.AccessId == "" {
			method.AccessId = objectStringPtr(string(method.Type))
		}
		methods = append(methods, method)
	}
	obj.AccessMethods = &methods
	if len(methods) == 0 {
		return drs.DrsObject{}, errorapi.ErrAccessMethodsRequired
	}
	return obj, nil
}

// NormalizeRecord applies canonical identity, timestamp, name, checksum, access, and alias values.
func NormalizeRecord(record drs.DrsObject, fallback time.Time) (drs.DrsObject, error) {
	id := strings.TrimSpace(record.Id)
	if id == "" {
		return drs.DrsObject{}, fmt.Errorf("did is required")
	}
	record.Id = id
	if record.Checksums != nil {
		record.Checksums = append([]drs.Checksum(nil), record.Checksums...)
	}

	record = materializeRecordTime(record, fallback.UTC())
	if record.Version == nil {
		record.Version = objectStringPtr("1")
	}

	if record.Name != nil {
		name := CleanToBasename(*record.Name)
		if name == "" {
			record.Name = nil
		} else {
			record.Name = objectStringPtr(name)
		}
	}
	for i, checksum := range record.Checksums {
		if NormalizeChecksumType(checksum.Type) != "sha256" {
			continue
		}
		if normalized, ok := NormalizeSHA256Query(checksum.Checksum); ok {
			record.Checksums[i] = drs.Checksum{Type: "sha256", Checksum: normalized}
		}
	}
	if record.ControlledAccess != nil {
		controlled := clientaccess.NormalizeAccessResources(*record.ControlledAccess)
		record.ControlledAccess = &controlled
	}
	primary := ""
	if record.Name != nil {
		primary = *record.Name
	}
	if record.NameAliases != nil {
		normalized := NormalizeNameAliases(primary, *record.NameAliases)
		record.NameAliases = &normalized
	}
	return record, nil
}

func enforceCanonicalProjectScope(obj drs.DrsObject, organization, project string) (drs.DrsObject, error) {
	scope, err := NewScope(organization, project)
	if err != nil {
		return drs.DrsObject{}, err
	}
	if scope.Organization == "" || scope.Project == "" {
		return obj, nil
	}

	resource, err := clientaccess.ResourcePath(scope.Organization, scope.Project)
	if err != nil {
		return drs.DrsObject{}, err
	}
	controlled := append(AccessResources(&obj), resource)
	controlled = clientaccess.NormalizeAccessResources(controlled)
	obj.ControlledAccess = &controlled
	return obj, nil
}

type RegistrationMergeInput struct {
	ExistingName        string
	ExistingVersion     string
	ExistingDescription string
	ExistingSize        int64
	ExistingUpdated     time.Time
	IncomingName        string
	IncomingVersion     string
	IncomingDescription string
	IncomingSize        int64
	IncomingUpdated     time.Time
	IncomingResources   []string
	CurrentResources    []string
}

// RegistrationMergeResult contains merged metadata and an optional name alias for the persistence write.
// It carries no SQL, context, or authorization state.
type RegistrationMergeResult struct {
	Name        string
	Version     string
	Description string
	Size        int64
	Updated     time.Time
	NameAlias   string
}

// MergeRegistrationMetadata applies registration-specific metadata replacement rules,
// including resource-overlap handling.
func MergeRegistrationMetadata(input RegistrationMergeInput) RegistrationMergeResult {
	allowReplacement := len(input.CurrentResources) == 1 && hasRegistrationResourceOverlap(input.IncomingResources, input.CurrentResources)
	incomingName := CleanToBasename(input.IncomingName)

	result := RegistrationMergeResult{
		Name:        input.ExistingName,
		Version:     input.ExistingVersion,
		Description: input.ExistingDescription,
		Size:        input.ExistingSize,
		Updated:     input.ExistingUpdated,
	}
	if input.ExistingName != "" && incomingName != "" && input.ExistingName != incomingName {
		result.NameAlias = incomingName
		if allowReplacement {
			result.NameAlias = input.ExistingName
		}
	}
	if allowReplacement || strings.TrimSpace(result.Name) == "" {
		if incomingName != "" {
			result.Name = incomingName
		}
	}
	if allowReplacement || strings.TrimSpace(result.Version) == "" {
		if incoming := strings.TrimSpace(input.IncomingVersion); incoming != "" {
			result.Version = incoming
		}
	}
	if allowReplacement || strings.TrimSpace(result.Description) == "" {
		if incoming := strings.TrimSpace(input.IncomingDescription); incoming != "" {
			result.Description = incoming
		}
	}
	if result.Size == 0 && input.IncomingSize != 0 {
		result.Size = input.IncomingSize
	}
	if input.IncomingUpdated.After(result.Updated) {
		result.Updated = input.IncomingUpdated
	}
	return result
}

func hasRegistrationResourceOverlap(left, right []string) bool {
	set := make(map[string]struct{}, len(left))
	for _, resource := range left {
		set[resource] = struct{}{}
	}
	for _, resource := range right {
		if _, ok := set[resource]; ok {
			return true
		}
	}
	return false
}

func objectStringPtr(value string) *string { return &value }
