package config

import "github.com/calypr/syfon/internal/common"

const (
	S3Prefix    = common.S3Prefix
	GCSPrefix   = common.GCSPrefix
	AzurePrefix = common.AzurePrefix
	DRSPrefix   = common.DRSPrefix

	// --- Route Constants ---
	RouteHealthz = "/healthz"

	// LFS
	RouteLFSBatch    = "/info/lfs/objects/batch"
	RouteLFSMetadata = "/info/lfs/objects/metadata"
	RouteLFSObject   = "/info/lfs/objects/{oid}"
	RouteLFSVerify   = "/info/lfs/verify"

	// Metrics
	RouteMetricsFiles      = "/index/v1/metrics/files"
	RouteMetricsFileDetail = "/index/v1/metrics/files/{object_id}"
	RouteMetricsSummary    = "/index/v1/metrics/summary"

	// Docs
	RouteSwaggerUI    = "/index/swagger"
	RouteSwaggerUIAlt = "/index/swagger/"
	RouteOpenAPISpec  = "/index/openapi.yaml"
	RouteLFSSpec      = "/index/openapi-lfs.yaml"
	RouteBucketSpec   = "/index/openapi-bucket.yaml"
	RouteInternalSpec = "/index/openapi-internal.yaml"

	// Internal DRS Data
	RouteInternalDownload          = "/data/download/{file_id}"
	RouteInternalDownloadPart      = "/data/download/{file_id}/part"
	RouteInternalUpload            = "/data/upload"
	RouteInternalUploadURL         = "/data/upload/{file_id}"
	RouteInternalUploadBulk        = "/data/upload/bulk"
	RouteInternalMultipartInit     = "/data/multipart/init"
	RouteInternalMultipartUpload   = "/data/multipart/upload"
	RouteInternalMultipartComplete = "/data/multipart/complete"
	RouteInternalBuckets           = "/data/buckets"
	RouteInternalBucketDetail      = "/data/buckets/{bucket}"
	RouteInternalBucketScopes      = "/data/buckets/{bucket}/scopes"

	// Internal DRS Index
	RouteInternalIndex             = "/index"
	RouteInternalIndexDetail       = "/index/{id}"
	RouteInternalBulkHashes        = "/index/bulk/hashes"
	RouteInternalBulkDeleteHashes  = "/index/bulk/delete"
	RouteInternalBulkSHA256        = "/index/bulk/sha256/validity"
	RouteInternalBulkSHA256Missing = "/index/bulk/sha256/missing"
	RouteInternalBulkCreate        = "/index/bulk"
	RouteInternalBulkDocs          = "/index/bulk/documents"

	// Core API
	RouteCoreSHA256 = "/index/v1/sha256/validity"
)

const (
	AuthModeLocal = "local"
	AuthModeGen3  = "gen3"
)

type Config struct {
	Port                 int                        `json:"port" yaml:"port"`
	Database             DatabaseConfig             `json:"database" yaml:"database"`
	Buckets              []BucketConfig             `json:"buckets,omitempty" yaml:"buckets,omitempty"`
	S3Credentials        []BucketConfig             `json:"s3_credentials,omitempty" yaml:"s3_credentials,omitempty"`
	BucketScopes         []BucketScopeConfig        `json:"bucket_scopes" yaml:"bucket_scopes"`
	CredentialEncryption CredentialEncryptionConfig `json:"credential_encryption" yaml:"credential_encryption"`
	Auth                 AuthConfig                 `json:"auth" yaml:"auth"`
	LFS                  LFSConfig                  `json:"lfs" yaml:"lfs"`
	Signing              SigningConfig              `json:"signing" yaml:"signing"`
	Routes               RoutesConfig               `json:"routes" yaml:"routes"`
}

type RoutesConfig struct {
	Docs     bool `json:"docs" yaml:"docs"`
	Ga4gh    bool `json:"ga4gh" yaml:"ga4gh"`
	Metrics  bool `json:"metrics" yaml:"metrics"`
	Internal bool `json:"internal" yaml:"internal"`
	LFS      bool `json:"lfs" yaml:"lfs"`
}

type DatabaseConfig struct {
	Sqlite   *SqliteConfig   `json:"sqlite,omitempty" yaml:"sqlite,omitempty"`
	Postgres *PostgresConfig `json:"postgres,omitempty" yaml:"postgres,omitempty"`
}

type SqliteConfig struct {
	File string `json:"file" yaml:"file"`
}

type PostgresConfig struct {
	Host     string `json:"host" yaml:"host"`
	Port     int    `json:"port" yaml:"port"`
	User     string `json:"user" yaml:"user"`
	Password string `json:"password" yaml:"password"`
	Database string `json:"database" yaml:"database"`
	SSLMode  string `json:"sslmode" yaml:"sslmode"`
}

type CredentialEncryptionConfig struct {
	LocalKeyFile string `json:"local_key_file" yaml:"local_key_file"`
	MasterKey    string `json:"master_key" yaml:"master_key"`
}

type BucketConfig struct {
	CredentialID string                 `json:"-" yaml:"-"`
	Bucket       string                 `json:"bucket" yaml:"bucket"`
	Provider     string                 `json:"provider,omitempty" yaml:"provider,omitempty"`
	Region       string                 `json:"region" yaml:"region"`
	AccessKey    string                 `json:"access_key" yaml:"access_key"`
	SecretKey    string                 `json:"secret_key" yaml:"secret_key"`
	Endpoint     string                 `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
	Resources    []BucketResourceConfig `json:"resources,omitempty" yaml:"resources,omitempty"`
}

type S3Config = BucketConfig

type BucketResourceConfig struct {
	Organization string `json:"organization" yaml:"organization"`
	OrgPath      string `json:"org_path,omitempty" yaml:"org_path,omitempty"`

	Projects []BucketProjectConfig `json:"projects,omitempty" yaml:"projects,omitempty"`
}

type BucketProjectConfig struct {
	ProjectID   string `json:"project_id,omitempty" yaml:"project_id,omitempty"`
	Project     string `json:"project,omitempty" yaml:"project,omitempty"`
	ProjectPath string `json:"project_path,omitempty" yaml:"project_path,omitempty"`
	Path        string `json:"path,omitempty" yaml:"path,omitempty"`
	PathPrefix  string `json:"path_prefix,omitempty" yaml:"path_prefix,omitempty"`
}

type BucketScopeConfig struct {
	Organization        string `json:"organization" yaml:"organization"`
	ProjectID           string `json:"project_id" yaml:"project_id"`
	CredentialID        string `json:"-" yaml:"-"`
	Bucket              string `json:"bucket,omitempty" yaml:"bucket,omitempty"`
	Path                string `json:"path,omitempty" yaml:"path,omitempty"`
	PathPrefix          string `json:"path_prefix,omitempty" yaml:"path_prefix,omitempty"`
	OrganizationSubPath string `json:"organization_sub_path,omitempty" yaml:"organization_sub_path,omitempty"`
	ProjectSubPath      string `json:"project_sub_path,omitempty" yaml:"project_sub_path,omitempty"`
}

type AuthConfig struct {
	Mode                 string          `json:"mode" yaml:"mode"`
	Basic                BasicAuthConfig `json:"basic" yaml:"basic"`
	LocalAuthzCSV        string          `json:"local_authz_csv" yaml:"local_authz_csv"`
	AllowUnauthenticated bool            `json:"allow_unauthenticated" yaml:"allow_unauthenticated"`
	Mock                 MockAuthConfig  `json:"mock" yaml:"mock"`
	PluginPaths          PluginPaths     `json:"plugin_paths" yaml:"plugin_paths"`
	FenceURL             string          `json:"fence_url" yaml:"fence_url"`
}

type MockAuthConfig struct {
	Enabled           bool     `json:"enabled" yaml:"enabled"`
	RequireAuthHeader bool     `json:"require_auth_header" yaml:"require_auth_header"`
	Resources         []string `json:"resources" yaml:"resources"`
	Methods           []string `json:"methods" yaml:"methods"`
}

type PluginPaths struct {
	Authz string `json:"authz" yaml:"authz"`
	Authn string `json:"authn" yaml:"authn"`
}

type BasicAuthConfig struct {
	Username string `json:"username" yaml:"username"`
	Password string `json:"password" yaml:"password"`
}

type SigningConfig struct {
	DefaultExpirySeconds int `json:"default_expiry_seconds" yaml:"default_expiry_seconds"`
}

type LFSConfig struct {
	MaxBatchObjects              int   `json:"max_batch_objects" yaml:"max_batch_objects"`
	MaxBatchBodyBytes            int64 `json:"max_batch_body_bytes" yaml:"max_batch_body_bytes"`
	RequestLimitPerMinute        int   `json:"request_limit_per_minute" yaml:"request_limit_per_minute"`
	BandwidthLimitBytesPerMinute int64 `json:"bandwidth_limit_bytes_per_minute" yaml:"bandwidth_limit_bytes_per_minute"`
}
