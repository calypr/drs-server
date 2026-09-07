package storage

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/calypr/syfon/internal/storage/address"
)

func (m *Manager) physicalTarget(ctx context.Context, raw string) (PhysicalTarget, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return PhysicalTarget{}, false, nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return PhysicalTarget{}, false, operationError(ErrorInvalid, "", "delete", err)
	}
	if provider := address.ProviderFromScheme(parsed.Scheme); provider != "" {
		bucket := strings.TrimSpace(parsed.Host)
		key := strings.Trim(strings.TrimSpace(parsed.Path), "/")
		if bucket == "" || key == "" {
			return PhysicalTarget{}, false, nil
		}
		credential, lookupErr := m.credentials.GetS3Credential(ctx, bucket)
		if lookupErr != nil {
			return PhysicalTarget{}, false, operationError(ErrorProvider, provider, "delete", lookupErr)
		}
		if credential == nil {
			if provider == address.FileProvider {
				return PhysicalTarget{}, false, operationError(ErrorNotFound, provider, "delete", fmt.Errorf("credential not found for %q", bucket))
			}
			return PhysicalTarget{Provider: provider, Bucket: bucket, Key: key}, true, nil
		}
		provider = address.NormalizeProvider(credential.Provider, provider)
		if provider == address.FileProvider {
			root := filepath.Clean(strings.TrimSpace(credential.Endpoint))
			if root == "." || root == "" {
				root = strings.TrimPrefix(strings.TrimSpace(credential.Bucket), "/")
			}
			if root == "" {
				return PhysicalTarget{}, false, operationError(ErrorInvalid, provider, "delete", fmt.Errorf("file storage root is missing"))
			}
			return PhysicalTarget{Provider: provider, Bucket: bucket, Key: key, Path: filepath.Clean(filepath.Join(root, filepath.FromSlash(key)))}, true, nil
		}
		return PhysicalTarget{Provider: provider, Bucket: bucket, Key: key}, true, nil
	}
	if filepath.IsAbs(trimmed) {
		return PhysicalTarget{Provider: address.FileProvider, Path: filepath.Clean(trimmed)}, true, nil
	}
	return PhysicalTarget{}, false, nil
}

func physicalTargetKey(target PhysicalTarget) string {
	return target.Provider + "\x00" + target.Bucket + "\x00" + target.Key + "\x00" + target.Path
}
