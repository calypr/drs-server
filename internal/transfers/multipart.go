package transfers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
	"github.com/google/uuid"
)

type MultipartInitRequest struct {
	GUID         *string
	Key          *string
	Organization *string
	Project      *string
	Target       *storage.Target
}

type MultipartInitResult struct {
	UploadID string
	GUID     string
}

// CompletedPart carries multipart completion metadata across the transfer
// boundary. Provider-specific part values are created only by Service.
type CompletedPart struct {
	ETag       string
	PartNumber int32
}

type multipartSession struct {
	target    storage.Target
	complete  chan struct{}
	completed bool
}

func newMultipartSession(target storage.Target) *multipartSession {
	ready := make(chan struct{}, 1)
	ready <- struct{}{}
	return &multipartSession{target: target, complete: ready}
}

func (s *multipartSession) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-s.complete:
		if err := ctx.Err(); err != nil {
			s.release()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *multipartSession) release() { s.complete <- struct{}{} }

func (s *Service) BeginMultipart(ctx context.Context, req MultipartInitRequest) (MultipartInitResult, error) {
	if s == nil || s.objects == nil {
		return MultipartInitResult{}, fmt.Errorf("transfer multipart is not configured")
	}
	var target storage.Target
	var guid string
	var err error
	if req.Target != nil {
		target = *req.Target
		guid = pointerValue(req.GUID)
		if guid == "" {
			guid = pointerValue(req.Key)
		}
	} else {
		target, guid, err = s.resolveMultipartTarget(ctx, req)
	}
	if err != nil {
		return MultipartInitResult{}, err
	}
	if s.storage == nil {
		return MultipartInitResult{}, fmt.Errorf("transfer multipart is not configured")
	}
	uploadID, err := s.storage.BeginMultipart(ctx, target)
	if err != nil {
		return MultipartInitResult{}, err
	}
	if strings.TrimSpace(string(uploadID)) == "" {
		return MultipartInitResult{}, fmt.Errorf("storage provider returned an empty multipart upload ID")
	}
	s.multipartMu.Lock()
	if s.multipartSessions == nil {
		s.multipartSessions = make(map[string]*multipartSession)
	}
	s.multipartSessions[string(uploadID)] = newMultipartSession(target)
	s.multipartMu.Unlock()
	return MultipartInitResult{UploadID: string(uploadID), GUID: guid}, nil
}

func (s *Service) SignMultipartPart(ctx context.Context, uploadID string, partNumber int32) (string, error) {
	if partNumber <= 0 {
		return "", fmt.Errorf("%w: multipart part number must be positive", errorapi.ErrInvalidInput)
	}
	session, err := s.multipartSession(uploadID)
	if err != nil {
		return "", err
	}
	signed, err := s.storage.SignMultipartPart(ctx, storage.MultipartPartRequest{Target: session.target, UploadID: storage.UploadID(uploadID), PartNumber: partNumber, ExpiresIn: s.signingExpiry})
	if err != nil {
		return "", err
	}
	return signed.Location, nil
}

func (s *Service) CompleteMultipart(ctx context.Context, uploadID string, parts []CompletedPart) error {
	providerParts, err := normalizeCompletedParts(parts)
	if err != nil {
		return err
	}
	session, err := s.multipartSession(uploadID)
	if err != nil {
		return err
	}
	if err := session.acquire(ctx); err != nil {
		return err
	}
	defer session.release()
	if session.completed {
		return fmt.Errorf("%w: %s", errorapi.ErrMultipartUploadNotFound, uploadID)
	}
	if err := s.storage.CompleteMultipart(ctx, storage.CompleteMultipartRequest{Target: session.target, UploadID: storage.UploadID(uploadID), Parts: providerParts}); err != nil {
		return err
	}
	session.completed = true
	s.multipartMu.Lock()
	if s.multipartSessions[uploadID] == session {
		delete(s.multipartSessions, uploadID)
	}
	s.multipartMu.Unlock()
	return nil
}

func normalizeCompletedParts(parts []CompletedPart) ([]storage.CompletedPart, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("%w: multipart complete requires at least one part", errorapi.ErrInvalidInput)
	}
	seen := make(map[int32]struct{}, len(parts))
	normalized := make([]storage.CompletedPart, len(parts))
	for i, part := range parts {
		if part.PartNumber <= 0 {
			return nil, fmt.Errorf("%w: multipart part number must be positive", errorapi.ErrInvalidInput)
		}
		if _, exists := seen[part.PartNumber]; exists {
			return nil, fmt.Errorf("%w: multipart part number %d is duplicated", errorapi.ErrInvalidInput, part.PartNumber)
		}
		seen[part.PartNumber] = struct{}{}
		normalized[i] = storage.CompletedPart{ETag: part.ETag, PartNumber: part.PartNumber}
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].PartNumber < normalized[j].PartNumber })
	return normalized, nil
}

func (s *Service) multipartSession(uploadID string) (*multipartSession, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: %s", errorapi.ErrMultipartUploadNotFound, uploadID)
	}
	s.multipartMu.Lock()
	session := s.multipartSessions[uploadID]
	s.multipartMu.Unlock()
	if session == nil {
		return nil, fmt.Errorf("%w: %s", errorapi.ErrMultipartUploadNotFound, uploadID)
	}
	return session, nil
}

func (s *Service) resolveMultipartTarget(ctx context.Context, req MultipartInitRequest) (storage.Target, string, error) {
	key := pointerValue(req.GUID)
	if key == "" {
		key = pointerValue(req.Key)
	}
	if key == "" {
		return storage.Target{}, "", fmt.Errorf("%w: key/guid is required", errorapi.ErrInvalidInput)
	}
	guid := key
	if strings.Contains(key, "/") {
		guid = uuid.NewString()
		return s.resolveScopedMultipartTarget(ctx, req, guid, key)
	}
	if objects.LooksLikeSHA256(key) {
		byChecksum, err := s.objects.GetObjectsByChecksums(ctx, []string{key}, "read")
		if err != nil {
			return storage.Target{}, "", err
		}
		existing := byChecksum[key]
		if len(existing) == 0 {
			return storage.Target{}, "", fmt.Errorf("%w: checksum-only multipart init requires an explicit guid or a project-scoped object id", errorapi.ErrInvalidInput)
		}
		guid = existing[0].Id
		canonical, err := s.ResolveCanonicalStorageTarget(ctx, CanonicalStorageTargetRequest{Object: &existing[0], PreferChecksum: true})
		if err != nil {
			return storage.Target{}, "", err
		}
		return storageTargetFromCanonical(canonical.URL, canonical), guid, nil
	}
	if existing, err := s.objects.GetObject(ctx, key, "read"); err == nil {
		canonical, targetErr := s.ResolveCanonicalStorageTarget(ctx, CanonicalStorageTargetRequest{Object: existing, PreferChecksum: true})
		if targetErr != nil {
			return storage.Target{}, "", targetErr
		}
		return storageTargetFromCanonical(canonical.URL, canonical), existing.Id, nil
	} else if !isNotFound(err) {
		return storage.Target{}, "", err
	}
	if _, err := uuid.Parse(key); err != nil {
		guid = uuid.NewString()
	}
	return s.resolveScopedMultipartTarget(ctx, req, guid, key)
}

func (s *Service) resolveScopedMultipartTarget(ctx context.Context, req MultipartInitRequest, guid, key string) (storage.Target, string, error) {
	canonical, err := s.ResolveScopedUploadTarget(ctx, pointerValue(req.Organization), pointerValue(req.Project), key)
	if err != nil {
		return storage.Target{}, "", err
	}
	return storageTargetFromCanonical(canonical.URL, canonical), guid, nil
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
