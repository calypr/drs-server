package copyproject

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	drsapi "github.com/calypr/syfon/apigen/client/drs"
	"github.com/calypr/syfon/apigen/client/internalapi"
	"github.com/calypr/syfon/client/services"
	transferdownload "github.com/calypr/syfon/client/transfer/download"
	"github.com/calypr/syfon/client/transfer/upload"
	"github.com/calypr/syfon/cmd/transferprogress"
	syfoncommon "github.com/calypr/syfon/common"
	"github.com/spf13/cobra"
)

func copyRecord(ctx context.Context, cmd *cobra.Command, sourceClient, targetClient services.SyfonClient, rec internalapi.InternalRecord, targetBucket, targetProjectPath string, dstResource string, current, total int, tempDir string) error {
	did := rec.Did
	fileName := ""
	if rec.Name != nil {
		fileName = *rec.Name
	}
	size := int64(0)
	if rec.Size != nil {
		size = *rec.Size
	}
	checksum := ""
	if rec.Hashes != nil {
		if sha256Val, ok := (*rec.Hashes)["sha256"]; ok {
			checksum = sha256Val
		}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "[%d/%d] Copying %s (size: %d, name: %s)...\n", current, total, did, size, fileName)

	tempFile, err := os.CreateTemp(tempDir, "copy-project-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file %s: %w", tempPath, err)
	}
	defer os.Remove(tempPath)

	progressName := fileName
	if strings.TrimSpace(progressName) == "" {
		progressName = filepath.Base(tempPath)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Downloading %s -> %s", did, tempPath)
	if size > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), " (%s)", upload.FormatSize(size))
	}
	fmt.Fprintln(cmd.OutOrStdout())

	downloadProgress := transferprogress.New(cmd.OutOrStdout(), filepath.Base(progressName), size)
	downloadProgress.Start()
	downloadCtx := transferprogress.WithProgress(ctx, did, downloadProgress)
	if err := transferdownload.DownloadFile(downloadCtx, sourceClient.Data(), did, tempPath); err != nil {
		downloadProgress.Abort()
		return fmt.Errorf("failed to download file %s: %w", did, err)
	}
	downloadProgress.Finish()

	drsObj := &drsapi.DrsObject{
		Id:   did,
		Name: &fileName,
		Size: size,
		Checksums: []drsapi.Checksum{
			{Type: "sha256", Checksum: checksum},
		},
		ControlledAccess: &[]string{dstResource},
	}

	uploadKey := preferredUploadKey(rec.AccessMethods, checksum, fileName, tempPath)
	targetObjectURL := scopedObjectURL(targetProjectPath, targetBucket, uploadKey)

	fmt.Fprintf(cmd.OutOrStdout(), "Uploading %s -> %s", did, targetObjectURL)
	if size > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), " (%s)", upload.FormatSize(size))
	}
	fmt.Fprintln(cmd.OutOrStdout())

	uploadProgress := transferprogress.New(cmd.OutOrStdout(), filepath.Base(progressName), size)
	uploadProgress.Start()
	uploadCtx := transferprogress.WithProgress(ctx, did, uploadProgress)
	if _, err := upload.RegisterFile(uploadCtx, targetClient.Data(), targetClient.DRS(), drsObj, tempPath, targetBucket); err != nil {
		uploadProgress.Abort()
		return fmt.Errorf("failed to upload file %s to target bucket %q: %w", did, targetBucket, err)
	}
	uploadProgress.Finish()

	targetAccessMethod := drsapi.AccessMethod{
		Type: drsapi.AccessMethodType(storageSchemeFromURL(targetObjectURL)),
		AccessUrl: &struct {
			Headers *[]string `json:"headers,omitempty"`
			Url     string    `json:"url"`
		}{Url: targetObjectURL},
	}

	registerReq := drsapi.RegisterObjectsJSONRequestBody{
		Candidates: []drsapi.DrsObjectCandidate{{
			Name:             drsObj.Name,
			Size:             drsObj.Size,
			Checksums:        drsObj.Checksums,
			Aliases:          &[]string{"id:" + did},
			AccessMethods:    &[]drsapi.AccessMethod{targetAccessMethod},
			ControlledAccess: &[]string{dstResource},
			Description:      drsObj.Description,
			MimeType:         drsObj.MimeType,
			Version:          drsObj.Version,
		}},
	}
	if _, err := targetClient.DRS().RegisterObjects(ctx, registerReq); err != nil {
		return fmt.Errorf("failed to update DRS metadata for DID %s: %w", did, err)
	}

	authzOrg, authzProject := pathScope(dstResource)
	authzMap := syfoncommon.AuthzMapFromScope(authzOrg, authzProject)
	if err := targetClient.Index().Upsert(ctx, did, targetObjectURL, fileName, size, checksum, authzMap); err != nil {
		return fmt.Errorf("failed to sync index record for DID %s: %w", did, err)
	}
	if _, err := targetClient.DRS().UpdateObjectAccessMethods(ctx, did, []drsapi.AccessMethod{targetAccessMethod}); err != nil {
		return fmt.Errorf("failed to replace access methods for DID %s: %w", did, err)
	}

	return nil
}

func preferredUploadKey(accessMethods *[]drsapi.AccessMethod, checksum, fileName, filePath string) string {
	if strings.TrimSpace(checksum) != "" {
		return strings.TrimSpace(checksum)
	}
	key := path.Base(filePath)
	if accessMethods != nil {
		for _, am := range *accessMethods {
			if am.AccessUrl == nil || strings.TrimSpace(am.AccessUrl.Url) == "" {
				continue
			}
			parts := strings.Split(strings.TrimSpace(am.AccessUrl.Url), "/")
			if candidate := strings.TrimSpace(parts[len(parts)-1]); candidate != "" {
				key = candidate
				break
			}
		}
	}
	if strings.TrimSpace(fileName) != "" && key == path.Base(filePath) {
		key = strings.TrimSpace(fileName)
	}
	return key
}
