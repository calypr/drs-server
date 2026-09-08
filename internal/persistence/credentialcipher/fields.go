package credentialcipher

import (
	"context"
	"fmt"

	"github.com/calypr/syfon/internal/buckets"
)

func (c *Cipher) Prepare(ctx context.Context, cred *buckets.Credential) (*buckets.Credential, error) {
	if cred == nil {
		return nil, fmt.Errorf("credential is required")
	}
	out := *cred
	var err error
	out.AccessKey, err = c.EncryptField(ctx, out.AccessKey)
	if err != nil {
		return nil, err
	}
	out.SecretKey, err = c.EncryptField(ctx, out.SecretKey)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Cipher) Parse(ctx context.Context, cred *buckets.Credential) (*buckets.Credential, error) {
	if cred == nil {
		return nil, fmt.Errorf("credential is required")
	}
	out := *cred
	var err error
	out.AccessKey, err = c.DecryptField(ctx, out.AccessKey)
	if err != nil {
		return nil, err
	}
	out.SecretKey, err = c.DecryptField(ctx, out.SecretKey)
	if err != nil {
		return nil, err
	}
	return &out, nil
}
