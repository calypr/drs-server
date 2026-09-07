package credentialcipher

import (
	"context"
	"fmt"

	"github.com/calypr/syfon/internal/buckets"
)

func PrepareS3CredentialForStorage(ctx context.Context, cred *buckets.Credential) (*buckets.Credential, error) {
	if cred == nil {
		return nil, fmt.Errorf("credential is required")
	}
	out := *cred
	var err error
	out.AccessKey, err = EncryptCredentialField(ctx, out.AccessKey)
	if err != nil {
		return nil, err
	}
	out.SecretKey, err = EncryptCredentialField(ctx, out.SecretKey)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func ParseS3CredentialFromStorage(ctx context.Context, cred *buckets.Credential) (*buckets.Credential, error) {
	if cred == nil {
		return nil, fmt.Errorf("credential is required")
	}
	out := *cred
	var err error
	out.AccessKey, err = DecryptCredentialField(ctx, out.AccessKey)
	if err != nil {
		return nil, err
	}
	out.SecretKey, err = DecryptCredentialField(ctx, out.SecretKey)
	if err != nil {
		return nil, err
	}
	return &out, nil
}
