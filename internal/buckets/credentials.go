package buckets

import (
	"context"
	"errors"
	"strings"

	"github.com/calypr/syfon/apigen/errorapi"
)

// SaveS3Credential persists a credential before invalidating every identity
// alias that could key a provider signer.
func (s *Service) SaveS3Credential(ctx context.Context, cred *Credential) error {
	if err := s.credentialAdmin.SaveS3Credential(ctx, cred); err != nil {
		return err
	}
	s.invalidateCredentialAliases(cred)
	return nil
}

func (s *Service) invalidateCredentialAliases(cred *Credential) {
	requestedID := ""
	physicalBucket := ""
	if cred != nil {
		requestedID = s.credentialIDForCredential(*cred)
		physicalBucket = strings.TrimSpace(cred.Bucket)
	}
	aliases := []string{requestedID, physicalBucket}
	if cred != nil {
		aliases = append(aliases, s.credentialIDForCredential(*cred), cred.CredentialID, cred.Bucket)
	}
	s.invalidateAliases(aliases...)
}

// DeleteS3Credential resolves aliases before mutation, then invalidates all
// known aliases only after the repository confirms deletion.
func (s *Service) DeleteS3Credential(ctx context.Context, bucket string) error {
	requested := strings.TrimSpace(bucket)
	resolved, err := s.credentialReader.GetS3Credential(ctx, requested)
	if err != nil && !errors.Is(err, errorapi.ErrStorageCredentialMissing) {
		return err
	}
	return s.deleteS3Credential(ctx, requested, resolved)
}

func (s *Service) deleteS3Credential(ctx context.Context, requested string, resolved *Credential) error {
	if err := s.credentialAdmin.DeleteS3Credential(ctx, requested); err != nil {
		return err
	}
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
