package credentialcipher

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

// Cipher encrypts and decrypts credential fields using the key manager selected
// at construction time. Key material is still loaded by the manager when an
// operation needs it.
type Cipher struct {
	managerName string
}

func NewFromEnv() (*Cipher, error) {
	return &Cipher{managerName: configuredCredentialKeyManagerName()}, nil
}

func (c *Cipher) manager() (CredentialKeyManager, error) {
	name := defaultCredentialKeyManager
	if c != nil && strings.TrimSpace(c.managerName) != "" {
		name = c.managerName
	}
	return resolveCredentialKeyManager(name)
}

func (c *Cipher) Enabled() (bool, error) {
	manager, err := c.manager()
	if err != nil {
		return false, err
	}
	if manager.Name() != defaultCredentialKeyManager {
		return true, nil
	}
	key, err := credentialMasterKey()
	if err != nil {
		return false, err
	}
	return len(key) == 32, nil
}
