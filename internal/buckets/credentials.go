package buckets

import (
	"context"
	"fmt"
	"strings"
)

// SaveS3Credential persists a credential before invalidating every identity
// alias that could key a provider signer.
func (s *Service) SaveS3Credential(ctx context.Context, cred *Credential) error {
	requestedID := ""
	physicalBucket := ""
	if cred != nil {
		requestedID = s.credentialIDForCredential(*cred)
		physicalBucket = strings.TrimSpace(cred.Bucket)
	}
	if err := s.credentialAdmin.SaveS3Credential(ctx, cred); err != nil {
		return err
	}

	aliases := []string{requestedID, physicalBucket}
	if cred != nil {
		aliases = append(aliases, s.credentialIDForCredential(*cred), cred.CredentialID, cred.Bucket)
	}
	s.invalidateAliases(aliases...)
	return nil
}

// DeleteS3Credential resolves aliases before mutation, then invalidates all
// known aliases only after the repository confirms deletion.
func (s *Service) DeleteS3Credential(ctx context.Context, bucket string) error {
	requested := strings.TrimSpace(bucket)
	var resolved *Credential
	if cred, err := s.credentialReader.GetS3Credential(ctx, bucket); err == nil && cred != nil {
		copy := *cred
		resolved = &copy
	}

	if err := s.credentialAdmin.DeleteS3Credential(ctx, bucket); err != nil {
		return err
	}
	s.scopeCache.clear()
	aliases := []string{requested}
	if resolved != nil {
		aliases = append(aliases, resolved.CredentialID, resolved.Bucket)
	}
	s.invalidateAliases(aliases...)
	return nil
}

func (s *Service) invalidateAliases(aliases ...string) {
	if s.signerCacheInvalidator == nil {
		return
	}
	seen := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		seen[alias] = struct{}{}
		s.signerCacheInvalidator.InvalidateBucket(alias)
	}
}

func (s *Service) credentialIDForCredential(cred Credential) string {
	if credentialID := strings.TrimSpace(cred.CredentialID); credentialID != "" {
		return credentialID
	}
	return strings.TrimSpace(cred.Bucket)
}

func (s *Service) ResolveBucket(ctx context.Context, bucketName string) (string, error) {
	creds, err := s.ListS3Credentials(ctx)
	if err != nil {
		return "", err
	}
	if len(creds) == 0 {
		return "", fmt.Errorf("no buckets configured")
	}
	if bucketName == "" {
		return creds[0].Bucket, nil
	}
	for _, cred := range creds {
		if cred.Bucket == bucketName {
			return cred.Bucket, nil
		}
	}
	return "", fmt.Errorf("bucket %q not configured", bucketName)
}
