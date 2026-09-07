package upload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/calypr/syfon/apigen/client/bucketapi"
	drsapi "github.com/calypr/syfon/apigen/client/drs"
	syfonclient "github.com/calypr/syfon/client/services"
	"github.com/calypr/syfon/client/transfer/upload"
	"github.com/calypr/syfon/cmd/cliauth"
	"github.com/calypr/syfon/cmd/transferprogress"

	clientaccess "github.com/calypr/syfon/client/access"
	intobjects "github.com/calypr/syfon/internal/objects"
	"github.com/spf13/cobra"
)

var (
	uploadFile      string
	uploadDID       string
	uploadOrg       string
	uploadProject   string
	uploadOverwrite bool
)

var Cmd = &cobra.Command{
	Use:   "upload",
	Short: "Upload a file and register/update its DRS record",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if strings.TrimSpace(uploadFile) == "" {
			return fmt.Errorf("--file is required")
		}

		srcPath := strings.TrimSpace(uploadFile)
		info, err := os.Stat(srcPath)
		if err != nil {
			return fmt.Errorf("stat source file: %w", err)
		}
		if info.IsDir() {
			return fmt.Errorf("--file must be a regular file")
		}

		org := strings.TrimSpace(uploadOrg)
		if org == "" {
			return fmt.Errorf("--org is required")
		}
		project := strings.TrimSpace(uploadProject)

		c, err := cliauth.NewServerClient(cmd)
		if err != nil {
			return err
		}

		bucketName := ""
		if buckets, listErr := c.Buckets().List(ctx); listErr != nil {
			return fmt.Errorf("resolve bucket for scope: %w", listErr)
		} else {
			resolvedBucket, resolveErr := resolveUploadBucketForScope(buckets, org, project)
			if resolveErr != nil {
				return resolveErr
			}
			bucketName = resolvedBucket
		}

		// Calculate SHA256 hash so omitted DIDs can be minted deterministically from content+scope.
		fileBytes, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("read file for hashing: %w", err)
		}
		hash := sha256.Sum256(fileBytes)
		checksum := hex.EncodeToString(hash[:])

		recordPath, err := uploadRecordPath(srcPath)
		if err != nil {
			return err
		}
		name := filepath.Base(srcPath)
		authzMap := clientaccess.AuthzMapFromScope(org, project)
		did := strings.TrimSpace(uploadDID)
		if did == "" {
			if project == "" {
				return fmt.Errorf("--project is required when --did is omitted")
			}
			minted, mintErr := intobjects.MintRecordIDFromChecksum(checksum, clientaccess.AuthzMapToControlledAccess(authzMap))
			if mintErr != nil {
				return mintErr
			}
			did = string(minted)
		}

		am := drsapi.AccessMethod{Type: "s3"}
		drsObj := &drsapi.DrsObject{
			Id:   did,
			Name: &name,
			Size: info.Size(),
			Checksums: []drsapi.Checksum{
				{Type: "sha256", Checksum: checksum},
			},
			AccessMethods: &[]drsapi.AccessMethod{am},
		}
		if authzMap != nil {
			controlled := clientaccess.AuthzMapToControlledAccess(authzMap)
			drsObj.ControlledAccess = &controlled
		}
		overwriteWarning, err := ensureWritableDID(ctx, c.DRS(), did, uploadOverwrite)
		if err != nil {
			return err
		}
		if overwriteWarning != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", overwriteWarning)
		}

		// Register and upload using the SDK's orchestrator
		fmt.Fprintf(cmd.OutOrStdout(), "Uploading %s -> DID: %s\n", srcPath, did)

		progress := transferprogress.New(cmd.OutOrStdout(), filepath.Base(srcPath), info.Size())
		progress.Start()
		uploadCtx := transferprogress.WithProgress(ctx, did, progress)

		registered, err := upload.RegisterFile(uploadCtx, c.Data(), c.DRS(), drsObj, srcPath, bucketName)
		if err != nil {
			progress.Abort()
			return fmt.Errorf("upload failed: %w", err)
		}
		progress.Finish()

		finalID := did
		if registered != nil && strings.TrimSpace(registered.Id) != "" {
			finalID = strings.TrimSpace(registered.Id)
		}
		if registered != nil && registered.AccessMethods != nil && len(*registered.AccessMethods) > 0 {
			objectURL := ""
			for _, am := range *registered.AccessMethods {
				if am.AccessUrl != nil && strings.TrimSpace(am.AccessUrl.Url) != "" {
					objectURL = strings.TrimSpace(am.AccessUrl.Url)
					break
				}
			}
			if objectURL != "" {
				if err := c.Index().Upsert(ctx, finalID, objectURL, recordPath, info.Size(), checksum, authzMap); err != nil {
					return fmt.Errorf("sync index record: %w", err)
				}
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "\nsuccessfully uploaded %s\n", finalID)
		fmt.Fprintf(cmd.OutOrStdout(), "requested DID: %s\n", did)
		return nil
	},
}

func init() {
	Cmd.Flags().StringVar(&uploadFile, "file", "", "Path to source file")
	Cmd.Flags().StringVar(&uploadDID, "did", "", "Optional object DID (generated deterministically from sha256 + project scope when omitted)")
	Cmd.Flags().StringVar(&uploadOrg, "org", "", "Required organization for the authz scope")
	Cmd.Flags().StringVar(&uploadProject, "project", "", "Project for the authz scope (required when --did is omitted)")
	Cmd.Flags().BoolVar(&uploadOverwrite, "overwrite", false, "Allow replacing an existing DID's record and storage mapping")
}

type didLookup interface {
	GetObject(ctx context.Context, objectID string) (drsapi.DrsObject, error)
}

type didReplacer interface {
	didLookup
	DeleteObject(ctx context.Context, objectID string, deleteStorageData bool) error
}

func ensureWritableDID(ctx context.Context, drs didReplacer, did string, overwrite bool) (string, error) {
	_, err := drs.GetObject(ctx, did)
	if err == nil {
		if overwrite {
			if err := drs.DeleteObject(ctx, did, false); err != nil {
				return "", fmt.Errorf("delete existing DID %s for overwrite: %w", did, err)
			}
			return fmt.Sprintf("DID %s already existed; removing the existing record metadata and rewriting it with the new upload. Storage bytes are not deleted as part of this overwrite.", did), nil
		}
		return "", fmt.Errorf("object DID %s already exists; pass --overwrite to replace it", did)
	}
	if errors.Is(err, syfonclient.ErrObjectNotFound) {
		return "", nil
	}
	return "", fmt.Errorf("check existing DID %s: %w", did, err)
}

func resolveUploadBucketForScope(buckets bucketapi.BucketsResponse, org, project string) (string, error) {
	org = strings.TrimSpace(org)
	project = strings.TrimSpace(project)
	scope, err := clientaccess.ResourcePath(org, project)
	if err != nil {
		return "", err
	}
	orgScope, err := clientaccess.ResourcePath(org, "")
	if err != nil {
		return "", err
	}

	exactMatches := make([]string, 0)
	orgWideMatches := make([]string, 0)
	for bucketName, meta := range buckets.S3BUCKETS {
		for _, resource := range normalizedBucketPrograms(meta) {
			switch resource {
			case scope:
				exactMatches = append(exactMatches, bucketName)
			case orgScope:
				orgWideMatches = append(orgWideMatches, bucketName)
			}
		}
	}

	sort.Strings(exactMatches)
	sort.Strings(orgWideMatches)
	exactMatches = uniqueStrings(exactMatches)
	orgWideMatches = uniqueStrings(orgWideMatches)

	if len(exactMatches) == 1 {
		return exactMatches[0], nil
	}
	if len(exactMatches) > 1 {
		return "", fmt.Errorf("scope %s maps to multiple buckets: %s", scope, strings.Join(exactMatches, ", "))
	}
	if project == "" {
		if len(orgWideMatches) == 1 {
			return orgWideMatches[0], nil
		}
		if len(orgWideMatches) > 1 {
			return "", fmt.Errorf("organization scope %s maps to multiple buckets: %s", orgScope, strings.Join(orgWideMatches, ", "))
		}
		return "", fmt.Errorf("no bucket configured for organization scope %s", orgScope)
	}
	if len(orgWideMatches) == 1 {
		return orgWideMatches[0], nil
	}
	if len(orgWideMatches) > 1 {
		return "", fmt.Errorf("project scope %s has no exact bucket mapping and organization scope %s maps to multiple buckets: %s", scope, orgScope, strings.Join(orgWideMatches, ", "))
	}
	return "", fmt.Errorf("no bucket configured for project scope %s or organization scope %s", scope, orgScope)
}

func normalizedBucketPrograms(meta bucketapi.BucketMetadata) []string {
	if meta.Programs == nil {
		return nil
	}
	return clientaccess.NormalizeAccessResources(*meta.Programs)
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func uploadRecordPath(srcPath string) (string, error) {
	cleaned := filepath.Clean(strings.TrimSpace(srcPath))
	if cleaned == "" || cleaned == "." {
		return "", fmt.Errorf("invalid upload file path %q", srcPath)
	}
	if !filepath.IsAbs(cleaned) {
		return filepath.ToSlash(cleaned), nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve working directory for upload path: %w", err)
	}
	normalizedCWD := normalizeComparablePath(cwd)
	normalizedPath := normalizeComparablePath(cleaned)
	rel, err := filepath.Rel(normalizedCWD, normalizedPath)
	if err == nil {
		rel = filepath.Clean(rel)
		if rel != "." && rel != "" && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel), nil
		}
	}

	return filepath.Base(cleaned), nil
}

func normalizeComparablePath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}
