package services

import (
	"context"
	"net/http"

	internalapi "github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/client/logs"
	"github.com/calypr/syfon/client/request"
)

type DataService struct {
	gen       internalapi.ClientWithResponsesInterface
	requestor request.Requester
	logger    *logs.Gen3Logger
	drs       *DRSService
}

func NewDataService(gen internalapi.ClientWithResponsesInterface, r request.Requester, l *logs.Gen3Logger, drs *DRSService) *DataService {
	return &DataService{
		gen:       gen,
		requestor: r,
		logger:    l,
		drs:       drs,
	}
}

func (d *DataService) UploadBlank(ctx context.Context, req internalapi.InternalUploadBlankRequest) (internalapi.InternalUploadBlankOutput, error) {
	resp, err := d.gen.InternalUploadBlankWithResponse(ctx, internalapi.InternalUploadBlankJSONRequestBody(req))
	if err != nil {
		return internalapi.InternalUploadBlankOutput{}, err
	}
	if resp.JSON201 == nil {
		return internalapi.InternalUploadBlankOutput{}, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return *resp.JSON201, nil
}

func (d *DataService) UploadURL(ctx context.Context, req UploadURLRequest) (internalapi.InternalSignedURL, error) {
	params := &internalapi.InternalUploadURLParams{}
	if req.Key != "" {
		params.Key = &req.Key
	}
	if req.ExpiresIn > 0 {
		expires := int32(req.ExpiresIn)
		params.ExpiresIn = &expires
	}
	if req.Organization != "" {
		params.Organization = &req.Organization
	}
	if req.Project != "" {
		params.Project = &req.Project
	}
	resp, err := d.gen.InternalUploadURLWithResponse(ctx, req.FileID, params)
	if err != nil {
		return internalapi.InternalSignedURL{}, err
	}
	if resp.JSON200 == nil {
		return internalapi.InternalSignedURL{}, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return *resp.JSON200, nil
}

func (d *DataService) UploadBulk(ctx context.Context, req internalapi.InternalUploadBulkRequest) (internalapi.InternalUploadBulkOutput, error) {
	resp, err := d.gen.InternalUploadBulkWithResponse(ctx, internalapi.InternalUploadBulkJSONRequestBody(req))
	if err != nil {
		return internalapi.InternalUploadBulkOutput{}, err
	}
	if resp.JSON200 == nil {
		return internalapi.InternalUploadBulkOutput{}, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return *resp.JSON200, nil
}

func (d *DataService) DownloadURL(ctx context.Context, did string, expiresIn int, redirect bool) (internalapi.InternalSignedURL, error) {
	params := &internalapi.InternalDownloadParams{}
	if expiresIn > 0 {
		params.ExpiresIn = &expiresIn
	}
	if redirect {
		params.Redirect = &redirect
	}
	resp, err := d.gen.InternalDownloadWithResponse(ctx, did, params)
	if err != nil {
		return internalapi.InternalSignedURL{}, err
	}
	if resp.JSON200 == nil {
		return internalapi.InternalSignedURL{}, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return *resp.JSON200, nil
}

func (d *DataService) DeleteFile(ctx context.Context, guid string) (string, error) {
	resp, err := d.gen.InternalDeleteWithResponse(ctx, guid)
	if err != nil {
		return "", err
	}
	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusNoContent {
		return "", apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return guid, nil
}

func (d *DataService) Delete(ctx context.Context, guid string) error {
	_, err := d.DeleteFile(ctx, guid)
	return err
}
