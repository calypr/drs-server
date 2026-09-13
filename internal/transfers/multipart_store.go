package transfers

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/storage"
)

type MultipartState string

const (
	MultipartStateActive     MultipartState = "active"
	MultipartStateCompleting MultipartState = "completing"
	MultipartStateCompleted  MultipartState = "completed"
)

type MultipartOperation string

const (
	MultipartOperationNone     MultipartOperation = ""
	MultipartOperationComplete MultipartOperation = "complete"
	MultipartOperationAbort    MultipartOperation = "abort"
)

type MultipartAuthorization struct {
	Resources []string     `json:"resources,omitempty"`
	Methods   []string     `json:"methods,omitempty"`
	Scope     *AccessScope `json:"scope,omitempty"`
}

func (a MultipartAuthorization) Authorize(ctx context.Context) error {
	if !access.IsAuthzEnforced(ctx) {
		return nil
	}
	if a.Scope != nil {
		return access.AuthorizeScopeWrite(ctx, a.Scope.Organization, a.Scope.Project, a.Methods...)
	}
	for _, method := range a.Methods {
		if access.HasObjectMethodAccess(ctx, method, a.Resources) {
			return nil
		}
	}
	return errorapi.ErrAccessDenied
}

type MultipartSession struct {
	UploadID          string                 `json:"upload_id"`
	CompletionID      string                 `json:"completion_id"`
	Target            storage.Target         `json:"target"`
	Authorization     MultipartAuthorization `json:"authorization"`
	State             MultipartState         `json:"state"`
	CompletionToken   string                 `json:"completion_token,omitempty"`
	PartsFingerprint  string                 `json:"parts_fingerprint,omitempty"`
	Operation         MultipartOperation     `json:"operation,omitempty"`
	CompletionParts   []CompletedPart        `json:"completion_parts,omitempty"`
	CompletedLocation string                 `json:"completed_location,omitempty"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

type memoryMultipartSessionStore struct {
	mu       sync.Mutex
	sessions map[string]MultipartSession
	receipts map[string]MultipartCompletionReceipt
}

func newMemoryMultipartSessionStore() *memoryMultipartSessionStore {
	return &memoryMultipartSessionStore{
		sessions: make(map[string]MultipartSession),
		receipts: make(map[string]MultipartCompletionReceipt),
	}
}

func (s *memoryMultipartSessionStore) SaveMultipartSession(_ context.Context, session MultipartSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[session.UploadID]; exists {
		return fmt.Errorf("%w: multipart upload ID %s already exists", errorapi.ErrConflict, session.UploadID)
	}
	if _, exists := s.receipts[session.UploadID]; exists {
		return fmt.Errorf("%w: multipart upload ID %s already exists", errorapi.ErrConflict, session.UploadID)
	}
	s.sessions[session.UploadID] = cloneMultipartSession(session)
	return nil
}

func (s *memoryMultipartSessionStore) GetMultipartSession(_ context.Context, uploadID string) (MultipartSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[uploadID]
	if !ok {
		return MultipartSession{}, multipartNotFound(uploadID)
	}
	return cloneMultipartSession(session), nil
}

func (s *memoryMultipartSessionStore) ClaimMultipartCompletion(_ context.Context, uploadID, token, partsFingerprint string, now, staleBefore time.Time) (MultipartSession, bool, error) {
	return s.claimMultipartCompletion(uploadID, token, partsFingerprint, nil, now, staleBefore)
}

func (s *memoryMultipartSessionStore) ClaimMultipartCompletionWithParts(_ context.Context, uploadID, token, partsFingerprint string, parts []CompletedPart, now, staleBefore time.Time) (MultipartSession, bool, error) {
	return s.claimMultipartCompletion(uploadID, token, partsFingerprint, parts, now, staleBefore)
}

func (s *memoryMultipartSessionStore) claimMultipartCompletion(uploadID, token, partsFingerprint string, parts []CompletedPart, now, staleBefore time.Time) (MultipartSession, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[uploadID]
	if !ok {
		return MultipartSession{}, false, multipartNotFound(uploadID)
	}
	sameFingerprint := session.PartsFingerprint == "" || session.PartsFingerprint == partsFingerprint
	if sameFingerprint && (session.State == MultipartStateActive || (session.State == MultipartStateCompleting && (session.Operation == MultipartOperationComplete || session.Operation == MultipartOperationNone) && !session.UpdatedAt.After(staleBefore))) {
		session.State = MultipartStateCompleting
		session.CompletionToken = token
		session.Operation = MultipartOperationComplete
		if session.PartsFingerprint == "" {
			session.PartsFingerprint = partsFingerprint
		}
		if len(parts) > 0 {
			session.CompletionParts = append([]CompletedPart(nil), parts...)
		}
		session.UpdatedAt = now
		s.sessions[uploadID] = session
		return cloneMultipartSession(session), true, nil
	}
	return cloneMultipartSession(session), false, nil
}

func (s *memoryMultipartSessionStore) ReleaseMultipartCompletion(_ context.Context, uploadID, token string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[uploadID]
	if ok && session.State == MultipartStateCompleting && session.Operation == MultipartOperationComplete && session.CompletionToken == token {
		session.State = MultipartStateActive
		session.CompletionToken = ""
		session.Operation = MultipartOperationNone
		session.CompletionParts = nil
		session.UpdatedAt = now
		s.sessions[uploadID] = session
	}
	return nil
}

func (s *memoryMultipartSessionStore) FinishMultipartCompletion(_ context.Context, uploadID, token, location string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[uploadID]
	if !ok || session.State != MultipartStateCompleting || session.Operation != MultipartOperationComplete || session.CompletionToken != token {
		return false, nil
	}
	session.State = MultipartStateCompleted
	session.CompletionToken = ""
	session.Operation = MultipartOperationNone
	session.CompletionParts = nil
	session.CompletedLocation = location
	session.UpdatedAt = now
	s.sessions[uploadID] = session
	return true, nil
}

func (s *memoryMultipartSessionStore) ClaimMultipartAbort(_ context.Context, uploadID, token string, now, staleBefore time.Time) (MultipartSession, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[uploadID]
	if !ok {
		return MultipartSession{}, false, multipartNotFound(uploadID)
	}
	if session.State == MultipartStateCompleted {
		return cloneMultipartSession(session), false, nil
	}
	if session.State == MultipartStateCompleting && (session.Operation != MultipartOperationAbort || session.UpdatedAt.After(staleBefore)) {
		return cloneMultipartSession(session), false, nil
	}
	session.State = MultipartStateCompleting
	session.Operation = MultipartOperationAbort
	session.CompletionToken = token
	session.UpdatedAt = now
	s.sessions[uploadID] = session
	return cloneMultipartSession(session), true, nil
}

func (s *memoryMultipartSessionStore) FinishMultipartAbort(_ context.Context, uploadID, token string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[uploadID]
	if !ok {
		return true, nil
	}
	if session.State != MultipartStateCompleting || session.Operation != MultipartOperationAbort || session.CompletionToken != token {
		return false, nil
	}
	delete(s.sessions, uploadID)
	return true, nil
}

func (s *memoryMultipartSessionStore) TouchMultipartSession(_ context.Context, uploadID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[uploadID]
	if !ok || session.State != MultipartStateActive {
		return multipartNotFound(uploadID)
	}
	session.UpdatedAt = now
	s.sessions[uploadID] = session
	return nil
}

func (s *memoryMultipartSessionStore) ListMultipartSessionsForReconcile(_ context.Context, inactiveBefore, completedBefore time.Time, limit int) ([]MultipartSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		return []MultipartSession{}, nil
	}
	result := make([]MultipartSession, 0, minMultipartInt(limit, len(s.sessions)))
	for _, session := range s.sessions {
		stale := session.State == MultipartStateCompleted && !session.UpdatedAt.After(completedBefore)
		stale = stale || session.State != MultipartStateCompleted && !session.UpdatedAt.After(inactiveBefore)
		if stale {
			result = append(result, cloneMultipartSession(session))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].UploadID < result[j].UploadID
		}
		return result[i].UpdatedAt.Before(result[j].UpdatedAt)
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (s *memoryMultipartSessionStore) GetMultipartCompletionReceipt(_ context.Context, uploadID string) (MultipartCompletionReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, ok := s.receipts[uploadID]
	if !ok {
		return MultipartCompletionReceipt{}, multipartNotFound(uploadID)
	}
	receipt.Authorization = cloneMultipartAuthorization(receipt.Authorization)
	return receipt, nil
}

func (s *memoryMultipartSessionStore) CompactCompletedMultipartSessions(_ context.Context, before time.Time, limit int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		return nil
	}
	eligible := make([]MultipartSession, 0)
	for _, session := range s.sessions {
		if session.State == MultipartStateCompleted && !session.UpdatedAt.After(before) {
			eligible = append(eligible, session)
		}
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].UpdatedAt.Equal(eligible[j].UpdatedAt) {
			return eligible[i].UploadID < eligible[j].UploadID
		}
		return eligible[i].UpdatedAt.Before(eligible[j].UpdatedAt)
	})
	if len(eligible) > limit {
		eligible = eligible[:limit]
	}
	for _, session := range eligible {
		s.receipts[session.UploadID] = MultipartCompletionReceipt{
			UploadID:          session.UploadID,
			Authorization:     cloneMultipartAuthorization(session.Authorization),
			PartsFingerprint:  session.PartsFingerprint,
			CompletedLocation: session.CompletedLocation,
		}
		delete(s.sessions, session.UploadID)
	}
	return nil
}

func minMultipartInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func cloneMultipartSession(session MultipartSession) MultipartSession {
	session.Authorization = cloneMultipartAuthorization(session.Authorization)
	session.CompletionParts = append([]CompletedPart(nil), session.CompletionParts...)
	return session
}

func cloneMultipartAuthorization(authorization MultipartAuthorization) MultipartAuthorization {
	authorization.Resources = append([]string(nil), authorization.Resources...)
	authorization.Methods = append([]string(nil), authorization.Methods...)
	if authorization.Scope != nil {
		scope := *authorization.Scope
		authorization.Scope = &scope
	}
	return authorization
}

func multipartNotFound(uploadID string) error {
	return fmt.Errorf("%w: %s", errorapi.ErrMultipartUploadNotFound, uploadID)
}
