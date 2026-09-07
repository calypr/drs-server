package upload

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	drsapi "github.com/calypr/syfon/apigen/client/drs"
	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/transfer"
	syfoncommon "github.com/calypr/syfon/common"
)

type MetadataClient interface {
	GetObject(ctx context.Context, objectID string) (drsapi.DrsObject, error)
	RegisterObjects(ctx context.Context, req drsapi.RegisterObjectsJSONRequestBody) (drsapi.N201ObjectsCreated, error)
	UpdateObjectAccessMethods(ctx context.Context, objectID string, accessMethods []drsapi.AccessMethod) (drsapi.DrsObject, error)
}

// RegisterFile orchestrates the full registration and upload flow:
// 1. Build a DRS object from the local file (if not provided).
// 2. Register metadata with the DRS server via the provided drs.Client.
// 3. Upload the file content via the provided Backend.
func RegisterFile(ctx context.Context, bk UploadBackend, dc MetadataClient, drsObject *drsapi.DrsObject, filePath string, bucketName string) (*drsapi.DrsObject, error) {
	// 1. Ensure we have a valid OID/metadata.
	// (Logic ported and generalized from git-drs/client/local/local_client.go)

	if drsObject == nil {
		return nil, fmt.Errorf("drsObject must be provided (containing at least checksums/size)")
	}
	requestedID := strings.TrimSpace(drsObject.Id)
	if requestedID == "" {
		return nil, fmt.Errorf("drsObject must include an id")
	}
	storageID := requestedID
	metadataControlledAccess := []string(nil)
	if drsObject.ControlledAccess != nil {
		metadataControlledAccess = append([]string(nil), (*drsObject.ControlledAccess)...)
	}
	metadata := common.FileMetadata{
		Authorizations: syfoncommon.ControlledAccessToAuthzMap(syfoncommon.NormalizeAccessResources(metadataControlledAccess)),
	}

	// 2. Determine upload filename/key
	// Content-Addressable Storage (CAS): We prioritize the SHA256 hash as the storage key.
	uploadFilename := filepath.Base(filePath)
	for _, c := range drsObject.Checksums {
		if strings.ToLower(c.Type) == "sha256" {
			uploadFilename = c.Checksum
			break
		}
	}

	if drsObject.AccessMethods != nil && len(*drsObject.AccessMethods) > 0 {
		for _, am := range *drsObject.AccessMethods {
			if am.Type == "s3" || am.Type == "gs" {
				if am.AccessUrl != nil && am.AccessUrl.Url == "" {
					continue
				}
				if am.AccessUrl != nil {
					parts := strings.Split(am.AccessUrl.Url, "/")
					if candidate := parts[len(parts)-1]; candidate != "" {
						uploadFilename = candidate
						break
					}
				}
			}
		}
	}

	// 3. Perform Upload
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file for upload: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	threshold := int64(4.5 * float64(common.GB)) // Default threshold with safety buffer
	canonicalInput := ""
	if stat.Size() < threshold {
		uploadURL, err := bk.ResolveUploadURL(ctx, storageID, uploadFilename, metadata, bucketName)
		if err != nil {
			return nil, fmt.Errorf("failed to get upload URL: %w", err)
		}
		if err := UploadSingle(ctx, bk, bk.Logger(), filePath, uploadFilename, storageID, bucketName, metadata, false); err != nil {
			return nil, fmt.Errorf("upload failed: %w", err)
		}
		canonicalInput = uploadURL
	} else {
		if err := Upload(ctx, bk, filePath, uploadFilename, storageID, bucketName, metadata, false, true); err != nil {
			return nil, fmt.Errorf("multipart upload failed: %w", err)
		}
		canonicalInput = "s3://" + strings.Trim(strings.TrimSpace(bucketName), "/") + "/" + strings.Trim(strings.TrimSpace(uploadFilename), "/")
	}

	// 4. Finalize registration with a concrete access location.
	canonical, err := bk.CanonicalObjectURL(canonicalInput, bucketName, storageID)
	if err != nil || canonical == "" {
		if err == nil {
			err = fmt.Errorf("empty canonical URL returned")
		}
		return nil, fmt.Errorf("failed to derive canonical object URL: %w", err)
	}

	current, getErr := dc.GetObject(ctx, drsObject.Id)
	if getErr != nil {
		current = *drsObject
	}
	controlledAccess := drsObject.ControlledAccess
	if controlledAccess == nil {
		controlledAccess = current.ControlledAccess
	}

	u, parseErr := url.Parse(canonical)
	if parseErr != nil || u.Scheme == "" {
		return nil, fmt.Errorf("failed to determine provider type from canonical URL: %s", canonical)
	}
	pType := u.Scheme

	am := drsapi.AccessMethod{
		Type: drsapi.AccessMethodType(pType),
		AccessUrl: &struct {
			Headers *[]string `json:"headers,omitempty"`
			Url     string    `json:"url"`
		}{Url: canonical},
	}

	found := false
	if current.AccessMethods != nil {
		for i, existing := range *current.AccessMethods {
			if (existing.AccessUrl != nil && existing.AccessUrl.Url == canonical) || (string(existing.Type) == pType && (existing.AccessUrl == nil || existing.AccessUrl.Url == "")) {
				(*current.AccessMethods)[i] = am
				found = true
				break
			}
		}
	}
	if !found {
		if current.AccessMethods == nil {
			current.AccessMethods = &[]drsapi.AccessMethod{}
		}
		*current.AccessMethods = append(*current.AccessMethods, am)
	}

	requestedAlias := "id:" + requestedID
	finalAliases := []string{requestedAlias}
	if drsObject.Aliases != nil {
		finalAliases = append(finalAliases, *drsObject.Aliases...)
	}

	candidates := []drsapi.DrsObjectCandidate{{
		Name:             drsObject.Name,
		Size:             drsObject.Size,
		Checksums:        drsObject.Checksums,
		Aliases:          &finalAliases,
		AccessMethods:    current.AccessMethods,
		ControlledAccess: controlledAccess,
		Description:      drsObject.Description,
		MimeType:         drsObject.MimeType,
		Version:          drsObject.Version,
	}}

	res, err := dc.RegisterObjects(ctx, drsapi.RegisterObjectsJSONRequestBody{Candidates: candidates})
	if err != nil {
		return nil, err
	}

	finalID := drsObject.Id
	if len(res.Objects) > 0 && strings.TrimSpace(res.Objects[0].Id) != "" {
		finalID = strings.TrimSpace(res.Objects[0].Id)
	}

	if current.AccessMethods != nil && len(*current.AccessMethods) > 0 {
		updated, err := dc.UpdateObjectAccessMethods(ctx, finalID, *current.AccessMethods)
		if err != nil {
			return nil, fmt.Errorf("persist finalized access methods: %w", err)
		}
		return &updated, nil
	}

	if len(res.Objects) > 0 {
		obj := res.Objects[0]
		return &obj, nil
	}
	drsObject.AccessMethods = current.AccessMethods
	drsObject.ControlledAccess = controlledAccess
	return drsObject, nil
}

type UploadBackend interface {
	transfer.Uploader
	transfer.MultipartBackend
}
