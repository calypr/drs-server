package services

import (
	"context"

	drsapi "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
)

// Deprecated: use errorapi.ErrNotFound.
var ErrObjectNotFound = errorapi.ErrNotFound

type DRSService struct {
	gen drsapi.ClientWithResponsesInterface
}

func NewDRSService(gen drsapi.ClientWithResponsesInterface) *DRSService {
	return &DRSService{gen: gen}
}

func (s *DRSService) GetObject(ctx context.Context, objectID string) (drsapi.DrsObject, error) {
	resp, err := s.gen.GetObjectWithResponse(ctx, drsapi.ObjectId(objectID), nil)
	if err != nil {
		return drsapi.DrsObject{}, err
	}
	if resp.JSON200 == nil {
		return drsapi.DrsObject{}, apiResponseError(resp.HTTPResponse, resp.Body)
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
		return apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return nil
}

func (s *DRSService) GetAccessURL(ctx context.Context, objectID, accessID string) (drsapi.AccessURL, error) {
	resp, err := s.gen.GetAccessURLWithResponse(ctx, drsapi.ObjectId(objectID), drsapi.AccessId(accessID))
	if err != nil {
		return drsapi.AccessURL{}, err
	}
	if resp.JSON200 == nil {
		return drsapi.AccessURL{}, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return *resp.JSON200, nil
}

func (s *DRSService) RegisterObjects(ctx context.Context, req drsapi.RegisterObjectsJSONRequestBody) (drsapi.N201ObjectsCreated, error) {
	resp, err := s.gen.RegisterObjectsWithResponse(ctx, drsapi.RegisterObjectsJSONRequestBody(req))
	if err != nil {
		return drsapi.N201ObjectsCreated{}, err
	}
	if resp.JSON201 == nil {
		return drsapi.N201ObjectsCreated{}, apiResponseError(resp.HTTPResponse, resp.Body)
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
		return drsapi.DrsObject{}, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return *resp.JSON200, nil
}
