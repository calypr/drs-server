package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/transfer"
)

// GetWriter returns an unsupported-operation error without creating an upload.
func (d *DataService) GetWriter(ctx context.Context, guid string) (io.WriteCloser, error) {
	return nil, fmt.Errorf("GetWriter not yet fully implemented for DataService")
}

func (d *DataService) Stat(ctx context.Context, guid string) (*transfer.ObjectMetadata, error) {
	if d.drs != nil {
		obj, err := d.drs.GetObject(ctx, guid)
		if err == nil {
			md := &transfer.ObjectMetadata{
				Size:     obj.Size,
				Provider: "drs",
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
		Provider:     "http",
		AcceptRanges: true,
		Size:         0,
		Checksums:    nil,
	}, nil
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
	if offset > 0 && resp.StatusCode == http.StatusOK {
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
	return transfer.GenericDownload(ctx, d.requestor, signedURL, rangeStart, rangeEnd)
}

func (d *DataService) ResolveUploadURL(ctx context.Context, guid, filename string, metadata common.FileMetadata, bucket string) (string, error) {
	organization, project := uploadScopeFromMetadata(metadata)
	resp, err := d.UploadURL(ctx, UploadURLRequest{
		FileID:       guid,
		Key:          filename,
		Organization: organization,
		Project:      project,
	})
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
	ctx, cancel := context.WithTimeout(ctx, common.DataTimeout)
	defer cancel()
	_, err := transfer.DoUpload(ctx, d.requestor, url, body, size)
	return err
}

func (d *DataService) UploadPart(ctx context.Context, url string, body io.Reader, size int64) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, common.DataTimeout)
	defer cancel()
	return transfer.DoUpload(ctx, d.requestor, url, body, size)
}

func (d *DataService) Name() string { return "syfon-data-service" }

func (d *DataService) Logger() transfer.TransferLogger {
	return d.logger
}

func (d *DataService) Validate(ctx context.Context, bucket string) error {
	return nil
}
