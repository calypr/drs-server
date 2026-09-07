package crypto

import (
	"errors"

	"github.com/calypr/syfon/internal/models"
)

func PrepareS3CredentialForStorage(cred *models.S3Credential) (*models.S3Credential, error) {
	if cred == nil {
		return nil, errors.New("credential is required")
	}
	out := *cred
	var err error
	out.AccessKey, err = EncryptCredentialField(out.AccessKey)
	if err != nil {
		return nil, err
	}
	out.SecretKey, err = EncryptCredentialField(out.SecretKey)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func ParseS3CredentialFromStorage(cred *models.S3Credential) (*models.S3Credential, error) {
	if cred == nil {
		return nil, errors.New("credential is required")
	}
	out := *cred
	var err error
	out.AccessKey, err = DecryptCredentialField(out.AccessKey)
	if err != nil {
		return nil, err
	}
	out.SecretKey, err = DecryptCredentialField(out.SecretKey)
	if err != nil {
		return nil, err
	}
	return &out, nil
}
