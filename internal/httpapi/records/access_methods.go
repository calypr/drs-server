package records

import (
	generated "github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/objects"
)

func fromGeneratedAccessMethods(methods []generated.AccessMethod) []objects.AccessMethod {
	out := make([]objects.AccessMethod, 0, len(methods))
	for _, method := range methods {
		out = append(out, fromGeneratedAccessMethod(method))
	}
	return out
}

func toGeneratedAccessMethods(methods *[]objects.AccessMethod) *[]generated.AccessMethod {
	if methods == nil {
		return nil
	}
	out := make([]generated.AccessMethod, 0, len(*methods))
	for _, method := range *methods {
		out = append(out, toGeneratedAccessMethod(method))
	}
	return &out
}

func fromGeneratedAccessMethod(method generated.AccessMethod) objects.AccessMethod {
	out := objects.AccessMethod{
		AccessId:  method.AccessId,
		Available: method.Available,
		Cloud:     method.Cloud,
		Region:    method.Region,
		Type:      string(method.Type),
	}
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
		out.Authorizations = &objects.AccessAuthorizations{
			BearerAuthIssuers:   method.Authorizations.BearerAuthIssuers,
			DrsObjectId:         method.Authorizations.DrsObjectId,
			PassportAuthIssuers: method.Authorizations.PassportAuthIssuers,
			SupportedTypes:      supported,
		}
	}
	return out
}

func toGeneratedAccessMethod(method objects.AccessMethod) generated.AccessMethod {
	out := generated.AccessMethod{
		AccessId:  method.AccessId,
		Available: method.Available,
		Cloud:     method.Cloud,
		Region:    method.Region,
		Type:      generated.AccessMethodType(method.Type),
	}
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
		}{
			BearerAuthIssuers:   method.Authorizations.BearerAuthIssuers,
			DrsObjectId:         method.Authorizations.DrsObjectId,
			PassportAuthIssuers: method.Authorizations.PassportAuthIssuers,
			SupportedTypes:      supported,
		}
	}
	return out
}
