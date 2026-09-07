package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	internalapi "github.com/calypr/syfon/apigen/client/internalapi"
	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/transfer"
)

func (s *DataService) multipartInitRequest(ctx context.Context, req internalapi.InternalMultipartInitRequest) (internalapi.InternalMultipartInitOutput, error) {
	resp, err := s.gen.InternalMultipartInitWithResponse(ctx, internalapi.InternalMultipartInitJSONRequestBody(req))
	if err != nil {
		return internalapi.InternalMultipartInitOutput{}, err
	}
	if resp.JSON200 == nil {
		return internalapi.InternalMultipartInitOutput{}, internalAPIResponseError("failed to init multipart", resp.StatusCode(), resp.Body)
	}
	return *resp.JSON200, nil
}

func (d *DataService) multipartUploadRequest(ctx context.Context, req internalapi.InternalMultipartUploadRequest) (internalapi.InternalMultipartUploadOutput, error) {
	resp, err := d.gen.InternalMultipartUploadWithResponse(ctx, internalapi.InternalMultipartUploadJSONRequestBody(req))
	if err != nil {
		return internalapi.InternalMultipartUploadOutput{}, err
	}
	if resp.JSON200 == nil {
		return internalapi.InternalMultipartUploadOutput{}, internalAPIResponseError("failed to upload part", resp.StatusCode(), resp.Body)
	}
	return *resp.JSON200, nil
}

func (d *DataService) multipartCompleteRequest(ctx context.Context, req internalapi.InternalMultipartCompleteRequest) error {
	resp, err := d.gen.InternalMultipartCompleteWithResponse(ctx, internalapi.InternalMultipartCompleteJSONRequestBody(req))
	if err != nil {
		return err
	}
	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusCreated {
		return internalAPIResponseError("failed to complete multipart", resp.StatusCode(), resp.Body)
	}
	return nil
}

func internalAPIResponseError(prefix string, status int, body []byte) error {
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
		return fmt.Errorf("%s: %d: %s", prefix, status, msg)
	}
	return fmt.Errorf("%s: %d", prefix, status)
}

// --- transfer.MultipartURLSigner interface support ---

func (d *DataService) InitMultipartUpload(ctx context.Context, guid, filename, bucket string) (string, string, error) {
	return d.InitMultipartUploadWithMetadata(ctx, guid, filename, bucket, common.FileMetadata{})
}

func (d *DataService) InitMultipartUploadWithMetadata(ctx context.Context, guid, filename, bucket string, metadata common.FileMetadata) (string, string, error) {
	organization, project := uploadScopeFromMetadata(metadata)
	req := internalapi.InternalMultipartInitRequest{
		Guid:         &guid,
		Key:          &filename,
		Organization: nil,
		Project:      nil,
	}
	if organization != "" {
		req.Organization = &organization
	}
	if project != "" {
		req.Project = &project
	}
	resp, err := d.multipartInitRequest(ctx, req)
	if err != nil {
		return "", "", err
	}
	uploadID := ""
	if resp.UploadId != nil {
		uploadID = *resp.UploadId
	}
	respGuid := ""
	if resp.Guid != nil {
		respGuid = *resp.Guid
	}
	return uploadID, respGuid, nil
}

func (d *DataService) GetMultipartUploadURL(ctx context.Context, key, uploadID string, partNum int32, bucket string) (string, error) {
	req := internalapi.InternalMultipartUploadRequest{
		Key:        key,
		UploadId:   uploadID,
		PartNumber: partNum,
		Bucket:     &bucket,
	}
	resp, err := d.multipartUploadRequest(ctx, req)
	if err != nil {
		return "", err
	}
	if resp.PresignedUrl == nil {
		return "", fmt.Errorf("response missing presigned URL")
	}
	return *resp.PresignedUrl, nil
}

func (d *DataService) CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []internalapi.InternalMultipartPart, bucket string) error {
	return d.multipartCompleteRequest(ctx, internalapi.InternalMultipartCompleteRequest{
		Key:      key,
		UploadId: uploadID,
		Bucket:   &bucket,
		Parts:    parts,
	})
}

func (d *DataService) MultipartInit(ctx context.Context, guid string) (string, error) {
	uploadID, _, err := d.InitMultipartUpload(ctx, guid, "", "")
	return uploadID, err
}

func (d *DataService) MultipartPart(ctx context.Context, guid string, uploadID string, partNum int, body io.Reader) (string, error) {
	url, err := d.GetMultipartUploadURL(ctx, guid, uploadID, int32(partNum), "")
	if err != nil {
		return "", err
	}
	if sized, ok := body.(interface{ Size() int64 }); ok {
		return d.UploadPart(ctx, url, body, sized.Size())
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	return d.UploadPart(ctx, url, bytes.NewReader(data), int64(len(data)))
}

func (d *DataService) MultipartComplete(ctx context.Context, guid string, uploadID string, parts []transfer.MultipartPart) error {
	reqParts := make([]internalapi.InternalMultipartPart, 0, len(parts))
	for _, p := range parts {
		reqParts = append(reqParts, internalapi.InternalMultipartPart{
			PartNumber: p.PartNumber,
			ETag:       p.ETag,
		})
	}
	return d.multipartCompleteRequest(ctx, internalapi.InternalMultipartCompleteRequest{
		Key:      guid,
		UploadId: uploadID,
		Parts:    reqParts,
	})
}
