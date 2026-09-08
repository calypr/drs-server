package transfers

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/usage"
)

type AccessLookupResult struct {
	Found  bool
	URL    string
	Target storage.Target
	Object *objects.Record
}

type AccessLookupRequest struct {
	ObjectID string
	AccessID string
}

type BulkAccessLookupRequest struct {
	ObjectID  string
	AccessIDs []string
}

type ResolvedAccess struct {
	ObjectID string
	AccessID string
	URL      string
}

type BulkAccessLookupResult struct {
	Requested           int
	Resolved            []ResolvedAccess
	UnresolvedObjectIDs []string
}

func (s *Service) IssueAccess(ctx context.Context, request AccessLookupRequest) (AccessLookupResult, error) {
	if s == nil || s.objects == nil || s.storage == nil {
		return AccessLookupResult{}, fmt.Errorf("transfer service is not configured")
	}
	obj, err := s.objects.GetObject(ctx, strings.TrimSpace(request.ObjectID), "read")
	if err != nil {
		return AccessLookupResult{}, err
	}
	sourceURL := accessURLForID(obj, request.AccessID)
	if sourceURL == "" {
		return AccessLookupResult{}, nil
	}
	target, err := s.resolveDownloadTarget(ctx, obj, sourceURL)
	if err != nil {
		return AccessLookupResult{}, err
	}
	filename := ""
	if obj.Name != nil {
		filename = objects.CleanToBasename(strings.TrimSpace(*obj.Name))
	}
	expires := defaultSigningExpiry()
	signed, err := s.sign(ctx, storage.SignRequest{Target: target, Method: http.MethodGet, ExpiresIn: expires, DownloadFilename: filename})
	if err != nil {
		return AccessLookupResult{}, err
	}
	if err := s.recordAccessIssued(ctx, AccessRequest{Object: obj, Target: target, AccessID: request.AccessID, Direction: usage.ProviderTransferDirectionDownload, StorageURL: sourceURL}); err != nil {
		return AccessLookupResult{}, err
	}
	return AccessLookupResult{Found: true, URL: signed.Location, Target: target, Object: obj}, nil
}

func (s *Service) IssueAccessBulk(ctx context.Context, requests []AccessLookupRequest) BulkAccessLookupResult {
	result := BulkAccessLookupResult{Resolved: make([]ResolvedAccess, 0)}
	unresolved := make(map[string]struct{})
	unresolvedOrder := make([]string, 0)
	for _, request := range requests {
		result.Requested++
		resolved, err := s.IssueAccess(ctx, request)
		if err != nil || !resolved.Found {
			if objectID := strings.TrimSpace(request.ObjectID); objectID != "" {
				if _, seen := unresolved[objectID]; !seen {
					unresolved[objectID] = struct{}{}
					unresolvedOrder = append(unresolvedOrder, objectID)
				}
			}
			continue
		}
		result.Resolved = append(result.Resolved, ResolvedAccess{ObjectID: strings.TrimSpace(request.ObjectID), AccessID: strings.TrimSpace(request.AccessID), URL: resolved.URL})
	}
	result.UnresolvedObjectIDs = append(result.UnresolvedObjectIDs, unresolvedOrder...)
	return result
}

func accessURLForID(obj *objects.Record, accessID string) string {
	if obj == nil || obj.AccessMethods == nil {
		return ""
	}
	accessID = strings.TrimSpace(accessID)
	if accessID == "" {
		return ""
	}
	legacyMatches := make([]string, 0, 1)
	for _, method := range *obj.AccessMethods {
		if method.AccessUrl == nil || strings.TrimSpace(method.AccessUrl.Url) == "" {
			continue
		}
		if strings.EqualFold(rawAccessID(method.AccessId), accessID) {
			return method.AccessUrl.Url
		}
		if strings.EqualFold(strings.TrimSpace(method.Type), accessID) {
			legacyMatches = append(legacyMatches, method.AccessUrl.Url)
		}
	}
	if len(legacyMatches) == 1 {
		return legacyMatches[0]
	}
	return ""
}

func rawAccessID(accessID *string) string {
	if accessID == nil {
		return ""
	}
	return *accessID
}
