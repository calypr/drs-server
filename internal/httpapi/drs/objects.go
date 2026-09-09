package drs

import (
	"encoding/json"
	generated "github.com/calypr/syfon/apigen/drs"
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
		Size:             &value.Size,
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

// ObjectResponse is the typed DRS response with the legacy identity and alias
// fields retained by this server's wire contract.
type ObjectResponse struct {
	generated.DrsObject
	Did         string    `json:"did,omitempty"`
	NameAliases *[]string `json:"name_aliases,omitempty"`
}

// MarshalJSON preserves the minimal identity response when a generated DRS
// field cannot be encoded, such as an invalid timestamp.
func (value ObjectResponse) MarshalJSON() ([]byte, error) {
	type response struct {
		generated.DrsObject
		Did         string    `json:"did,omitempty"`
		NameAliases *[]string `json:"name_aliases,omitempty"`
	}
	encoded, err := json.Marshal(response(value))
	if err == nil {
		return encoded, nil
	}
	type fallback struct {
		ID      string `json:"id,omitempty"`
		DID     string `json:"did,omitempty"`
		SelfURI string `json:"self_uri"`
	}
	return json.Marshal(fallback{ID: value.Id, DID: value.Did, SelfURI: value.SelfUri})
}

// ObjectPayload projects a domain record into the typed DRS response.
func ObjectPayload(record objects.Record) ObjectResponse {
	var aliases *[]string
	if record.NameAliases != nil {
		copyAliases := append([]string(nil), record.NameAliases...)
		aliases = &copyAliases
	}
	return ObjectResponse{
		DrsObject:   ToGenerated(record),
		Did:         string(record.Id),
		NameAliases: aliases,
	}
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

func drsPtr[T any](value T) *T {
	return &value
}
