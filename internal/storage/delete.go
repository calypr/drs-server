package storage

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/calypr/syfon/internal/storage/address"
)

func (m *Manager) physicalTargetWithBinding(ctx context.Context, raw string) (PhysicalTarget, ProviderBinding, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return PhysicalTarget{}, ProviderBinding{}, false, nil
	}
	parsed, err := address.ParseLocation(trimmed)
	if err != nil {
		return PhysicalTarget{}, ProviderBinding{}, false, operationError(ErrorInvalid, "", "delete", err)
	}
	if parsed.Provider != "" {
		bucket := strings.TrimSpace(parsed.Bucket)
		key := strings.Trim(strings.TrimSpace(parsed.Key), "/")
		if parsed.Provider == address.FileProvider && bucket == "" && parsed.Path != "" {
			return PhysicalTarget{Provider: address.FileProvider, Path: filepath.Clean(parsed.Path)}, ProviderBinding{Provider: address.FileProvider}, true, nil
		}
		if bucket == "" || key == "" {
			return PhysicalTarget{}, ProviderBinding{}, false, nil
		}
		credential, lookupErr := m.credentials.GetS3Credential(ctx, bucket)
		if lookupErr != nil {
			return PhysicalTarget{}, ProviderBinding{}, false, operationError(ErrorProvider, parsed.Provider, "delete", lookupErr)
		}
		if credential == nil {
			if parsed.Provider == address.FileProvider {
				return PhysicalTarget{}, ProviderBinding{}, false, operationError(ErrorNotFound, parsed.Provider, "delete", fmt.Errorf("credential not found for %q", bucket))
			}
			binding := ProviderBinding{Provider: parsed.Provider, LookupKey: bucket, PhysicalBucket: bucket}
			return PhysicalTarget{Provider: parsed.Provider, LookupKey: bucket, PhysicalBucket: bucket, Key: key}, binding, true, nil
		}
		provider := address.NormalizeProvider(credential.Provider, parsed.Provider)
		if provider == address.FileProvider {
			root := fileRootFromEndpoint(credential.Endpoint)
			if root == "." || root == "" {
				root = strings.TrimPrefix(strings.TrimSpace(credential.Bucket), "/")
			}
			if root == "" {
				return PhysicalTarget{}, ProviderBinding{}, false, operationError(ErrorInvalid, provider, "delete", fmt.Errorf("file storage root is missing"))
			}
			binding := ProviderBinding{Provider: provider, LookupKey: bucket, PhysicalBucket: bucket, Credential: credential}
			return PhysicalTarget{Provider: provider, LookupKey: bucket, PhysicalBucket: bucket, Key: key, Path: filepath.Clean(filepath.Join(root, filepath.FromSlash(key)))}, binding, true, nil
		}
		binding := ProviderBinding{Provider: provider, LookupKey: bucket, PhysicalBucket: bucket, Credential: credential}
		return PhysicalTarget{Provider: provider, LookupKey: bucket, PhysicalBucket: bucket, Key: key}, binding, true, nil
	}
	if filepath.IsAbs(trimmed) {
		return PhysicalTarget{Provider: address.FileProvider, Path: filepath.Clean(trimmed)}, ProviderBinding{Provider: address.FileProvider}, true, nil
	}
	return PhysicalTarget{}, ProviderBinding{}, false, nil
}

func fileRootFromEndpoint(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if strings.HasPrefix(strings.ToLower(endpoint), "file://") {
		u, err := url.Parse(endpoint)
		if err != nil || (u.Host != "" && u.Host != "localhost") {
			return ""
		}
		return filepath.Clean(u.Path)
	}
	return filepath.Clean(endpoint)
}

func physicalTargetKey(target PhysicalTarget) string {
	return target.Provider + "\x00" + target.LookupKey + "\x00" + target.PhysicalBucket + "\x00" + target.Key + "\x00" + target.Path
}
