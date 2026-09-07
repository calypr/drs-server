package crypto

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

const (
	defaultCredentialKeyManager = "local"
	awsKMSKeyManagerName        = "aws-kms"
)

type WrappedDataKey struct {
	Manager    string
	KeyID      string
	Ciphertext string
}

type CredentialKeyManager interface {
	Name() string
	WrapDataKey(ctx context.Context, dataKey []byte) (*WrappedDataKey, error)
	UnwrapDataKey(ctx context.Context, wrapped *WrappedDataKey) ([]byte, error)
}

var (
	credentialKeyManagerRegistryMu sync.RWMutex
	credentialKeyManagerRegistry   = map[string]func() (CredentialKeyManager, error){
		defaultCredentialKeyManager: func() (CredentialKeyManager, error) { return &localKeyManager{}, nil },
		awsKMSKeyManagerName:        newAWSKMSKeyManagerFromEnv,
	}
)

func resolveCredentialKeyManager(name string) (CredentialKeyManager, error) {
	managerName := strings.ToLower(strings.TrimSpace(name))
	if managerName == "" {
		managerName = defaultCredentialKeyManager
	}
	credentialKeyManagerRegistryMu.RLock()
	factory, ok := credentialKeyManagerRegistry[managerName]
	credentialKeyManagerRegistryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("credential key manager %q is not registered", managerName)
	}
	manager, err := factory()
	if err != nil {
		return nil, fmt.Errorf("initialize credential key manager %q: %w", managerName, err)
	}
	if manager == nil {
		return nil, fmt.Errorf("credential key manager %q returned nil", managerName)
	}
	return manager, nil
}
