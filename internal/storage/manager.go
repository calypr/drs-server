package storage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/storage/address"
)

// CredentialLookup reads one configured bucket credential by identifier or
// physical bucket name.
type CredentialLookup interface {
	GetS3Credential(ctx context.Context, bucket string) (*buckets.Credential, error)
}

type Manager struct {
	credentials CredentialLookup
	providers   map[string]Registration
	order       []Registration
	closeOnce   sync.Once
	closeErr    error
}

func NewManager(credentials CredentialLookup, registrations ...Registration) (*Manager, error) {
	if isNilInterface(credentials) {
		return nil, fmt.Errorf("storage credential lookup is required")
	}
	providers := make(map[string]Registration, len(registrations))
	order := make([]Registration, 0, len(registrations))
	for _, registration := range registrations {
		trimmed := strings.TrimSpace(registration.provider)
		if trimmed == "" {
			return nil, fmt.Errorf("storage provider is required")
		}
		provider, err := address.ParseBucketProvider(trimmed)
		if err != nil {
			return nil, fmt.Errorf("invalid storage provider %q: %w", registration.provider, err)
		}
		if isNilInterface(registration.complete) {
			return nil, fmt.Errorf("storage provider %q has no backend", provider)
		}
		if _, exists := providers[provider]; exists {
			return nil, fmt.Errorf("storage provider %q is registered more than once", provider)
		}
		registration.provider = provider
		providers[provider] = registration
		order = append(order, registration)
	}
	return &Manager{credentials: credentials, providers: providers, order: order}, nil
}

// Close releases resources owned by the manager. Registrations are closed in
// construction order and repeated calls return the same aggregate result.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.closeOnce.Do(func() {
		closeErrors := make([]error, 0)
		for _, registration := range m.order {
			if registration.closer == nil {
				continue
			}
			if err := registration.closer.Close(); err != nil {
				closeErrors = append(closeErrors, err)
			}
		}
		m.closeErr = errors.Join(closeErrors...)
	})
	return m.closeErr
}

// Sign resolves one credential binding and passes it to the provider.
func (m *Manager) Sign(ctx context.Context, request SignRequest) (SignedAccess, error) {
	binding, target, err := m.resolveTarget(ctx, request.Target, "access", true)
	if err != nil {
		return SignedAccess{}, err
	}
	registration, err := m.registration(binding.Provider, "access")
	if err != nil {
		return SignedAccess{}, err
	}
	request.Target = target
	access, err := registration.complete.Sign(ctx, binding, request)
	return access, wrapProviderError(err, binding.Provider, "access")
}

func (m *Manager) BeginMultipart(ctx context.Context, request BeginMultipartRequest) (UploadID, error) {
	binding, target, err := m.resolveTarget(ctx, request.Target, "multipart", false)
	if err != nil {
		return "", err
	}
	registration, err := m.registration(binding.Provider, "multipart")
	if err != nil {
		return "", err
	}
	request.Target = target
	uploadID, err := registration.complete.BeginMultipart(ctx, binding, request)
	return uploadID, wrapProviderError(err, binding.Provider, "multipart")
}

func (m *Manager) SignMultipartPart(ctx context.Context, request MultipartPartRequest) (SignedAccess, error) {
	binding, target, err := m.resolveTarget(ctx, request.Target, "multipart", false)
	if err != nil {
		return SignedAccess{}, err
	}
	registration, err := m.registration(binding.Provider, "multipart")
	if err != nil {
		return SignedAccess{}, err
	}
	request.Target = target
	access, err := registration.complete.SignMultipartPart(ctx, binding, request)
	return access, wrapProviderError(err, binding.Provider, "multipart")
}

func (m *Manager) CompleteMultipart(ctx context.Context, request CompleteMultipartRequest) error {
	binding, target, err := m.resolveTarget(ctx, request.Target, "multipart", false)
	if err != nil {
		return err
	}
	registration, err := m.registration(binding.Provider, "multipart")
	if err != nil {
		return err
	}
	request.Target = target
	return wrapProviderError(registration.complete.CompleteMultipart(ctx, binding, request), binding.Provider, "multipart")
}

// AbortMultipart asks a provider to discard an in-progress multipart upload.
// Providers that cannot expose an abort API are reported as unsupported and
// the durable session remains available for a later retry.
func (m *Manager) AbortMultipart(ctx context.Context, request AbortMultipartRequest) error {
	binding, target, err := m.resolveTarget(ctx, request.Target, "multipart", false)
	if err != nil {
		return err
	}
	registration, err := m.registration(binding.Provider, "multipart abort")
	if err != nil {
		return err
	}
	if registration.aborter == nil {
		return operationError(ErrorUnsupported, binding.Provider, "multipart abort", nil)
	}
	request.Target = target
	return wrapProviderError(registration.aborter.AbortMultipart(ctx, binding, request), binding.Provider, "multipart abort")
}

func (m *Manager) Probe(ctx context.Context, targets []ProbeTarget) []ProbeResult {
	if len(targets) == 0 {
		return nil
	}
	results := make([]ProbeResult, len(targets))
	type groupKey struct{ provider, lookupKey, physicalBucket string }
	groups := make(map[groupKey]*probeGroup)
	order := make([]groupKey, 0)
	for index, target := range targets {
		results[index] = ProbeResult{ID: target.ID, Target: target.Target}
		binding, resolved, err := m.resolveTarget(ctx, target.Target, "probe", false)
		if err != nil {
			results[index].Err = err
			continue
		}
		target.Target = resolved
		key := groupKey{binding.Provider, binding.LookupKey, binding.PhysicalBucket}
		if _, exists := groups[key]; !exists {
			order = append(order, key)
			groups[key] = &probeGroup{binding: binding}
		}
		groups[key].items = append(groups[key].items, probeIndex{index: index, target: target})
	}
	for _, key := range order {
		registration := m.providers[key.provider]
		group := groups[key]
		indexes := group.items
		if registration.prober == nil {
			for _, item := range indexes {
				results[item.index].Err = operationError(ErrorUnsupported, key.provider, "probe", nil)
			}
			continue
		}
		batch := make([]ProbeTarget, len(indexes))
		for index, item := range indexes {
			batch[index] = item.target
		}
		provided := registration.prober.Probe(ctx, group.binding, batch)
		for index, item := range indexes {
			if index >= len(provided) {
				results[item.index].Err = operationError(ErrorProvider, key.provider, "probe", fmt.Errorf("backend returned %d results for %d targets", len(provided), len(batch)))
				continue
			}
			results[item.index].Metadata = provided[index].Metadata
			results[item.index].Err = wrapProviderError(provided[index].Err, key.provider, "probe")
		}
	}
	return results
}

func (m *Manager) Inventory(ctx context.Context, request InventoryRequest) (InventoryResult, error) {
	binding, target, err := m.resolveTarget(ctx, request.Target, "inventory", false)
	if err != nil {
		return InventoryResult{}, err
	}
	registration, err := m.registration(binding.Provider, "inventory")
	if err != nil {
		return InventoryResult{}, err
	}
	if registration.inventory == nil {
		return InventoryResult{}, operationError(ErrorUnsupported, binding.Provider, "inventory", nil)
	}
	request.Target = target
	result, err := registration.inventory.Inventory(ctx, binding, request)
	return result, wrapProviderError(err, binding.Provider, "inventory")
}

func (m *Manager) DeleteExact(ctx context.Context, targets []DeleteTarget) error {
	if len(targets) == 0 {
		return nil
	}
	resolved := make([]PhysicalTarget, 0, len(targets))
	bindings := make(map[string]ProviderBinding, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		physical, binding, ok, err := m.physicalTargetWithBinding(ctx, target.Location)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		key := physicalTargetKey(physical)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		resolved = append(resolved, physical)
		bindings[key] = binding
	}
	type groupKey struct{ provider, lookupKey, physicalBucket string }
	groups := make(map[groupKey][]PhysicalTarget)
	for _, physical := range resolved {
		key := groupKey{physical.Provider, physical.LookupKey, physical.PhysicalBucket}
		groups[key] = append(groups[key], physical)
	}
	keys := make([]groupKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].provider != keys[j].provider {
			return keys[i].provider < keys[j].provider
		}
		if keys[i].lookupKey != keys[j].lookupKey {
			return keys[i].lookupKey < keys[j].lookupKey
		}
		return keys[i].physicalBucket < keys[j].physicalBucket
	})
	for _, key := range keys {
		targets := groups[key]
		sort.Slice(targets, func(i, j int) bool {
			if targets[i].PhysicalBucket != targets[j].PhysicalBucket {
				return targets[i].PhysicalBucket < targets[j].PhysicalBucket
			}
			return targets[i].Key < targets[j].Key
		})
		if err := m.deleteTargets(ctx, bindings[physicalTargetKey(targets[0])], targets); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) deleteTargets(ctx context.Context, binding ProviderBinding, targets []PhysicalTarget) error {
	registration, err := m.registration(binding.Provider, "delete")
	if err != nil {
		return err
	}
	if registration.deleter == nil {
		return operationError(ErrorUnsupported, binding.Provider, "delete", nil)
	}
	return wrapProviderError(registration.deleter.Delete(ctx, binding, targets), binding.Provider, "delete")
}

func wrapProviderError(err error, provider, capability string) error {
	if err == nil {
		return nil
	}
	var operation *OperationError
	if errors.As(err, &operation) {
		return err
	}
	return operationError(ErrorProvider, provider, capability, err)
}

func (m *Manager) InvalidateBucket(bucket string) {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return
	}
	for _, registration := range m.order {
		if registration.invalidator != nil {
			registration.invalidator.InvalidateBucket(bucket)
		}
	}
}

type probeIndex struct {
	index  int
	target ProbeTarget
}

type probeGroup struct {
	binding ProviderBinding
	items   []probeIndex
}

func (m *Manager) resolveTarget(ctx context.Context, target Target, capability string, allowMissing bool) (ProviderBinding, Target, error) {
	resolved := target
	if strings.TrimSpace(resolved.OriginalURL) != "" {
		parsed, err := address.ParseLocation(resolved.OriginalURL)
		if err != nil {
			return ProviderBinding{}, Target{}, operationError(ErrorInvalid, "", capability, err)
		}
		if resolved.PhysicalBucket == "" {
			resolved.PhysicalBucket = parsed.Bucket
		}
		if resolved.Key == "" {
			resolved.Key = parsed.Key
		}
		if resolved.Provider == "" {
			resolved.Provider = parsed.Provider
		}
		if resolved.Path == "" {
			resolved.Path = parsed.Path
		}
	}
	candidates := append([]string(nil), resolved.LookupCandidates...)
	if strings.TrimSpace(resolved.PhysicalBucket) != "" {
		candidates = append(candidates, resolved.PhysicalBucket)
	}
	if strings.TrimSpace(resolved.LookupKey) != "" {
		candidates = append(candidates, resolved.LookupKey)
	}
	candidates = uniqueStrings(candidates)
	var lastErr error
	for _, candidate := range candidates {
		credential, err := m.credentials.GetS3Credential(ctx, candidate)
		if err != nil {
			lastErr = operationError(ErrorProvider, resolved.Provider, capability, err)
			continue
		}
		if credential == nil {
			lastErr = operationError(ErrorNotFound, resolved.Provider, capability, fmt.Errorf("credential not found for %q", candidate))
			continue
		}
		provider := address.NormalizeProvider(credential.Provider, resolved.Provider)
		if provider == "" {
			provider = address.S3Provider
		}
		resolved.Provider = provider
		resolved.LookupKey = candidate
		if resolved.PhysicalBucket == "" {
			resolved.PhysicalBucket = strings.TrimSpace(credential.Bucket)
		}
		return ProviderBinding{Provider: provider, LookupKey: candidate, PhysicalBucket: resolved.PhysicalBucket, Credential: credential}, resolved, nil
	}
	provider := address.NormalizeProvider(resolved.Provider, address.S3Provider)
	if allowMissing && provider != address.FileProvider {
		resolved.Provider = provider
		return ProviderBinding{Provider: provider, LookupKey: resolved.LookupKey, PhysicalBucket: resolved.PhysicalBucket}, resolved, nil
	}
	if lastErr != nil {
		return ProviderBinding{}, Target{}, lastErr
	}
	if provider == address.FileProvider && resolved.Path != "" {
		resolved.Provider = provider
		return ProviderBinding{Provider: provider, LookupKey: resolved.LookupKey, PhysicalBucket: resolved.PhysicalBucket}, resolved, nil
	}
	return ProviderBinding{}, Target{}, operationError(ErrorInvalid, provider, capability, fmt.Errorf("storage credential is required"))
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (m *Manager) registration(provider, capability string) (Registration, error) {
	registration, ok := m.providers[provider]
	if !ok {
		return Registration{}, operationError(ErrorProvider, provider, capability, fmt.Errorf("provider is not registered"))
	}
	return registration, nil
}
