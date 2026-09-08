package transfers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/usage"
)

type AccountingMode string

const (
	AccountingNone                AccountingMode = "none"
	AccountingDownloadBeforeEvent AccountingMode = "download_before_event"
	AccountingEventOnly           AccountingMode = "event_only"
)

type DownloadRequest struct {
	ObjectID           string
	AccessID           string
	ExpiresIn          time.Duration
	Range              *storage.ByteRange
	Accounting         AccountingMode
	AccountingObjectID string
}

type DownloadResult struct {
	URL       string
	SourceURL string
	Target    storage.Target
	Object    *objects.Record
}

type UploadRequest struct {
	ObjectID     string
	Organization string
	Project      string
	Key          string
	Scope        *AccessScope
	ExpiresIn    time.Duration
}

type UploadResult struct {
	URL      string
	Target   storage.Target
	Existing bool
	ObjectID string
	Err      error
}

type Service struct {
	objects      ObjectPort
	storage      StoragePort
	access       AccessPort
	multipart    MultipartPort
	fileCounters usage.FileCounterRecorder
	scopes       ScopeReader
	credentials  CredentialReader
	events       EventRecorder
	now          func() time.Time
}

// BindLegacyDependencies keeps the old HTTP fixture composition executable
// while callers migrate to Dependencies.Objects/FileCounters. It only fills
// missing capabilities and is harmless for the canonical composition.
func (s *Service) BindLegacyDependencies(objects ObjectPort, counters usage.FileCounterRecorder) {
	if s == nil {
		return
	}
	if s.objects == nil {
		s.objects = objects
	}
	if s.fileCounters == nil {
		s.fileCounters = counters
	}
}

func NewService(deps Dependencies) *Service {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &Service{objects: deps.Objects, storage: deps.Storage, access: deps.Access, multipart: deps.Multipart, fileCounters: deps.FileCounters, scopes: deps.Scopes, credentials: deps.Credentials, events: deps.Events, now: now}
}

func (s *Service) Download(ctx context.Context, req DownloadRequest) (DownloadResult, error) {
	if s == nil || (s.objects == nil && strings.TrimSpace(req.ObjectID) != "") || (s.storage == nil && s.access == nil) {
		return DownloadResult{}, fmt.Errorf("transfer service is not configured")
	}
	obj, err := s.objects.GetObject(ctx, strings.TrimSpace(req.ObjectID), "read")
	if err != nil {
		return DownloadResult{}, err
	}
	sourceURL := accessURLForID(obj, req.AccessID)
	if strings.TrimSpace(req.AccessID) == "" && sourceURL == "" {
		sourceURL = firstSupportedAccessURL(obj)
	}
	if sourceURL == "" {
		return DownloadResult{}, errorapi.ErrObjectLocationUnavailable
	}
	target, err := s.resolveDownloadTarget(ctx, obj, sourceURL)
	if err != nil {
		return DownloadResult{}, err
	}
	expires := req.ExpiresIn
	if expires <= 0 {
		expires = defaultSigningExpiry()
	}
	filename := ""
	if obj.Name != nil {
		filename = objects.CleanToBasename(strings.TrimSpace(*obj.Name))
	}
	signed, err := s.sign(ctx, storage.SignRequest{Target: target, Method: http.MethodGet, ExpiresIn: expires, DownloadFilename: filename, Range: req.Range})
	if err != nil {
		return DownloadResult{}, err
	}
	result := DownloadResult{URL: signed.Location, SourceURL: sourceURL, Target: target, Object: obj}
	if req.Accounting == AccountingDownloadBeforeEvent {
		if s.fileCounters == nil {
			return DownloadResult{}, fmt.Errorf("file usage recorder is not configured")
		}
		counterID := strings.TrimSpace(req.AccountingObjectID)
		if counterID == "" {
			counterID = string(obj.Id)
		}
		if err := s.fileCounters.RecordFileDownload(ctx, counterID); err != nil {
			return DownloadResult{}, err
		}
	}
	if req.Accounting == AccountingDownloadBeforeEvent || req.Accounting == AccountingEventOnly {
		if err := s.recordAccessIssued(ctx, AccessRequest{Object: obj, Target: target, AccessID: req.AccessID, Direction: usage.ProviderTransferDirectionDownload, StorageURL: sourceURL, RangeStart: rangeStart(req.Range), RangeEnd: rangeEnd(req.Range)}); err != nil {
			return DownloadResult{}, err
		}
	}
	return result, nil
}

func (s *Service) UploadURL(ctx context.Context, req UploadRequest) (UploadResult, error) {
	if s == nil || s.objects == nil || (s.storage == nil && s.access == nil) {
		return UploadResult{}, fmt.Errorf("transfer service is not configured")
	}
	objectID := strings.TrimSpace(req.ObjectID)
	var obj *objects.Record
	var err error
	if s.objects != nil {
		obj, err = s.objects.GetObject(ctx, objectID, "update")
	} else {
		err = errorapi.ErrObjectNotFound
	}
	existing := err == nil
	if err != nil && !isNotFound(err) {
		return UploadResult{}, err
	}
	if objectID == "" {
		existing = false
	}
	var target storage.Target
	if existing {
		if strings.TrimSpace(req.Organization) != "" {
			target, err = s.resolveScopedTarget(ctx, req.Organization, req.Project, uploadKeyForRequest(obj, req.Key))
		} else {
			canonical, resolveErr := s.ResolveCanonicalStorageTarget(ctx, CanonicalStorageTargetRequest{Object: obj, Key: req.Key, PreferChecksum: true})
			if resolveErr == nil {
				target = storageTargetFromCanonical(canonical.URL, canonical)
			}
			err = resolveErr
		}
	} else {
		key := strings.Trim(strings.TrimSpace(req.Key), "/")
		if key == "" {
			key = objectID
		}
		target, err = s.resolveScopedTarget(ctx, req.Organization, req.Project, key)
	}
	if err != nil {
		return UploadResult{}, err
	}
	expires := req.ExpiresIn
	if expires <= 0 {
		expires = defaultSigningExpiry()
	}
	signed, err := s.sign(ctx, storage.SignRequest{Target: target, Method: http.MethodPut, ExpiresIn: expires})
	if err != nil {
		return UploadResult{}, err
	}
	if existing {
		if err := s.recordAccessIssued(ctx, AccessRequest{Object: obj, Target: target, Scope: req.Scope, Direction: usage.ProviderTransferDirectionUpload, StorageURL: target.OriginalURL}); err != nil {
			return UploadResult{}, err
		}
	}
	return UploadResult{URL: signed.Location, Target: target, Existing: existing, ObjectID: objectID}, nil
}

func (s *Service) UploadBulk(ctx context.Context, requests []UploadRequest) []UploadResult {
	results := make([]UploadResult, len(requests))
	for i, req := range requests {
		result, err := s.UploadURL(ctx, req)
		result.ObjectID = strings.TrimSpace(req.ObjectID)
		result.Err = err
		results[i] = result
	}
	return results
}

func (s *Service) recordAccessIssued(ctx context.Context, req AccessRequest) error {
	if req.Object == nil {
		return nil
	}
	if s.events == nil {
		return fmt.Errorf("transfer event recorder is not configured")
	}
	event := eventFromObject(ctx, req)
	if s.now != nil {
		event.EventTime = s.now().UTC()
		event.EventID = usage.EventID(event)
		event.AccessGrantID = usage.GrantID(event)
	}
	return s.events.RecordTransferAttributionEvents(ctx, []usage.Event{event})
}

func (s *Service) sign(ctx context.Context, request storage.SignRequest) (storage.SignedAccess, error) {
	if s.storage != nil {
		return s.storage.Sign(ctx, request)
	}
	if s.access == nil {
		return storage.SignedAccess{}, fmt.Errorf("storage access is not configured")
	}
	location := request.Target.OriginalURL
	if location == "" {
		location = request.Target.CanonicalURL
	}
	access, err := s.access.Access(ctx, storage.AccessRequest{Target: storage.AccessTarget{AccessID: request.Target.LookupKey, Location: location}, Options: storage.AccessOptions{Method: request.Method, ExpiresIn: request.ExpiresIn, DownloadFilename: request.DownloadFilename}, Range: request.Range})
	if err != nil {
		return storage.SignedAccess{}, err
	}
	return storage.SignedAccess{Location: access.Location}, nil
}

func isNotFound(err error) bool {
	return errors.Is(err, errorapi.ErrNotFound) || errors.Is(err, errorapi.ErrObjectNotFound)
}

func defaultSigningExpiry() time.Duration { return 15 * time.Minute }
func rangeStart(r *storage.ByteRange) *int64 {
	if r == nil {
		return nil
	}
	v := r.Start
	return &v
}
func rangeEnd(r *storage.ByteRange) *int64 {
	if r == nil {
		return nil
	}
	v := r.End
	return &v
}
func uploadKeyForRequest(obj *objects.Record, key string) string {
	if key = strings.Trim(strings.TrimSpace(key), "/"); key != "" {
		return key
	}
	if obj != nil {
		if sha, ok := objects.CanonicalSHA256(obj.Checksums); ok {
			return sha
		}
	}
	return ""
}
