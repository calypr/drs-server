package lfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/calypr/syfon/apigen/server/lfsapi"
	"github.com/calypr/syfon/internal/api/attribution"
	apimiddleware "github.com/calypr/syfon/internal/api/middleware"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/urlmanager"
)

func prepareDownloadActions(ctx context.Context, om *core.ObjectManager, oid string) (*lfsapi.BatchActions, *lfsapi.ObjectError) {
	obj, err := om.GetObject(ctx, oid, "read")
	if err != nil {
		return nil, dbErrToBatchError(ctx, err)
	}

	var src string
	var accessID string
	if obj.AccessMethods != nil {
		for _, am := range *obj.AccessMethods {
			if am.AccessUrl != nil && strings.TrimSpace(am.AccessUrl.Url) != "" {
				src = am.AccessUrl.Url
				if am.AccessId != nil && strings.TrimSpace(*am.AccessId) != "" {
					accessID = strings.TrimSpace(*am.AccessId)
				} else {
					accessID = strings.TrimSpace(string(am.Type))
				}
				break
			}
		}
	}
	if src == "" {
		return nil, &lfsapi.ObjectError{Code: int32(http.StatusNotFound), Message: "no object location available"}
	}

	signed, err := om.SignObjectURL(ctx, obj, src, urlmanager.SignOptions{})
	if err != nil {
		return nil, &lfsapi.ObjectError{Code: int32(http.StatusInternalServerError), Message: err.Error()}
	}

	if err := om.RecordDownload(ctx, oid); err != nil {
		return nil, &lfsapi.ObjectError{Code: int32(http.StatusInternalServerError), Message: err.Error()}
	}
	if err := attribution.RecordAccessIssued(ctx, om, obj, attribution.AccessDetails{
		Direction:  models.ProviderTransferDirectionDownload,
		AccessID:   accessID,
		StorageURL: src,
	}); err != nil {
		return nil, &lfsapi.ObjectError{Code: int32(http.StatusInternalServerError), Message: err.Error()}
	}
	action := lfsapi.Action{Href: signed}
	return &lfsapi.BatchActions{Download: &action}, nil
}

func prepareUploadActions(ctx context.Context, om *core.ObjectManager, oid string, reqSize int64, baseURL string) (*lfsapi.BatchActions, int64, *lfsapi.ObjectError) {
	existing, err := om.GetObject(ctx, oid, "read")
	if err == nil {
		return nil, existing.Size, nil
	}
	if !faults.IsNotFoundError(err) {
		return nil, reqSize, dbErrToBatchError(ctx, err)
	}

	if err := om.RequireObjectResources(ctx, "create", []string{"/data_file"}); err != nil {
		return nil, reqSize, dbErrToBatchError(ctx, err)
	}

	creds, credErr := om.ListS3Credentials(ctx)
	if credErr != nil || len(creds) == 0 || strings.TrimSpace(creds[0].Bucket) == "" {
		return nil, reqSize, &lfsapi.ObjectError{Code: int32(http.StatusInsufficientStorage), Message: "no bucket configured"}
	}

	size := reqSize
	if size < 0 {
		size = 0
	}
	uploadURL := baseURL + "/info/lfs/objects/" + oid
	verifyURL := baseURL + "/info/lfs/verify"
	uploadAction := lfsapi.Action{Href: uploadURL}
	verifyAction := lfsapi.Action{Href: verifyURL}
	return &lfsapi.BatchActions{Upload: &uploadAction, Verify: &verifyAction}, size, nil
}

func uploadPartToSignedURL(ctx context.Context, signedURL string, content []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, signedURL, bytes.NewReader(content))
	if err != nil {
		return "", err
	}
	req.ContentLength = int64(len(content))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 2048))
		if err != nil {
			return "", fmt.Errorf("read multipart part error body: %w", err)
		}
		return "", fmt.Errorf("multipart part put failed status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	etag := strings.TrimSpace(resp.Header.Get("ETag"))
	etag = strings.Trim(etag, "\"")
	if etag == "" {
		return "", fmt.Errorf("multipart part upload missing etag")
	}
	return etag, nil
}

func dbErrToBatchError(ctx context.Context, err error) *lfsapi.ObjectError {
	if faults.IsNotFoundError(err) {
		return &lfsapi.ObjectError{Code: int32(http.StatusNotFound), Message: "object not found"}
	}
	if err == faults.ErrUnauthorized {
		return &lfsapi.ObjectError{Code: int32(apimiddleware.AuthFailureStatus(ctx)), Message: "unauthorized"}
	}
	return &lfsapi.ObjectError{Code: int32(http.StatusInternalServerError), Message: err.Error()}
}
