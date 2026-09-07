package storage

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/calypr/syfon/internal/storage/address"
)

type Manager struct {
	credentials CredentialLookup
	providers   map[string]Registration
	order       []Registration
}

func NewManager(credentials CredentialLookup, registrations ...Registration) (*Manager, error) {
	if isNilInterface(credentials) {
		return nil, fmt.Errorf("storage credential lookup is required")
	}

	providers := make(map[string]Registration, len(registrations))
	order := make([]Registration, 0, len(registrations))
	for _, registration := range registrations {
		provider, err := canonicalProvider(registration.provider)
		if err != nil {
			return nil, err
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

func canonicalProvider(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("storage provider is required")
	}
	provider, err := address.ParseBucketProvider(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid storage provider %q: %w", raw, err)
	}
	return provider, nil
}

func (m *Manager) Access(ctx context.Context, request AccessRequest) (Access, error) {
	bucket, key, provider, err := m.resolveAccess(ctx, request.Target)
	if err != nil {
		return Access{}, err
	}
	registration, err := m.registration(provider, "access")
	if err != nil {
		return Access{}, err
	}
	target := ObjectTarget{Bucket: bucket, Key: key}
	if request.Range != nil {
		return registration.complete.SignDownloadPart(ctx, target, *request.Range, request.Options)
	}
	return registration.complete.SignURL(ctx, target, request.Options)
}

func (m *Manager) BeginMultipart(ctx context.Context, target ObjectTarget) (UploadID, error) {
	registration, err := m.registrationForBucket(ctx, target.Bucket, "multipart")
	if err != nil {
		return "", err
	}
	return registration.complete.InitMultipartUpload(ctx, target)
}

func (m *Manager) AccessMultipartPart(ctx context.Context, request MultipartPartRequest) (Access, error) {
	registration, err := m.registrationForBucket(ctx, request.Target.Bucket, "multipart")
	if err != nil {
		return Access{}, err
	}
	return registration.complete.SignMultipartPart(ctx, request)
}

func (m *Manager) CompleteMultipart(ctx context.Context, request CompleteMultipartRequest) error {
	registration, err := m.registrationForBucket(ctx, request.Target.Bucket, "multipart")
	if err != nil {
		return err
	}
	return registration.complete.CompleteMultipartUpload(ctx, request)
}

func (m *Manager) Probe(ctx context.Context, targets []ProbeTarget) []ProbeResult {
	if len(targets) == 0 {
		return nil
	}
	results := make([]ProbeResult, len(targets))
	groups := make(map[string][]probeIndex)
	providerOrder := make([]string, 0)
	for index, target := range targets {
		results[index] = ProbeResult{ID: target.ID, Target: target.Target}
		provider, err := m.providerForBucket(ctx, target.Target.Bucket, "probe")
		if err != nil {
			results[index].Err = err
			continue
		}
		if _, ok := m.providers[provider]; !ok {
			results[index].Err = operationError(ErrorProvider, provider, "probe", nil)
			continue
		}
		if _, exists := groups[provider]; !exists {
			providerOrder = append(providerOrder, provider)
		}
		groups[provider] = append(groups[provider], probeIndex{index: index, target: target})
	}

	for _, provider := range providerOrder {
		registration := m.providers[provider]
		indexes := groups[provider]
		if registration.prober == nil {
			for _, item := range indexes {
				results[item.index].Err = operationError(ErrorUnsupported, provider, "probe", nil)
			}
			continue
		}
		batch := make([]ProbeTarget, len(indexes))
		for index, item := range indexes {
			batch[index] = item.target
		}
		provided := registration.prober.Probe(ctx, batch)
		for index, item := range indexes {
			if index >= len(provided) {
				results[item.index].Err = operationError(ErrorProvider, provider, "probe", fmt.Errorf("backend returned %d results for %d targets", len(provided), len(batch)))
				continue
			}
			results[item.index].Metadata = provided[index].Metadata
			results[item.index].Err = provided[index].Err
		}
	}
	return results
}

func (m *Manager) Inventory(ctx context.Context, request InventoryRequest) (InventoryResult, error) {
	registration, err := m.registrationForBucket(ctx, request.Target.Bucket, "inventory")
	if err != nil {
		return InventoryResult{}, err
	}
	if registration.inventory == nil {
		return InventoryResult{}, operationError(ErrorUnsupported, registration.provider, "inventory", nil)
	}
	return registration.inventory.Inventory(ctx, request)
}

func (m *Manager) DeleteExact(ctx context.Context, targets []DeleteTarget) error {
	if len(targets) == 0 {
		return nil
	}
	resolved := make([]PhysicalTarget, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		physical, ok, err := m.physicalTarget(ctx, target.Location)
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
	}

	var s3TargetsByBucket map[string][]PhysicalTarget
	for _, physical := range resolved {
		if physical.Provider == address.S3Provider {
			if s3TargetsByBucket == nil {
				s3TargetsByBucket = make(map[string][]PhysicalTarget)
			}
			s3TargetsByBucket[physical.Bucket] = append(s3TargetsByBucket[physical.Bucket], physical)
			continue
		}
		if err := m.deleteTargets(ctx, physical.Provider, []PhysicalTarget{physical}); err != nil {
			return err
		}
	}

	buckets := make([]string, 0, len(s3TargetsByBucket))
	for bucket := range s3TargetsByBucket {
		buckets = append(buckets, bucket)
	}
	sort.Strings(buckets)
	for _, bucket := range buckets {
		targets := s3TargetsByBucket[bucket]
		sort.Slice(targets, func(i, j int) bool {
			return targets[i].Key < targets[j].Key
		})
		if err := m.deleteTargets(ctx, address.S3Provider, targets); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) deleteTargets(ctx context.Context, provider string, targets []PhysicalTarget) error {
	registration, err := m.registration(provider, "delete")
	if err != nil {
		return err
	}
	if registration.deleter == nil {
		return operationError(ErrorUnsupported, provider, "delete", nil)
	}
	if err := registration.deleter.Delete(ctx, targets); err != nil {
		return err
	}
	return nil
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

func (m *Manager) resolveAccess(ctx context.Context, target AccessTarget) (string, string, string, error) {
	parsed, err := url.Parse(target.Location)
	if err != nil {
		return "", "", "", operationError(ErrorInvalid, "", "access", err)
	}
	bucket := parsed.Host
	key := strings.TrimPrefix(parsed.Path, "/")
	for _, candidate := range []string{bucket, target.AccessID} {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		provider, lookupErr := m.providerForBucket(ctx, candidate, "access")
		if lookupErr == nil {
			return bucket, key, provider, nil
		}
	}
	provider := address.NormalizeProvider(address.ProviderFromScheme(parsed.Scheme), address.S3Provider)
	return bucket, key, provider, nil
}

func (m *Manager) registrationForBucket(ctx context.Context, bucket, capability string) (Registration, error) {
	provider, err := m.providerForBucket(ctx, bucket, capability)
	if err != nil {
		return Registration{}, err
	}
	return m.registration(provider, capability)
}

func (m *Manager) providerForBucket(ctx context.Context, bucket, capability string) (string, error) {
	trimmed := strings.TrimSpace(bucket)
	if trimmed == "" {
		return "", operationError(ErrorInvalid, "", capability, fmt.Errorf("bucket is required"))
	}
	credential, err := m.credentials.GetS3Credential(ctx, trimmed)
	if err != nil {
		return "", operationError(ErrorProvider, "", capability, err)
	}
	if credential == nil {
		return "", operationError(ErrorNotFound, "", capability, fmt.Errorf("credential not found for %q", trimmed))
	}
	return address.NormalizeProvider(credential.Provider, address.S3Provider), nil
}

func (m *Manager) registration(provider, capability string) (Registration, error) {
	registration, ok := m.providers[provider]
	if !ok {
		return Registration{}, operationError(ErrorProvider, provider, capability, fmt.Errorf("provider is not registered"))
	}
	return registration, nil
}
