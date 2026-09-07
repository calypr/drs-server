package crypto

import (
	"os"
	"strings"
)

const (
	CredentialMasterKeyEnv    = "DRS_CREDENTIAL_MASTER_KEY"
	CredentialLocalKeyFileEnv = "DRS_CREDENTIAL_LOCAL_KEY_FILE"
	DatabaseSQLiteFileEnv     = "DRS_DB_SQLITE_FILE"
	CredentialKeyManagerEnv   = "DRS_CREDENTIAL_KEY_MANAGER"
	CredentialKMSKeyIDEnv     = "DRS_CREDENTIAL_KMS_KEY_ID"
)

func configuredCredentialKeyManagerName() string {
	if name := strings.ToLower(strings.TrimSpace(os.Getenv(CredentialKeyManagerEnv))); name != "" {
		return name
	}
	if strings.TrimSpace(os.Getenv(CredentialKMSKeyIDEnv)) != "" {
		return awsKMSKeyManagerName
	}
	return defaultCredentialKeyManager
}

func CredentialEncryptionEnabled() (bool, error) {
	key, err := credentialMasterKey()
	if err != nil {
		return false, err
	}
	return len(key) == 32, nil
}
