package drs

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	generated "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/objects"
)

func TestToGeneratedChecksumNilAndEmptySlicesRemainDistinct(t *testing.T) {
	nilValue := ToGenerated(objects.Record{})
	if nilValue.Checksums != nil {
		t.Fatalf("nil domain checksums became empty generated slice: %#v", nilValue.Checksums)
	}
	emptyValue := ToGenerated(objects.Record{Checksums: []objects.Checksum{}})
	if emptyValue.Checksums == nil || len(emptyValue.Checksums) != 0 {
		t.Fatalf("empty domain checksums changed: %#v", emptyValue.Checksums)
	}
}

func TestObjectPayloadUsesTypedNestedResponse(t *testing.T) {
	name := "sample"
	aliases := []string{"sample.alias"}
	created := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	childID := "child"
	record := objects.Record{
		Id: "record-1", Name: &name, NameAliases: aliases, CreatedTime: created,
		Contents: &[]objects.Content{{Id: &childID, Contents: &[]objects.Content{{Name: "leaf"}}}},
	}
	data, err := json.Marshal(ObjectPayload(record))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"id": `"record-1"`, "did": `"record-1"`, "name_aliases": `["sample.alias"]`} {
		if string(payload[key]) != want {
			t.Fatalf("%s = %s, want %s", key, payload[key], want)
		}
	}
	var contents []generated.ContentsObject
	if err := json.Unmarshal(payload["contents"], &contents); err != nil {
		t.Fatal(err)
	}
	if len(contents) != 1 || contents[0].Contents == nil || len(*contents[0].Contents) != 1 {
		t.Fatalf("nested contents = %#v", contents)
	}
}

func TestObjectPayloadInvalidTimeFallsBackToIdentity(t *testing.T) {
	record := objects.Record{Id: "record-1", SelfUri: "drs://example/record-1", CreatedTime: time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)}
	data, err := json.Marshal(ObjectPayload(record))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 3 || string(payload["id"]) != `"record-1"` || string(payload["did"]) != `"record-1"` || string(payload["self_uri"]) != `"drs://example/record-1"` {
		t.Fatalf("fallback payload = %s", data)
	}
}

func TestAccessMethodsRoundTripPreservesGeneratedWireShape(t *testing.T) {
	accessID := "access"
	available := true
	cloud := "aws"
	region := "us-east-1"
	headers := []string{"authorization", "x-test"}
	supported := []generated.AccessMethodAuthorizationsSupportedTypes{generated.AccessMethodAuthorizationsSupportedTypesBearerAuth}
	want := generated.AccessMethod{
		AccessId: &accessID, Available: &available, Cloud: &cloud, Region: &region, Type: generated.AccessMethodTypeS3,
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
