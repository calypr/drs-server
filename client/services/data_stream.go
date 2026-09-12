package services

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/transfer"
)

func (d *DataService) Stat(ctx context.Context, guid string) (*transfer.ObjectMetadata, error) {
	if d.drs != nil {
		obj, err := d.drs.GetObject(ctx, guid)
		if err == nil {
			md := &transfer.ObjectMetadata{
				Size:     obj.Size,
				Identity: downloadObjectIdentity(&obj),
			}
			if obj.AccessMethods != nil && len(*obj.AccessMethods) > 0 {
				md.AcceptRanges = true
			}
			return md, nil
		}
	}
	_, err := d.ResolveDownloadURL(ctx, guid, "")
	if err != nil {
		return nil, err
	}
	return &transfer.ObjectMetadata{
		AcceptRanges: true,
		Size:         0,
	}, nil
}

func downloadObjectIdentity(object *drs.DrsObject) string {
	if object == nil {
		return ""
	}
	for _, checksum := range object.Checksums {
		checksumType := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(checksum.Type), "-", ""))
		if checksumType == "sha256" {
			value := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(checksum.Checksum)), "sha256:")
			decoded, err := hex.DecodeString(value)
			if err == nil && len(decoded) == 32 {
				return "sha256:" + value
			}
		}
	}
	return ""
}

func (d *DataService) GetReader(ctx context.Context, guid string) (io.ReadCloser, error) {
	signedURL, err := d.ResolveDownloadURL(ctx, guid, "")
	if err != nil {
		return nil, err
	}
	resp, err := d.Download(ctx, signedURL, nil, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (d *DataService) GetRangeReader(ctx context.Context, guid string, offset, length int64) (io.ReadCloser, error) {
	signedURL, err := d.ResolveDownloadURL(ctx, guid, "")
	if err != nil {
		return nil, err
	}
	var end *int64
	if length > 0 {
		e := offset + length - 1
		end = &e
	}
	resp, err := d.Download(ctx, signedURL, &offset, end)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK {
		resp.Body.Close()
		return nil, transfer.ErrRangeIgnored
	}
	return resp.Body, nil
}

func (d *DataService) ResolveDownloadURL(ctx context.Context, guid string, accessID string) (string, error) {
	resp, err := d.DownloadURL(ctx, guid, 0, false)
	if err != nil {
		return "", err
	}
	if resp.Url == nil {
		return "", fmt.Errorf("response missing URL")
	}
	return *resp.Url, nil
}

func (d *DataService) Download(ctx context.Context, signedURL string, rangeStart, rangeEnd *int64) (*http.Response, error) {
	return transfer.GenericDownload(ctx, d.httpClient, signedURL, rangeStart, rangeEnd)
}

func (d *DataService) ResolveUploadURL(ctx context.Context, guid, filename string, metadata common.FileMetadata, bucket string) (string, error) {
	organization, project := uploadScopeFromMetadata(metadata)
	params := &internalapi.InternalUploadURLParams{Key: &filename}
	if organization != "" {
		params.Organization = &organization
	}
	if project != "" {
		params.Project = &project
	}
	resp, err := d.UploadURL(ctx, guid, params)
	if err != nil {
		return "", err
	}
	if resp.Url == nil {
		return "", fmt.Errorf("response missing URL")
	}
	return *resp.Url, nil
}

func uploadScopeFromMetadata(metadata common.FileMetadata) (string, string) {
	if len(metadata.Authorizations) == 0 {
		return "", ""
	}
	for org, projects := range metadata.Authorizations {
		org = strings.TrimSpace(org)
		if org == "" {
			continue
		}
		for _, project := range projects {
			project = strings.TrimSpace(project)
			if project != "" {
				return org, project
			}
		}
		return org, ""
	}
	return "", ""
}

func (d *DataService) Upload(ctx context.Context, url string, body io.Reader, size int64) error {
	_, err := transfer.DoUpload(ctx, d.httpClient, url, body, size)
	return err
}

func (d *DataService) UploadPart(ctx context.Context, url string, body io.Reader, size int64) (string, error) {
	return transfer.DoUpload(ctx, d.httpClient, url, body, size)
}

func (d *DataService) Logger() transfer.TransferLogger {
	return d.logger
}
