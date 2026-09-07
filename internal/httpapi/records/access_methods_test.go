package records

import (
	"encoding/json"
	"reflect"
	"testing"

	generated "github.com/calypr/syfon/apigen/server/drs"
)

func TestAccessMethodsRoundTripPreservesGeneratedWireShape(t *testing.T) {
	accessID := "access"
	available := true
	cloud := "aws"
	region := "us-east-1"
	headers := []string{"authorization", "x-test"}
	supported := []generated.AccessMethodAuthorizationsSupportedTypes{generated.AccessMethodAuthorizationsSupportedTypesBearerAuth}
	want := generated.AccessMethod{
		AccessId:  &accessID,
		Available: &available,
		Cloud:     &cloud,
		Region:    &region,
		Type:      generated.AccessMethodTypeS3,
		AccessUrl: &struct {
			Headers *[]string `json:"headers,omitempty"`
			Url     string    `json:"url"`
		}{Headers: &headers, Url: "s3://bucket/key"},
		Authorizations: &struct {
			BearerAuthIssuers   *[]string                                             `json:"bearer_auth_issuers,omitempty"`
			DrsObjectId         *string                                               `json:"drs_object_id,omitempty"`
			PassportAuthIssuers *[]string                                             `json:"passport_auth_issuers,omitempty"`
			SupportedTypes      *[]generated.AccessMethodAuthorizationsSupportedTypes `json:"supported_types,omitempty"`
		}{SupportedTypes: &supported},
	}

	got := toGeneratedAccessMethod(fromGeneratedAccessMethod(want))
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(wantJSON, gotJSON) {
		t.Fatalf("access method wire shape changed: want %s got %s", wantJSON, gotJSON)
	}
}
