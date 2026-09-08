package transfers

import (
	"context"
	"fmt"
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
	target   storage.Target
	complete chan struct{}
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

func (s *Service) beginMultipartTarget(ctx context.Context, target storage.Target) (storage.UploadID, error) {
	if s == nil || s.storage == nil {
		return "", fmt.Errorf("storage multipart is not configured")
	}
	return s.storage.BeginMultipart(ctx, target)
}

func (s *Service) signMultipartPartTarget(ctx context.Context, target storage.Target, uploadID storage.UploadID, partNumber int32) (storage.SignedAccess, error) {
	if s == nil || s.storage == nil {
		return storage.SignedAccess{}, fmt.Errorf("storage multipart is not configured")
	}
	return s.storage.SignMultipartPart(ctx, storage.MultipartPartRequest{Target: target, UploadID: uploadID, PartNumber: partNumber})
}

func (s *Service) completeMultipartTarget(ctx context.Context, target storage.Target, uploadID storage.UploadID, parts []storage.CompletedPart) error {
	if s == nil || s.storage == nil {
		return fmt.Errorf("storage multipart is not configured")
	}
	return s.storage.CompleteMultipart(ctx, storage.CompleteMultipartRequest{Target: target, UploadID: uploadID, Parts: parts})
}

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
	uploadID, err := s.beginMultipartTarget(ctx, target)
	if err != nil {
		return MultipartInitResult{}, err
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
	session, err := s.multipartSession(uploadID)
	if err != nil {
		return "", err
	}
	signed, err := s.signMultipartPartTarget(ctx, session.target, storage.UploadID(uploadID), partNumber)
	if err != nil {
		return "", err
	}
	return signed.Location, nil
}

func (s *Service) CompleteMultipart(ctx context.Context, uploadID string, parts []CompletedPart) error {
	session, err := s.multipartSession(uploadID)
	if err != nil {
		return err
	}
	if err := session.acquire(ctx); err != nil {
		return err
	}
	defer session.release()
	providerParts := make([]storage.CompletedPart, len(parts))
	for i, part := range parts {
		providerParts[i] = storage.CompletedPart{ETag: part.ETag, PartNumber: part.PartNumber}
	}
	if err := s.completeMultipartTarget(ctx, session.target, storage.UploadID(uploadID), providerParts); err != nil {
		return err
	}
	s.multipartMu.Lock()
	delete(s.multipartSessions, uploadID)
	s.multipartMu.Unlock()
	return nil
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
		existing, err := s.objects.GetObjectsByChecksum(ctx, key, "read")
		if err != nil {
			return storage.Target{}, "", err
		}
		if len(existing) == 0 {
			return storage.Target{}, "", fmt.Errorf("%w: checksum-only multipart init requires an explicit guid or a project-scoped object id", errorapi.ErrInvalidInput)
		}
		guid = string(existing[0].Id)
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
		return storageTargetFromCanonical(canonical.URL, canonical), string(existing.Id), nil
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
