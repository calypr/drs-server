package transfers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
	"github.com/google/uuid"
)

type MultipartInitRequest struct {
	GUID          *string
	Key           *string
	Organization  *string
	Project       *string
	Target        *storage.Target
	Authorization *MultipartAuthorization
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

const multipartCompletionLease = time.Hour

func (s *Service) BeginMultipart(ctx context.Context, req MultipartInitRequest) (MultipartInitResult, error) {
	if s == nil || s.objects == nil {
		return MultipartInitResult{}, fmt.Errorf("transfer multipart is not configured")
	}
	var target storage.Target
	var guid string
	var authorization MultipartAuthorization
	var err error
	if req.Target != nil {
		target = *req.Target
		if req.Authorization != nil {
			authorization = *req.Authorization
		}
		guid = pointerValue(req.GUID)
		if guid == "" {
			guid = pointerValue(req.Key)
		}
	} else {
		target, guid, authorization, err = s.resolveMultipartTarget(ctx, req)
	}
	if err != nil {
		return MultipartInitResult{}, err
	}
	if s.storage == nil {
		return MultipartInitResult{}, fmt.Errorf("transfer multipart is not configured")
	}
	if err := authorization.Authorize(ctx); err != nil {
		return MultipartInitResult{}, err
	}
	completionID := uuid.NewString()
	uploadID, err := s.storage.BeginMultipart(ctx, storage.BeginMultipartRequest{Target: target, CompletionID: completionID})
	if err != nil {
		return MultipartInitResult{}, err
	}
	if strings.TrimSpace(string(uploadID)) == "" {
		return MultipartInitResult{}, fmt.Errorf("storage provider returned an empty multipart upload ID")
	}
	now := s.now().UTC()
	if err := s.multipartSessions.SaveMultipartSession(ctx, MultipartSession{UploadID: string(uploadID), CompletionID: completionID, Target: target, Authorization: authorization, State: MultipartStateActive, Operation: MultipartOperationNone, CreatedAt: now, UpdatedAt: now}); err != nil {
		persistErr := fmt.Errorf("persist multipart upload %s: %w", uploadID, err)
		if aborter, ok := s.storage.(MultipartAborter); ok {
			if abortErr := aborter.AbortMultipart(ctx, storage.AbortMultipartRequest{Target: target, UploadID: uploadID, CompletionID: completionID}); abortErr != nil {
				return MultipartInitResult{}, errors.Join(persistErr, fmt.Errorf("abort unpersisted multipart upload %s: %w", uploadID, abortErr))
			}
		}
		return MultipartInitResult{}, persistErr
	}
	return MultipartInitResult{UploadID: string(uploadID), GUID: guid}, nil
}

func (s *Service) SignMultipartPart(ctx context.Context, uploadID string, partNumber int32) (string, error) {
	if partNumber <= 0 {
		return "", fmt.Errorf("%w: multipart part number must be positive", errorapi.ErrInvalidInput)
	}
	session, err := s.multipartSessions.GetMultipartSession(ctx, uploadID)
	if err != nil {
		return "", err
	}
	if err := session.Authorization.Authorize(ctx); err != nil {
		return "", err
	}
	if session.State != MultipartStateActive {
		return "", multipartNotFound(uploadID)
	}
	signed, err := s.storage.SignMultipartPart(ctx, storage.MultipartPartRequest{Target: session.Target, UploadID: storage.UploadID(uploadID), PartNumber: partNumber, ExpiresIn: s.signingExpiry})
	if err != nil {
		return "", err
	}
	if toucher, ok := s.multipartSessions.(MultipartActivityStore); ok {
		if err := toucher.TouchMultipartSession(ctx, uploadID, s.now().UTC()); err != nil {
			return "", err
		}
	}
	return signed.Location, nil
}

func (s *Service) CompleteMultipart(ctx context.Context, uploadID string, parts []CompletedPart) (string, error) {
	providerParts, err := normalizeCompletedParts(parts)
	if err != nil {
		return "", err
	}
	partsFingerprint := multipartPartsFingerprint(providerParts)
	session, err := s.multipartSessions.GetMultipartSession(ctx, uploadID)
	if err != nil {
		if errors.Is(err, errorapi.ErrMultipartUploadNotFound) {
			return s.replayCompletedMultipartReceipt(ctx, uploadID, partsFingerprint)
		}
		return "", err
	}
	if err := session.Authorization.Authorize(ctx); err != nil {
		return "", err
	}
	if session.PartsFingerprint != "" && session.PartsFingerprint != partsFingerprint {
		return "", fmt.Errorf("%w: multipart completion parts differ from the first completion request", errorapi.ErrConflict)
	}
	if session.State == MultipartStateCompleted {
		return session.CompletedLocation, nil
	}
	now := s.now().UTC()
	token := uuid.NewString()
	var claimed bool
	if durable, ok := s.multipartSessions.(MultipartCompletionPartsClaimer); ok {
		session, claimed, err = durable.ClaimMultipartCompletionWithParts(ctx, uploadID, token, partsFingerprint, completedPartsForSession(providerParts), now, now.Add(-multipartCompletionLease))
	} else {
		session, claimed, err = s.multipartSessions.ClaimMultipartCompletion(ctx, uploadID, token, partsFingerprint, now, now.Add(-multipartCompletionLease))
	}
	if err != nil {
		return "", err
	}
	if !claimed {
		if session.State == MultipartStateCompleted {
			if session.PartsFingerprint != partsFingerprint {
				return "", fmt.Errorf("%w: multipart completion parts differ from the completed request", errorapi.ErrConflict)
			}
			return session.CompletedLocation, nil
		}
		return "", fmt.Errorf("%w: multipart upload completion is already in progress", errorapi.ErrConflict)
	}
	if err := s.storage.CompleteMultipart(ctx, storage.CompleteMultipartRequest{Target: session.Target, UploadID: storage.UploadID(uploadID), CompletionID: session.CompletionID, Parts: providerParts}); err != nil {
		if !errors.Is(err, storage.ErrMultipartCompletionIndeterminate) {
			_ = s.multipartSessions.ReleaseMultipartCompletion(ctx, uploadID, token, s.now().UTC())
		}
		return "", err
	}
	location := session.Target.CanonicalURL
	finished, err := s.multipartSessions.FinishMultipartCompletion(ctx, uploadID, token, location, s.now().UTC())
	if err != nil {
		return "", fmt.Errorf("persist completed multipart upload %s: %w", uploadID, err)
	}
	if !finished {
		current, reloadErr := s.multipartSessions.GetMultipartSession(ctx, uploadID)
		if reloadErr != nil {
			return "", fmt.Errorf("reload multipart upload %s after completion race: %w", uploadID, reloadErr)
		}
		if current.State == MultipartStateCompleted && current.CompletionID == session.CompletionID && current.PartsFingerprint == partsFingerprint {
			return current.CompletedLocation, nil
		}
		return "", fmt.Errorf("%w: multipart upload changed while completion was in progress", errorapi.ErrConflict)
	}
	return location, nil
}

func (s *Service) replayCompletedMultipartReceipt(ctx context.Context, uploadID, partsFingerprint string) (string, error) {
	receipts, ok := s.multipartSessions.(MultipartCompletionReceiptStore)
	if !ok {
		return "", multipartNotFound(uploadID)
	}
	receipt, err := receipts.GetMultipartCompletionReceipt(ctx, uploadID)
	if err != nil {
		return "", err
	}
	if err := receipt.Authorization.Authorize(ctx); err != nil {
		return "", err
	}
	if receipt.PartsFingerprint != partsFingerprint {
		return "", fmt.Errorf("%w: multipart completion parts differ from the completed request", errorapi.ErrConflict)
	}
	return receipt.CompletedLocation, nil
}

func completedPartsForSession(parts []storage.CompletedPart) []CompletedPart {
	result := make([]CompletedPart, len(parts))
	for i, part := range parts {
		result[i] = CompletedPart{ETag: part.ETag, PartNumber: part.PartNumber}
	}
	return result
}

// AbortMultipart is an additive, idempotent operation. Authorization is
// evaluated before a durable abort lease is claimed, while the provider call
// itself runs under the claimed session state.
func (s *Service) AbortMultipart(ctx context.Context, uploadID string) error {
	uploadID = strings.TrimSpace(uploadID)
	if uploadID == "" {
		return fmt.Errorf("%w: upload ID is required", errorapi.ErrInvalidInput)
	}
	session, err := s.multipartSessions.GetMultipartSession(ctx, uploadID)
	if err != nil {
		if errors.Is(err, errorapi.ErrMultipartUploadNotFound) {
			return nil
		}
		return err
	}
	if err := session.Authorization.Authorize(ctx); err != nil {
		return err
	}
	if session.State == MultipartStateCompleted {
		return nil
	}
	claimer, ok := s.multipartSessions.(MultipartAbortStore)
	if !ok {
		return fmt.Errorf("multipart abort persistence is not configured")
	}
	aborter, ok := s.storage.(MultipartAborter)
	if !ok {
		return fmt.Errorf("multipart abort is not supported by the storage provider")
	}
	now := s.now().UTC()
	token := uuid.NewString()
	claimedSession, claimed, err := claimer.ClaimMultipartAbort(ctx, uploadID, token, now, now.Add(-multipartCompletionLease))
	if err != nil {
		if errors.Is(err, errorapi.ErrMultipartUploadNotFound) {
			return nil
		}
		return err
	}
	if !claimed {
		if claimedSession.State == MultipartStateCompleted {
			return nil
		}
		return fmt.Errorf("%w: multipart upload operation is already in progress", errorapi.ErrConflict)
	}
	if err := aborter.AbortMultipart(ctx, storage.AbortMultipartRequest{Target: claimedSession.Target, UploadID: storage.UploadID(uploadID), CompletionID: claimedSession.CompletionID}); err != nil {
		return err
	}
	finished, err := claimer.FinishMultipartAbort(ctx, uploadID, token, s.now().UTC())
	if err != nil {
		return fmt.Errorf("persist aborted multipart upload %s: %w", uploadID, err)
	}
	if !finished {
		return fmt.Errorf("%w: multipart upload changed while abort was in progress", errorapi.ErrConflict)
	}
	return nil
}

func (s *Service) ReconcileMultipartSessions(ctx context.Context, inactiveTimeout, completedRetention time.Duration, batchSize int) error {
	store, ok := s.multipartSessions.(MultipartReconcilerStore)
	if !ok || batchSize <= 0 {
		return nil
	}
	now := s.now().UTC()
	sessions, err := store.ListMultipartSessionsForReconcile(ctx, now.Add(-inactiveTimeout), now.Add(-completedRetention), batchSize)
	if err != nil {
		return err
	}
	var reconcileErrors []error
	for _, session := range sessions {
		var operationErr error
		switch {
		case session.State == MultipartStateCompleted:
			// Completed receipts are catalog state only. The provider object is
			// already committed and must never be deleted by this sweep.
			operationErr = nil
		case session.Operation == MultipartOperationComplete && len(session.CompletionParts) > 0:
			operationErr = s.replayMultipartCompletion(ctx, session)
		case session.State == MultipartStateActive || session.Operation == MultipartOperationAbort:
			operationErr = s.AbortMultipart(ctx, session.UploadID)
		default:
			continue
		}
		if operationErr != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile multipart session %s: %w", session.UploadID, operationErr))
		}
	}
	if err := store.CompactCompletedMultipartSessions(ctx, now.Add(-completedRetention), batchSize); err != nil {
		reconcileErrors = append(reconcileErrors, err)
	}
	return errors.Join(reconcileErrors...)
}

func (s *Service) replayMultipartCompletion(ctx context.Context, session MultipartSession) error {
	parts := append([]CompletedPart(nil), session.CompletionParts...)
	_, err := s.CompleteMultipart(ctx, session.UploadID, parts)
	return err
}

func multipartPartsFingerprint(parts []storage.CompletedPart) string {
	encoded, _ := json.Marshal(parts)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
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

func (s *Service) resolveMultipartTarget(ctx context.Context, req MultipartInitRequest) (storage.Target, string, MultipartAuthorization, error) {
	key := pointerValue(req.GUID)
	if key == "" {
		key = pointerValue(req.Key)
	}
	if key == "" {
		return storage.Target{}, "", MultipartAuthorization{}, fmt.Errorf("%w: key/guid is required", errorapi.ErrInvalidInput)
	}
	guid := key
	if strings.Contains(key, "/") {
		guid = uuid.NewString()
		return s.resolveScopedMultipartTarget(ctx, req, guid, key)
	}
	if objects.LooksLikeSHA256(key) {
		byChecksum, err := s.objects.GetObjectsByChecksums(ctx, []string{key}, "update")
		if err != nil {
			return storage.Target{}, "", MultipartAuthorization{}, err
		}
		existing := byChecksum[key]
		if len(existing) == 0 {
			return storage.Target{}, "", MultipartAuthorization{}, fmt.Errorf("%w: checksum-only multipart init requires an explicit guid or a project-scoped object id", errorapi.ErrInvalidInput)
		}
		guid = existing[0].Id
		canonical, err := s.ResolveCanonicalStorageTarget(ctx, CanonicalStorageTargetRequest{Object: &existing[0], PreferChecksum: true})
		if err != nil {
			return storage.Target{}, "", MultipartAuthorization{}, err
		}
		return storageTargetFromCanonical(canonical.URL, canonical), guid, objectMultipartAuthorization(&existing[0]), nil
	}
	if existing, err := s.objects.GetObject(ctx, key, "update"); err == nil {
		canonical, targetErr := s.ResolveCanonicalStorageTarget(ctx, CanonicalStorageTargetRequest{Object: existing, PreferChecksum: true})
		if targetErr != nil {
			return storage.Target{}, "", MultipartAuthorization{}, targetErr
		}
		return storageTargetFromCanonical(canonical.URL, canonical), existing.Id, objectMultipartAuthorization(existing), nil
	} else if !isNotFound(err) {
		return storage.Target{}, "", MultipartAuthorization{}, err
	}
	if _, err := uuid.Parse(key); err != nil {
		guid = uuid.NewString()
	}
	return s.resolveScopedMultipartTarget(ctx, req, guid, key)
}

func (s *Service) resolveScopedMultipartTarget(ctx context.Context, req MultipartInitRequest, guid, key string) (storage.Target, string, MultipartAuthorization, error) {
	organization := pointerValue(req.Organization)
	project := pointerValue(req.Project)
	if organization == "" {
		return storage.Target{}, "", MultipartAuthorization{}, fmt.Errorf("%w: organization is required", errorapi.ErrInvalidInput)
	}
	scope := &AccessScope{Organization: organization, Project: project}
	authorization := MultipartAuthorization{Scope: scope, Methods: []string{"file_upload", "create", "update"}}
	if err := access.AuthorizeScopeWrite(ctx, organization, project, "file_upload", "create", "update"); err != nil {
		return storage.Target{}, "", MultipartAuthorization{}, err
	}
	canonical, err := s.ResolveScopedUploadTarget(ctx, organization, project, key)
	if err != nil {
		return storage.Target{}, "", MultipartAuthorization{}, err
	}
	return storageTargetFromCanonical(canonical.URL, canonical), guid, authorization, nil
}

func objectMultipartAuthorization(object *drs.DrsObject) MultipartAuthorization {
	return MultipartAuthorization{Resources: objects.AccessResources(object), Methods: []string{"update"}}
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
