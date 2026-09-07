package models

// S3Credential represents the 's3_credential' table
type S3Credential struct {
	CredentialID string `db:"credential_id"`
	Bucket       string `db:"bucket"`
	Provider     string `db:"provider"`
	Region       string `db:"region"`
	AccessKey    string `db:"access_key"`
	SecretKey    string `db:"secret_key"`
	Endpoint     string `db:"endpoint"`
}

type BucketScope struct {
	Organization string `db:"organization"`
	ProjectID    string `db:"project_id"`
	CredentialID string `db:"credential_id"`
	Bucket       string `db:"bucket"`
	PathPrefix   string `db:"path_prefix"`
}
