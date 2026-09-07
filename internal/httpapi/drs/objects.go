// Package drs translates object-domain values to and from the generated DRS
// contract at the HTTP boundary.
package drs

import (
	"encoding/json"

	generated "github.com/calypr/syfon/apigen/server/drs"
	syfoncommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/objects"
)

// FromGeneratedCandidate translates the DRS registration request into an object candidate.
func FromGeneratedCandidate(value generated.DrsObjectCandidate) objects.Candidate {
	out := objects.Candidate{
		Aliases:          value.Aliases,
		Description:      value.Description,
		MimeType:         value.MimeType,
		Name:             value.Name,
		ControlledAccess: value.ControlledAccess,
		Size:             int64Ptr(value.Size),
	}
	if value.Checksums != nil {
		checksums := make([]objects.Checksum, 0, len(value.Checksums))
		for _, checksum := range value.Checksums {
			checksums = append(checksums, objects.Checksum{Type: checksum.Type, Checksum: checksum.Checksum})
		}
		out.Checksums = &checksums
	}
	if value.AccessMethods != nil {
		methods := make([]objects.AccessMethod, 0, len(*value.AccessMethods))
		for _, method := range *value.AccessMethods {
			methods = append(methods, fromGeneratedAccessMethod(method))
		}
		out.AccessMethods = &methods
	}
	if value.Contents != nil {
		contents := make([]objects.Content, 0, len(*value.Contents))
		for _, content := range *value.Contents {
			contents = append(contents, fromGeneratedContent(content))
		}
		out.Contents = &contents
	}
	return out
}

func int64Ptr(value int64) *int64 { return &value }

func ToGenerated(record objects.Record) generated.DrsObject {
	out := generated.DrsObject{
		Id:               string(record.Id),
		ControlledAccess: record.ControlledAccess,
		CreatedTime:      record.CreatedTime,
		Description:      record.Description,
		MimeType:         record.MimeType,
		Name:             record.Name,
		SelfUri:          record.SelfUri,
		Size:             record.Size,
		UpdatedTime:      record.UpdatedTime,
		Version:          record.Version,
	}
	if record.Checksums != nil {
		out.Checksums = make([]generated.Checksum, 0, len(record.Checksums))
	}
	if out.ControlledAccess == nil && len(record.Authorizations) > 0 {
		controlled := syfoncommon.AuthzMapToControlledAccess(record.Authorizations)
		out.ControlledAccess = &controlled
	}
	for _, checksum := range record.Checksums {
		out.Checksums = append(out.Checksums, generated.Checksum{Type: checksum.Type, Checksum: checksum.Checksum})
	}
	if record.AccessMethods != nil {
		methods := make([]generated.AccessMethod, 0, len(*record.AccessMethods))
		for _, method := range *record.AccessMethods {
			methods = append(methods, toGeneratedAccessMethod(method))
		}
		out.AccessMethods = &methods
	}
	if record.Aliases != nil {
		out.Aliases = record.Aliases
	}
	if record.Contents != nil {
		contents := make([]generated.ContentsObject, 0, len(*record.Contents))
		for _, content := range *record.Contents {
			contents = append(contents, toGeneratedContent(content))
		}
		out.Contents = &contents
	}
	return out
}

// ObjectPayload builds the compatibility DRS response payload. Unknown
// properties remain raw JSON values so response encoding does not coerce them
// through interface{}.
func ObjectPayload(record objects.Record) map[string]json.RawMessage {
	var payload map[string]json.RawMessage
	data, err := json.Marshal(ToGenerated(record))
	if err == nil {
		if err := json.Unmarshal(data, &payload); err == nil {
			if record.Id != "" {
				payload["id"], _ = json.Marshal(string(record.Id))
				payload["did"], _ = json.Marshal(string(record.Id))
			}
			if record.NameAliases != nil {
				if encoded, marshalErr := json.Marshal(record.NameAliases); marshalErr == nil {
					payload["name_aliases"] = encoded
				}
			}
			for key, value := range record.Properties {
				switch key {
				case "id", "did", "checksums", "hashes", "access_methods", "controlled_access", "created_time", "updated_time", "name", "name_aliases", "description", "mime_type", "size", "self_uri", "version", "aliases", "contents", "project", "auth", "authz", "authorizations", "urls":
					continue
				}
				payload[key] = value
			}
			return payload
		}
	}
	payload = map[string]json.RawMessage{}
	if record.Id != "" {
		payload["id"], _ = json.Marshal(string(record.Id))
		payload["did"], _ = json.Marshal(string(record.Id))
	}
	payload["self_uri"], _ = json.Marshal(record.SelfUri)
	return payload
}

func FromGeneratedAccessMethods(methods []generated.AccessMethod) []objects.AccessMethod {
	out := make([]objects.AccessMethod, 0, len(methods))
	for _, method := range methods {
		out = append(out, fromGeneratedAccessMethod(method))
	}
	return out
}

// ToGeneratedAccessMethods translates domain access methods for generated
// request/response models that embed the DRS access contract.
func ToGeneratedAccessMethods(methods *[]objects.AccessMethod) *[]generated.AccessMethod {
	if methods == nil {
		return nil
	}
	out := make([]generated.AccessMethod, 0, len(*methods))
	for _, method := range *methods {
		out = append(out, toGeneratedAccessMethod(method))
	}
	return &out
}

func FromGeneratedAccessMethodMap(updates map[string][]generated.AccessMethod) map[string][]objects.AccessMethod {
	out := make(map[string][]objects.AccessMethod, len(updates))
	for id, methods := range updates {
		out[id] = FromGeneratedAccessMethods(methods)
	}
	return out
}

func toGeneratedAccessMethod(method objects.AccessMethod) generated.AccessMethod {
	out := generated.AccessMethod{AccessId: method.AccessId, Available: method.Available, Cloud: method.Cloud, Region: method.Region, Type: generated.AccessMethodType(method.Type)}
	if method.AccessUrl != nil {
		out.AccessUrl = &struct {
			Headers *[]string `json:"headers,omitempty"`
			Url     string    `json:"url"`
		}{Headers: method.AccessUrl.Headers, Url: method.AccessUrl.Url}
	}
	if method.Authorizations != nil {
		supported := (*[]generated.AccessMethodAuthorizationsSupportedTypes)(nil)
		if method.Authorizations.SupportedTypes != nil {
			converted := make([]generated.AccessMethodAuthorizationsSupportedTypes, len(*method.Authorizations.SupportedTypes))
			for i, value := range *method.Authorizations.SupportedTypes {
				converted[i] = generated.AccessMethodAuthorizationsSupportedTypes(value)
			}
			supported = &converted
		}
		out.Authorizations = &struct {
			BearerAuthIssuers   *[]string                                             `json:"bearer_auth_issuers,omitempty"`
			DrsObjectId         *string                                               `json:"drs_object_id,omitempty"`
			PassportAuthIssuers *[]string                                             `json:"passport_auth_issuers,omitempty"`
			SupportedTypes      *[]generated.AccessMethodAuthorizationsSupportedTypes `json:"supported_types,omitempty"`
		}{BearerAuthIssuers: method.Authorizations.BearerAuthIssuers, DrsObjectId: method.Authorizations.DrsObjectId, PassportAuthIssuers: method.Authorizations.PassportAuthIssuers, SupportedTypes: supported}
	}
	return out
}

func fromGeneratedAccessMethod(method generated.AccessMethod) objects.AccessMethod {
	out := objects.AccessMethod{AccessId: method.AccessId, Available: method.Available, Cloud: method.Cloud, Region: method.Region, Type: string(method.Type)}
	if method.AccessUrl != nil {
		out.AccessUrl = &objects.AccessURL{Headers: method.AccessUrl.Headers, Url: method.AccessUrl.Url}
	}
	if method.Authorizations != nil {
		var supported *[]string
		if method.Authorizations.SupportedTypes != nil {
			converted := make([]string, len(*method.Authorizations.SupportedTypes))
			for i, value := range *method.Authorizations.SupportedTypes {
				converted[i] = string(value)
			}
			supported = &converted
		}
		out.Authorizations = &objects.AccessAuthorizations{BearerAuthIssuers: method.Authorizations.BearerAuthIssuers, DrsObjectId: method.Authorizations.DrsObjectId, PassportAuthIssuers: method.Authorizations.PassportAuthIssuers, SupportedTypes: supported}
	}
	return out
}

func toGeneratedContent(content objects.Content) generated.ContentsObject {
	out := generated.ContentsObject{DrsUri: content.DrsUri, Id: content.Id, Name: content.Name}
	if content.Contents != nil {
		nested := make([]generated.ContentsObject, 0, len(*content.Contents))
		for _, child := range *content.Contents {
			nested = append(nested, toGeneratedContent(child))
		}
		out.Contents = &nested
	}
	return out
}

func fromGeneratedContent(content generated.ContentsObject) objects.Content {
	out := objects.Content{DrsUri: content.DrsUri, Id: content.Id, Name: content.Name}
	if content.Contents != nil {
		nested := make([]objects.Content, 0, len(*content.Contents))
		for _, child := range *content.Contents {
			nested = append(nested, fromGeneratedContent(child))
		}
		out.Contents = &nested
	}
	return out
}
