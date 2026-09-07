package buckets

// Credential represents the s3_credential table and the provider credentials
// consumed by storage implementations. SecretKey is never part of derived
// identity, but remains available to the provider after decryption.
type Credential struct {
	CredentialID string `db:"credential_id"`
	Bucket       string `db:"bucket"`
	Provider     string `db:"provider"`
	Region       string `db:"region"`
	AccessKey    string `db:"access_key"`
	SecretKey    string `db:"secret_key"`
	Endpoint     string `db:"endpoint"`
}

// Scope maps a Gen3 organization/project resource to a bucket credential.
type Scope struct {
	Organization string `db:"organization"`
	ProjectID    string `db:"project_id"`
	CredentialID string `db:"credential_id"`
	Bucket       string `db:"bucket"`
	PathPrefix   string `db:"path_prefix"`
}

// VisibilityRow is the minimum storage projection needed to build bucket
// visibility responses without hydrating full objects.
type VisibilityRow struct {
	AccessURL  string
	AccessType string
	Resource   string
}

// VisibleBucket is the credential and resource projection exposed to callers
// that need to authorize access to a physical bucket.
type VisibleBucket struct {
	Credential Credential
	Programs   []string
}
