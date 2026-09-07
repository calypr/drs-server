package credentialcipher

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
)

type awsKMSKeyManager struct {
	client *kms.Client
	keyID  string
}

func (m *awsKMSKeyManager) Name() string { return awsKMSKeyManagerName }

func (m *awsKMSKeyManager) WrapDataKey(ctx context.Context, dataKey []byte) (*WrappedDataKey, error) {
	if strings.TrimSpace(m.keyID) == "" {
		return nil, fmt.Errorf("%s is required for %s", CredentialKMSKeyIDEnv, awsKMSKeyManagerName)
	}
	out, err := m.client.Encrypt(ctx, &kms.EncryptInput{
		KeyId:             aws.String(m.keyID),
		Plaintext:         dataKey,
		EncryptionContext: map[string]string{"purpose": "syfon-credential-dek"},
	})
	if err != nil {
		return nil, fmt.Errorf("kms encrypt failed: %w", err)
	}
	return &WrappedDataKey{
		Manager:    m.Name(),
		KeyID:      aws.ToString(out.KeyId),
		Ciphertext: base64.RawStdEncoding.EncodeToString(out.CiphertextBlob),
	}, nil
}

func (m *awsKMSKeyManager) UnwrapDataKey(ctx context.Context, wrapped *WrappedDataKey) ([]byte, error) {
	if wrapped == nil {
		return nil, errors.New("wrapped data key is required")
	}
	blob, err := base64.RawStdEncoding.DecodeString(wrapped.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode wrapped data key: %w", err)
	}
	out, err := m.client.Decrypt(ctx, &kms.DecryptInput{
		CiphertextBlob:    blob,
		EncryptionContext: map[string]string{"purpose": "syfon-credential-dek"},
	})
	if err != nil {
		return nil, fmt.Errorf("kms decrypt failed: %w", err)
	}
	return out.Plaintext, nil
}

func newAWSKMSKeyManagerFromEnv() (CredentialKeyManager, error) {
	loadOpts := []func(*awsconfig.LoadOptions) error{}
	if strings.TrimSpace(os.Getenv("AWS_REGION")) == "" && strings.TrimSpace(os.Getenv("AWS_DEFAULT_REGION")) == "" {
		loadOpts = append(loadOpts, awsconfig.WithRegion("us-east-1"))
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return &awsKMSKeyManager{
		client: kms.NewFromConfig(cfg),
		keyID:  strings.TrimSpace(os.Getenv(CredentialKMSKeyIDEnv)),
	}, nil
}
