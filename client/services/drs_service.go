package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	drsapi "github.com/calypr/syfon/apigen/client/drs"
	"github.com/calypr/syfon/apigen/client/internalapi"
	"github.com/calypr/syfon/client/transfer"
	syfoncommon "github.com/calypr/syfon/common"
)

var ErrNoRecordsForHash = errors.New("no records found for hash")
var ErrObjectNotFound = errors.New("drs object not found")

type DRSService struct {
	gen   drsapi.ClientWithResponsesInterface
	index *IndexService
}

func NewDRSService(gen drsapi.ClientWithResponsesInterface, index *IndexService) *DRSService {
	return &DRSService{gen: gen, index: index}
}

// Resolve implements transfer.Resolver by fetching object metadata from the DRS API.
func (s *DRSService) Resolve(ctx context.Context, id string) (*transfer.ResolvedObject, error) {
	obj, err := s.GetObject(ctx, id)
	if err != nil {
		return nil, err
	}
	if obj.AccessMethods == nil || len(*obj.AccessMethods) == 0 {
		return nil, fmt.Errorf("no access methods found for object %s", id)
	}
	am := (*obj.AccessMethods)[0]
	name := ""
	if obj.Name != nil {
		name = *obj.Name
	}
	url := ""
	if am.AccessUrl != nil {
		url = am.AccessUrl.Url
	}
	return &transfer.ResolvedObject{
		Id:           obj.Id,
		Name:         name,
		Size:         obj.Size,
		ProviderURL:  url,
		AccessMethod: string(am.Type),
	}, nil
}

func (s *DRSService) GetObject(ctx context.Context, objectID string) (drsapi.DrsObject, error) {
	resp, err := s.gen.GetObjectWithResponse(ctx, drsapi.ObjectId(objectID), nil)
	if err != nil {
		return drsapi.DrsObject{}, err
	}
	if resp.JSON200 == nil {
		if resp.StatusCode() == http.StatusNotFound {
			return drsapi.DrsObject{}, fmt.Errorf("%w: %s", ErrObjectNotFound, objectID)
		}
		return drsapi.DrsObject{}, drsResponseError(resp.StatusCode(), resp.Body)
	}
	return *resp.JSON200, nil
}

func (s *DRSService) DeleteObject(ctx context.Context, objectID string, deleteStorageData bool) error {
	deleteMetadata := true
	resp, err := s.gen.DeleteObjectWithResponse(ctx, drsapi.ObjectId(objectID), drsapi.DeleteObjectJSONRequestBody{
		DeleteObjectMetadata: &deleteMetadata,
		DeleteStorageData:    &deleteStorageData,
	})
	if err != nil {
		return err
	}
	if resp.StatusCode() != 200 && resp.StatusCode() != 204 {
		return drsResponseError(resp.StatusCode(), resp.Body)
	}
	return nil
}

func (s *DRSService) ListObjects(ctx context.Context, limit, page int) (DRSPage, error) {
	listResp, err := s.index.List(ctx, ListRecordsOptions{
		Limit: limit,
		Page:  page,
	})
	if err != nil {
		return DRSPage{}, err
	}
	records := make([]internalapi.InternalRecord, 0)
	if listResp.Records != nil {
		records = *listResp.Records
	}
	out := DRSPage{
		DrsObjects: make([]drsapi.DrsObject, 0, len(records)),
	}
	for _, rec := range records {
		out.DrsObjects = append(out.DrsObjects, internalRecordToDRSObject(&rec))
	}
	return out, nil
}

func (s *DRSService) ListObjectsAfter(ctx context.Context, limit int, start string) (DRSPage, error) {
	listResp, err := s.index.List(ctx, ListRecordsOptions{
		Limit: limit,
		Start: start,
	})
	if err != nil {
		return DRSPage{}, err
	}
	records := make([]internalapi.InternalRecord, 0)
	if listResp.Records != nil {
		records = *listResp.Records
	}
	out := DRSPage{
		DrsObjects: make([]drsapi.DrsObject, 0, len(records)),
	}
	for _, rec := range records {
		out.DrsObjects = append(out.DrsObjects, internalRecordToDRSObject(&rec))
	}
	return out, nil
}

func (s *DRSService) GetAccessURL(ctx context.Context, objectID, accessID string) (drsapi.AccessURL, error) {
	resp, err := s.gen.GetAccessURLWithResponse(ctx, drsapi.ObjectId(objectID), drsapi.AccessId(accessID))
	if err != nil {
		return drsapi.AccessURL{}, err
	}
	if resp.JSON200 == nil {
		return drsapi.AccessURL{}, drsResponseError(resp.StatusCode(), resp.Body)
	}
	return *resp.JSON200, nil
}

func (s *DRSService) RegisterObjects(ctx context.Context, req drsapi.RegisterObjectsJSONRequestBody) (drsapi.N201ObjectsCreated, error) {
	resp, err := s.gen.RegisterObjectsWithResponse(ctx, drsapi.RegisterObjectsJSONRequestBody(req))
	if err != nil {
		return drsapi.N201ObjectsCreated{}, err
	}
	if resp.JSON201 == nil {
		return drsapi.N201ObjectsCreated{}, drsResponseError(resp.StatusCode(), resp.Body)
	}
	return *resp.JSON201, nil
}

func (s *DRSService) UpdateObjectAccessMethods(ctx context.Context, objectID string, accessMethods []drsapi.AccessMethod) (drsapi.DrsObject, error) {
	resp, err := s.gen.UpdateObjectAccessMethodsWithResponse(ctx, objectID, drsapi.UpdateObjectAccessMethodsJSONRequestBody{
		AccessMethods: accessMethods,
	})
	if err != nil {
		return drsapi.DrsObject{}, err
	}
	if resp.JSON200 == nil {
		if resp.StatusCode() == http.StatusNotFound {
			return drsapi.DrsObject{}, fmt.Errorf("%w: %s", ErrObjectNotFound, objectID)
		}
		return drsapi.DrsObject{}, drsResponseError(resp.StatusCode(), resp.Body)
	}
	return *resp.JSON200, nil
}

func drsResponseError(status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if msg != "" {
		var payload struct {
			Error *struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(body, &payload); err == nil {
			switch {
			case payload.Error != nil && strings.TrimSpace(payload.Error.Message) != "":
				msg = strings.TrimSpace(payload.Error.Message)
			case strings.TrimSpace(payload.Message) != "":
				msg = strings.TrimSpace(payload.Message)
			}
		}
		return fmt.Errorf("unexpected response: %d: %s", status, msg)
	}
	return fmt.Errorf("unexpected response: %d", status)
}

func (s *DRSService) ListObjectsByProject(ctx context.Context, projectID string, limit, page int) (DRSPage, error) {
	listResp, err := s.index.List(ctx, ListRecordsOptions{
		ProjectID: projectID,
		Limit:     limit,
		Page:      page,
	})
	if err != nil {
		return DRSPage{}, err
	}
	records := make([]internalapi.InternalRecord, 0)
	if listResp.Records != nil {
		records = *listResp.Records
	}
	out := DRSPage{
		DrsObjects: make([]drsapi.DrsObject, 0, len(records)),
	}
	for _, rec := range records {
		out.DrsObjects = append(out.DrsObjects, internalRecordToDRSObject(&rec))
	}
	return out, nil
}

func (s *DRSService) ListObjectsByProjectAfter(ctx context.Context, projectID string, limit int, start string) (DRSPage, error) {
	listResp, err := s.index.List(ctx, ListRecordsOptions{
		ProjectID: projectID,
		Limit:     limit,
		Start:     start,
	})
	if err != nil {
		return DRSPage{}, err
	}
	records := make([]internalapi.InternalRecord, 0)
	if listResp.Records != nil {
		records = *listResp.Records
	}
	out := DRSPage{
		DrsObjects: make([]drsapi.DrsObject, 0, len(records)),
	}
	for _, rec := range records {
		out.DrsObjects = append(out.DrsObjects, internalRecordToDRSObject(&rec))
	}
	return out, nil
}

func (s *DRSService) GetProjectSample(ctx context.Context, projectID string, limit int) (DRSPage, error) {
	return s.ListObjectsByProject(ctx, projectID, limit, 1)
}

func (s *DRSService) BatchGetObjectsByHash(ctx context.Context, hashes []string) (DRSPage, error) {
	out := DRSPage{DrsObjects: make([]drsapi.DrsObject, 0, len(hashes))}
	seen := make(map[string]struct{})
	for _, h := range hashes {
		checksum := normalizeChecksum(h)
		if checksum == "" {
			continue
		}
		resp, err := s.gen.GetObjectsByChecksumWithResponse(ctx, drsapi.ChecksumParameter(checksum))
		if err != nil {
			return DRSPage{}, fmt.Errorf("get objects by checksum %s: %w", checksum, err)
		}
		if resp.JSON200 == nil || resp.JSON200.ResolvedDrsObject == nil {
			return DRSPage{}, fmt.Errorf("get objects by checksum %s failed: unexpected response: %d", checksum, resp.StatusCode())
		}
		for _, obj := range *resp.JSON200.ResolvedDrsObject {
			id := strings.TrimSpace(obj.Id)
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			out.DrsObjects = append(out.DrsObjects, obj)
		}
	}
	return out, nil
}

func (s *DRSService) DeleteRecordsByHash(ctx context.Context, hash string) error {
	hash = normalizeChecksum(hash)
	page, err := s.BatchGetObjectsByHash(ctx, []string{hash})
	if err != nil {
		return fmt.Errorf("error resolving DRS object for hash %s: %w", hash, err)
	}
	if len(page.DrsObjects) == 0 {
		return fmt.Errorf("%w %s", ErrNoRecordsForHash, hash)
	}

	seen := make(map[string]struct{}, len(page.DrsObjects))
	for _, record := range page.DrsObjects {
		did := strings.TrimSpace(record.Id)
		if did == "" {
			continue
		}
		if _, exists := seen[did]; exists {
			continue
		}
		seen[did] = struct{}{}
		if err := s.index.Delete(ctx, did); err != nil {
			return fmt.Errorf("error deleting DID %s for hash %s: %w", did, hash, err)
		}
	}
	if len(seen) == 0 {
		return fmt.Errorf("no deleteable DIDs found for hash %s", hash)
	}
	return nil
}

func normalizeChecksum(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "sha256:")
	return strings.TrimSpace(raw)
}

func internalRecordToDRSObject(rec *internalapi.InternalRecord) drsapi.DrsObject {
	size := int64(0)
	if rec.Size != nil {
		size = *rec.Size
	}
	obj := drsapi.DrsObject{
		Id:      rec.Did,
		SelfUri: "drs://" + rec.Did,
		Size:    size,
	}
	if rec.Name != nil {
		base := path.Base(strings.TrimSpace(*rec.Name))
		if base == "." || base == "/" || base == "" {
			base = strings.TrimSpace(*rec.Name)
		}
		obj.Name = &base
	}
	if rec.Hashes != nil {
		hInfo := *rec.Hashes
		obj.Checksums = make([]drsapi.Checksum, 0, len(hInfo))
		for typ, checksum := range hInfo {
			obj.Checksums = append(obj.Checksums, drsapi.Checksum{
				Type:     typ,
				Checksum: checksum,
			})
		}
	}
	if rec.ControlledAccess != nil {
		controlled := syfoncommon.NormalizeAccessResources(*rec.ControlledAccess)
		obj.ControlledAccess = &controlled
	}
	if rec.AccessMethods != nil {
		methods := append([]drsapi.AccessMethod(nil), (*rec.AccessMethods)...)
		obj.AccessMethods = &methods
	}
	return obj
}
