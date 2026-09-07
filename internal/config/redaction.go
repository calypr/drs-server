package config

import "encoding/json"

// SECURITY FIX MED-1: Redact password when marshaling to JSON
func (b BasicAuthConfig) MarshalJSON() ([]byte, error) {
	type Alias BasicAuthConfig
	return json.Marshal(&struct {
		Password string `json:"password"`
		*Alias
	}{
		Password: "***REDACTED***",
		Alias:    (*Alias)(&b),
	})
}

// SECURITY FIX MED-1: Redact secret key when marshaling to JSON
func (b BucketConfig) MarshalJSON() ([]byte, error) {
	type Alias BucketConfig
	return json.Marshal(&struct {
		SecretKey string `json:"secret_key"`
		AccessKey string `json:"access_key"`
		*Alias
	}{
		SecretKey: "***REDACTED***",
		AccessKey: "***REDACTED***",
		Alias:     (*Alias)(&b),
	})
}

// SECURITY FIX MED-1: Redact password when marshaling to JSON
func (p PostgresConfig) MarshalJSON() ([]byte, error) {
	type Alias PostgresConfig
	return json.Marshal(&struct {
		Password string `json:"password"`
		*Alias
	}{
		Password: "***REDACTED***",
		Alias:    (*Alias)(&p),
	})
}

func (c CredentialEncryptionConfig) MarshalJSON() ([]byte, error) {
	masterKey := ""
	if c.MasterKey != "" {
		masterKey = "***REDACTED***"
	}
	return json.Marshal(&struct {
		LocalKeyFile string `json:"local_key_file"`
		MasterKey    string `json:"master_key"`
	}{
		LocalKeyFile: c.LocalKeyFile,
		MasterKey:    masterKey,
	})
}
