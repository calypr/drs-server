package config

import (
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/buckets"
)

const validYAMLConfig = `
auth:
  mode: local
  allow_unauthenticated: true
database:
  sqlite:
    file: ":memory:"
`

const validJSONConfig = `{
  "auth": {"mode": "local", "allow_unauthenticated": true},
  "database": {"sqlite": {"file": ":memory:"}}
}`

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	tests := []struct {
		name      string
		extension string
		content   string
	}{
		{name: "yaml top level", extension: ".yaml", content: validYAMLConfig + "unknown: true\n"},
		{name: "yaml nested", extension: ".yaml", content: strings.Replace(validYAMLConfig, "    file: \":memory:\"", "    file: \":memory:\"\n    unknown: true", 1)},
		{name: "json top level", extension: ".json", content: strings.TrimSuffix(validJSONConfig, "}") + `, "unknown": true}`},
		{name: "json nested", extension: ".json", content: strings.Replace(validJSONConfig, `"file": ":memory:"`, `"file": ":memory:", "unknown": true`, 1)},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := LoadConfig(writeConfigTestFileWithExtension(t, testCase.content, testCase.extension))
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unknown") {
				t.Fatalf("LoadConfig error = %v, want unknown-field rejection", err)
			}
		})
	}
}

func TestLoadConfigRejectsTrailingDocumentsAndValues(t *testing.T) {
	tests := []struct {
		name      string
		extension string
		content   string
	}{
		{name: "yaml document", extension: ".yaml", content: validYAMLConfig + "---\nport: 9000\n"},
		{name: "yaml malformed document", extension: ".yaml", content: validYAMLConfig + "---\nport: [\n"},
		{name: "json value", extension: ".json", content: validJSONConfig + ` {"port": 9000}`},
		{name: "json malformed value", extension: ".json", content: validJSONConfig + ` {`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := LoadConfig(writeConfigTestFileWithExtension(t, testCase.content, testCase.extension)); err == nil {
				t.Fatal("LoadConfig accepted trailing configuration content")
			}
		})
	}
}

func TestLoadConfigNormalizesCredentialIdentityWithoutChangingSecrets(t *testing.T) {
	content := validYAMLConfig + `
buckets:
  - bucket: "  EllrottLab  "
    provider: " S3 "
    region: " US-EAST-1 "
    endpoint: " https://MINIO.example/ "
    access_key: " access-key "
    secret_key: " secret-key "
`
	cfg, err := LoadConfig(writeConfigTestFile(t, content))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Buckets) != 1 {
		t.Fatalf("buckets = %+v", cfg.Buckets)
	}
	credential := cfg.Buckets[0]
	if credential.Bucket != "EllrottLab" || credential.Provider != "s3" || credential.Region != "us-east-1" || credential.Endpoint != "https://MINIO.example" {
		t.Fatalf("normalized credential = %+v", credential)
	}
	if credential.AccessKey != " access-key " || credential.SecretKey != " secret-key " {
		t.Fatalf("credential material was changed: access=%q secret=%q", credential.AccessKey, credential.SecretKey)
	}
	wantID := buckets.DeriveCredentialID(credential.Bucket, credential.Provider, credential.Region, credential.Endpoint, credential.AccessKey)
	if credential.CredentialID != wantID {
		t.Fatalf("credential ID = %q, want %q", credential.CredentialID, wantID)
	}
}

func TestLoadConfigCollapsesEquivalentBucketScopes(t *testing.T) {
	content := validYAMLConfig + `
bucket_scopes:
  - organization: " org "
    project_id: " project "
    bucket: bucket
    path_prefix: /prefix/
  - organization: org
    project_id: project
    bucket: " bucket "
    path_prefix: prefix
`
	cfg, err := LoadConfig(writeConfigTestFile(t, content))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.BucketScopes) != 1 {
		t.Fatalf("equivalent scopes were not collapsed: %+v", cfg.BucketScopes)
	}
}

func TestLoadConfigRejectsConflictingExplicitAndDerivedScopes(t *testing.T) {
	content := validYAMLConfig + `
buckets:
  - bucket: example-bucket
    provider: s3
    region: us-east-1
    access_key: access
    secret_key: secret
    resources:
      - organization: org
        projects:
          - project_id: project
            path_prefix: derived
bucket_scopes:
  - organization: org
    project_id: project
    bucket: example-bucket
    path_prefix: explicit
`
	_, err := LoadConfig(writeConfigTestFile(t, content))
	if err == nil || !strings.Contains(err.Error(), "bucket_scopes[0]") || !strings.Contains(err.Error(), "buckets[0].resources[0].projects[0]") {
		t.Fatalf("LoadConfig error = %v, want both conflicting field paths", err)
	}
}

func TestLoadConfigCollapsesEquivalentExplicitAndDerivedScopes(t *testing.T) {
	content := validYAMLConfig + `
buckets:
  - bucket: example-bucket
    provider: s3
    region: us-east-1
    access_key: access
    secret_key: secret
    resources:
      - organization: org
        projects:
          - project_id: project
            path_prefix: prefix
bucket_scopes:
  - organization: " org "
    project_id: " project "
    bucket: " example-bucket "
    path_prefix: /prefix/
`
	cfg, err := LoadConfig(writeConfigTestFile(t, content))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.BucketScopes) != 1 {
		t.Fatalf("equivalent explicit and derived scopes were not collapsed: %+v", cfg.BucketScopes)
	}
}

func TestLoadConfigRejectsConflictingProjectAliases(t *testing.T) {
	content := validYAMLConfig + `
buckets:
  - bucket: example-bucket
    provider: s3
    region: us-east-1
    access_key: access
    secret_key: secret
    resources:
      - organization: org
        projects:
          - project_id: one
            project: two
`
	_, err := LoadConfig(writeConfigTestFile(t, content))
	if err == nil || !strings.Contains(err.Error(), "project_id") || !strings.Contains(err.Error(), "project") {
		t.Fatalf("LoadConfig error = %v, want conflicting project aliases", err)
	}
}
