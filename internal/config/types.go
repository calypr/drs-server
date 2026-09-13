package config

const (
	AuthModeLocal = "local"
	AuthModeGen3  = "gen3"
)

type Config struct {
	// Profile selects stricter operational defaults at the application
	// boundary. An empty profile preserves the historical development behavior.
	Profile              string                     `json:"profile,omitempty" yaml:"profile,omitempty"`
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
	Service              ServiceConfig              `json:"service" yaml:"service"`
	DRS                  DRSConfig                  `json:"drs" yaml:"drs"`
	Multipart            MultipartConfig            `json:"multipart" yaml:"multipart"`
}

const (
	ProfileDevelopment = "development"
	ProfileProduction  = "production"
)

type ServiceConfig struct {
	ID               string `json:"id" yaml:"id"`
	Name             string `json:"name" yaml:"name"`
	Description      string `json:"description" yaml:"description"`
	Environment      string `json:"environment" yaml:"environment"`
	Organization     string `json:"organization" yaml:"organization"`
	OrganizationURL  string `json:"organization_url" yaml:"organization_url"`
	ContactURL       string `json:"contact_url" yaml:"contact_url"`
	DocumentationURL string `json:"documentation_url" yaml:"documentation_url"`
}

type DRSConfig struct {
	MaxBulkRequestLength int `json:"max_bulk_request_length" yaml:"max_bulk_request_length"`
}

type MultipartConfig struct {
	CleanupIntervalSeconds    int `json:"cleanup_interval_seconds" yaml:"cleanup_interval_seconds"`
	InactiveTimeoutSeconds    int `json:"inactive_timeout_seconds" yaml:"inactive_timeout_seconds"`
	CompletedRetentionSeconds int `json:"completed_retention_seconds" yaml:"completed_retention_seconds"`
	BatchSize                 int `json:"batch_size" yaml:"batch_size"`
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
	Host                         string `json:"host" yaml:"host"`
	Port                         int    `json:"port" yaml:"port"`
	User                         string `json:"user" yaml:"user"`
	Password                     string `json:"password" yaml:"password"`
	Database                     string `json:"database" yaml:"database"`
	SSLMode                      string `json:"sslmode" yaml:"sslmode"`
	AllowInsecureTransport       bool   `json:"allow_insecure_transport" yaml:"allow_insecure_transport"`
	MaxOpenConnections           int    `json:"max_open_connections" yaml:"max_open_connections"`
	MaxIdleConnections           int    `json:"max_idle_connections" yaml:"max_idle_connections"`
	ConnectionMaxLifetimeSeconds int    `json:"connection_max_lifetime_seconds" yaml:"connection_max_lifetime_seconds"`
	ConnectionMaxIdleTimeSeconds int    `json:"connection_max_idle_time_seconds" yaml:"connection_max_idle_time_seconds"`
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
