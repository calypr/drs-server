package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/internalapi"

	clientaccess "github.com/calypr/syfon/client/access"
)

type ListRecordsOptions struct {
	Hash         string
	URL          string
	Organization string
	ProjectID    string
	Limit        int
	Start        string
	Page         int
}

type DeleteByQueryOptions struct {
	Organization string
	ProjectID    string
	Hash         string
	HashType     string
}

type IndexService struct {
	gen internalapi.ClientWithResponsesInterface
}

func NewIndexService(gen internalapi.ClientWithResponsesInterface) *IndexService {
	return &IndexService{gen: gen}
}

func (s *IndexService) Get(ctx context.Context, did string) (internalapi.InternalRecordResponse, error) {
	resp, err := s.gen.InternalGetWithResponse(ctx, did)
	if err != nil {
		return internalapi.InternalRecordResponse{}, err
	}
	if resp.JSON200 == nil {
		return internalapi.InternalRecordResponse{}, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return *resp.JSON200, nil
}

func (s *IndexService) Create(ctx context.Context, rec internalapi.InternalRecord) (internalapi.InternalRecordResponse, error) {
	resp, err := s.gen.InternalCreateWithResponse(ctx, internalapi.InternalCreateJSONRequestBody(rec))
	if err != nil {
		return internalapi.InternalRecordResponse{}, err
	}
	if resp.JSON201 == nil {
		return internalapi.InternalRecordResponse{}, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return *resp.JSON201, nil
}

func (s *IndexService) Update(ctx context.Context, did string, rec internalapi.InternalRecord) (internalapi.InternalRecordResponse, error) {
	resp, err := s.gen.InternalUpdateWithResponse(ctx, did, internalapi.InternalUpdateJSONRequestBody(rec))
	if err != nil {
		return internalapi.InternalRecordResponse{}, err
	}
	if resp.JSON200 == nil {
		return internalapi.InternalRecordResponse{}, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return *resp.JSON200, nil
}

func (s *IndexService) Delete(ctx context.Context, did string) error {
	resp, err := s.gen.InternalDeleteWithResponse(ctx, did)
	if err != nil {
		return err
	}
	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusNoContent {
		return apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return nil
}

func (s *IndexService) DeleteByQuery(ctx context.Context, opts DeleteByQueryOptions) (internalapi.DeleteByQueryResponse, error) {
	params := &internalapi.InternalDeleteByQueryParams{}
	if opts.Organization != "" {
		params.Organization = &opts.Organization
	}
	if opts.ProjectID != "" {
		params.Project = &opts.ProjectID
	}
	if opts.Hash != "" {
		params.Hash = &opts.Hash
	}
	if opts.HashType != "" {
		params.HashType = &opts.HashType
	}
	resp, err := s.gen.InternalDeleteByQueryWithResponse(ctx, params)
	if err != nil {
		return internalapi.DeleteByQueryResponse{}, err
	}
	if resp.JSON200 == nil {
		return internalapi.DeleteByQueryResponse{}, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return *resp.JSON200, nil
}

func (s *IndexService) RemoveControlledAccess(ctx context.Context, did, resource string) (internalapi.InternalRecordResponse, error) {
	resp, err := s.gen.InternalRemoveControlledAccessWithResponse(ctx, did, internalapi.InternalRemoveControlledAccessJSONRequestBody{
		Resource: resource,
	})
	if err != nil {
		return internalapi.InternalRecordResponse{}, err
	}
	if resp.JSON200 == nil {
		return internalapi.InternalRecordResponse{}, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return *resp.JSON200, nil
}

func (s *IndexService) List(ctx context.Context, opts ListRecordsOptions) (internalapi.ListRecordsResponse, error) {
	params := &internalapi.InternalListParams{}
	if opts.Hash != "" {
		params.Hash = &opts.Hash
	}
	if opts.URL != "" {
		params.Url = &opts.URL
	}
	if opts.Organization != "" {
		params.Organization = &opts.Organization
	}
	if opts.ProjectID != "" {
		params.Project = &opts.ProjectID
	}
	if opts.Limit != 0 {
		params.Limit = &opts.Limit
	}
	if opts.Start != "" {
		params.Start = &opts.Start
	} else if opts.Page != 0 {
		params.Page = &opts.Page
	}
	resp, err := s.gen.InternalListWithResponse(ctx, params)
	if err != nil {
		return internalapi.ListRecordsResponse{}, err
	}
	if resp.JSON200 == nil {
		return internalapi.ListRecordsResponse{}, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return *resp.JSON200, nil
}

func (s *IndexService) Upsert(ctx context.Context, did, objectURL, recordPath string, size int64, sha256sum string, authorizations map[string][]string) error {
	recordName := strings.TrimSpace(recordPath)
	if recordName != "" {
		recordName = path.Base(strings.Trim(recordName, "/"))
	}
	existing, err := s.Get(ctx, did)
	if err == nil {
		req := internalapi.InternalRecord{
			Did:              existing.Did,
			AccessMethods:    existing.AccessMethods,
			ControlledAccess: existing.ControlledAccess,
			Description:      existing.Description,
			Name:             existing.Name,
			Hashes:           existing.Hashes,
			Size:             existing.Size,
			Version:          existing.Version,
			Organization:     existing.Organization,
			Project:          existing.Project,
		}

		if strings.TrimSpace(req.Did) == "" {
			req.Did = did
		}
		if req.ControlledAccess == nil || len(*req.ControlledAccess) == 0 {
			if len(authorizations) == 0 {
				return fmt.Errorf("authorizations are required to upsert record %s", did)
			}
			controlled := clientaccess.AuthzMapToControlledAccess(authorizations)
			req.ControlledAccess = &controlled
		}
		if recordName != "" {
			req.Name = &recordName
		}
		if size > 0 {
			req.Size = &size
		}
		if objectURL != "" {
			appendAccessMethod(&req, objectURL)
		}
		if sha256sum != "" {
			if req.Hashes == nil {
				h := make(internalapi.HashInfo)
				req.Hashes = &h
			}
			(*req.Hashes)["sha256"] = sha256sum
		}
		_, err := s.Update(ctx, did, req)
		return err
	}

	if !errors.Is(err, errorapi.ErrNotFound) {
		return err
	}

	payload := internalapi.InternalRecord{
		Did: did,
	}
	if len(authorizations) == 0 {
		return fmt.Errorf("authorizations are required to create record %s", did)
	}
	controlled := clientaccess.AuthzMapToControlledAccess(authorizations)
	payload.ControlledAccess = &controlled
	appendAccessMethod(&payload, objectURL)
	if size > 0 {
		payload.Size = &size
	}
	if recordName != "" {
		payload.Name = &recordName
	}
	if sha256sum != "" {
		h := internalapi.HashInfo{"sha256": sha256sum}
		payload.Hashes = &h
	}
	_, err = s.Create(ctx, payload)
	return err
}

func appendAccessMethod(req *internalapi.InternalRecord, rawURL string) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return
	}
	methodType := methodTypeForURL(rawURL)
	methods := []drs.AccessMethod{}
	if req.AccessMethods != nil {
		methods = append(methods, (*req.AccessMethods)...)
	}
	for _, existing := range methods {
		if existing.AccessUrl != nil && strings.TrimSpace(existing.AccessUrl.Url) == rawURL {
			req.AccessMethods = &methods
			return
		}
	}
	methods = append(methods, drs.AccessMethod{
		Type:      drs.AccessMethodType(methodType),
		AccessId:  &methodType,
		AccessUrl: &drs.AccessURL{Url: rawURL},
	})
	req.AccessMethods = &methods
}

func methodTypeForURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err == nil && strings.TrimSpace(parsed.Scheme) != "" {
		return strings.TrimSpace(parsed.Scheme)
	}
	return "https"
}
