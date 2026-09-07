package internaldrs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/client/bucketapi"
	clientdrs "github.com/calypr/syfon/apigen/client/drs"
	clientinternalapi "github.com/calypr/syfon/apigen/client/internalapi"
	serverdrs "github.com/calypr/syfon/apigen/server/drs"
	serverinternalapi "github.com/calypr/syfon/apigen/server/internalapi"
	sycommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/api/apiutil"
	apimiddleware "github.com/calypr/syfon/internal/api/middleware"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/repair"
	"github.com/gofiber/fiber/v3"
)

func authorizeStorageCleanupScope(ctx context.Context, organization, project string, methods ...string) error {
	if !access.IsAuthzEnforced(ctx) {
		return nil
	}
	resource, err := sycommon.ResourcePath(organization, project)
	if err != nil {
		return err
	}
	if access.HasMethodAccess(ctx, methods[0], []string{"/programs", "/data_file"}) || access.HasAnyMethodAccess(ctx, []string{resource}, methods...) {
		return nil
	}
	return faults.ErrUnauthorized
}

func handleInternalScopeRepairAuditFiber(om *core.ObjectManager) fiber.Handler {
	svc := repair.NewService(scopeRepairIndexAdapter{om: om}, scopeRepairBucketsAdapter{om: om}, storageRepairInspector{om: om})
	return func(c fiber.Ctx) error {
		if apimiddleware.MissingGen3AuthHeader(c.Context()) {
			return c.SendStatus(fiber.StatusUnauthorized)
		}
		var req repair.Options
		if err := decodeStrictJSON(c.Body(), &req); err != nil {
			return apiutil.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
		}
		req.Organization = strings.TrimSpace(req.Organization)
		req.Project = strings.TrimSpace(req.Project)
		req.CheckStorage = true
		if req.Organization == "" || req.Project == "" {
			return apiutil.Reject(c, fiber.StatusBadRequest, "organization and project are required")
		}
		if err := authorizeStorageCleanupScope(c.Context(), req.Organization, req.Project, "read"); err != nil {
			return apiutil.HandleError(c, err)
		}
		report, err := svc.Audit(c.Context(), req)
		if err != nil {
			return apiutil.HandleError(c, err)
		}
		return c.JSON(report)
	}
}

func handleInternalScopeRepairApplyFiber(om *core.ObjectManager) fiber.Handler {
	svc := repair.NewService(scopeRepairIndexAdapter{om: om}, scopeRepairBucketsAdapter{om: om}, storageRepairInspector{om: om})
	return func(c fiber.Ctx) error {
		if apimiddleware.MissingGen3AuthHeader(c.Context()) {
			return c.SendStatus(fiber.StatusUnauthorized)
		}
		var req repair.Options
		if err := decodeStrictJSON(c.Body(), &req); err != nil {
			return apiutil.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
		}
		req.Organization = strings.TrimSpace(req.Organization)
		req.Project = strings.TrimSpace(req.Project)
		req.CheckStorage = true
		if req.Organization == "" || req.Project == "" {
			return apiutil.Reject(c, fiber.StatusBadRequest, "organization and project are required")
		}
		if err := authorizeStorageCleanupScope(c.Context(), req.Organization, req.Project, "read"); err != nil {
			return apiutil.HandleError(c, err)
		}
		if err := authorizeStorageCleanupScope(c.Context(), req.Organization, req.Project, "update"); err != nil {
			return apiutil.HandleError(c, err)
		}
		if _, err := om.CollapseProjectChecksumDuplicates(c.Context(), req.Organization, req.Project); err != nil {
			return apiutil.HandleError(c, err)
		}
		result, err := svc.Apply(c.Context(), req)
		if err != nil {
			return apiutil.HandleError(c, err)
		}
		return c.JSON(result)
	}
}

type scopeRepairIndexAdapter struct {
	om *core.ObjectManager
}

func (a scopeRepairIndexAdapter) List(ctx context.Context, opts repair.ListRecordsOptions) (clientinternalapi.ListRecordsResponse, error) {
	objects, err := a.om.ListPreparedObjectsPageByScope(ctx, strings.TrimSpace(opts.Organization), strings.TrimSpace(opts.Project), "read", strings.TrimSpace(opts.Start), opts.Limit, 0)
	if err != nil {
		return clientinternalapi.ListRecordsResponse{}, err
	}
	records := make([]clientinternalapi.InternalRecord, 0, len(objects))
	for _, obj := range objects {
		records = append(records, clientRecordFromServer(core.InternalObjectToInternalRecord(obj)))
	}
	return clientinternalapi.ListRecordsResponse{Records: &records}, nil
}

func (a scopeRepairIndexAdapter) Update(ctx context.Context, did string, rec clientinternalapi.InternalRecord) (clientinternalapi.InternalRecordResponse, error) {
	update, err := core.InternalRecordToInternalObject(serverRecordFromClient(rec), time.Now().UTC())
	if err != nil {
		return clientinternalapi.InternalRecordResponse{}, err
	}
	existing, err := a.om.GetObject(ctx, did, "update")
	if err != nil {
		return clientinternalapi.InternalRecordResponse{}, err
	}
	merged, err := core.MergeInternalObjectUpdate(*existing, update, did, time.Now().UTC())
	if err != nil {
		return clientinternalapi.InternalRecordResponse{}, err
	}
	if err := a.om.ReplaceObjects(ctx, []models.InternalObject{merged}); err != nil {
		return clientinternalapi.InternalRecordResponse{}, err
	}
	return clientRecordResponseFromServer(core.InternalObjectToInternalRecordResponse(merged)), nil
}

type scopeRepairBucketsAdapter struct {
	om *core.ObjectManager
}

func (a scopeRepairBucketsAdapter) List(ctx context.Context) (bucketapi.BucketsResponse, error) {
	creds, err := a.om.ListS3Credentials(ctx)
	if err != nil {
		return bucketapi.BucketsResponse{}, err
	}
	out := bucketapi.BucketsResponse{S3BUCKETS: map[string]bucketapi.BucketMetadata{}}
	for _, cred := range creds {
		out.S3BUCKETS[strings.TrimSpace(cred.Bucket)] = bucketapi.BucketMetadata{}
	}
	return out, nil
}

func (a scopeRepairBucketsAdapter) ListScopes(ctx context.Context, bucket string) ([]bucketapi.BucketScopeResponse, error) {
	scopes, err := a.om.ListBucketScopes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]bucketapi.BucketScopeResponse, 0)
	for _, scope := range scopes {
		if strings.TrimSpace(scope.Bucket) != strings.TrimSpace(bucket) {
			continue
		}
		path := ""
		if strings.TrimSpace(scope.PathPrefix) != "" {
			path = fmt.Sprintf("s3://%s/%s", scope.Bucket, strings.Trim(strings.TrimSpace(scope.PathPrefix), "/"))
		} else {
			path = fmt.Sprintf("s3://%s", scope.Bucket)
		}
		out = append(out, bucketapi.BucketScopeResponse{
			Organization: scope.Organization,
			ProjectId:    scope.ProjectID,
			Path:         &path,
		})
	}
	return out, nil
}

func clientRecordFromServer(rec serverinternalapi.InternalRecord) clientinternalapi.InternalRecord {
	out := clientinternalapi.InternalRecord{
		Did:              rec.Did,
		AccessMethods:    clientAccessMethodsFromServer(rec.AccessMethods),
		ControlledAccess: rec.ControlledAccess,
		Size:             rec.Size,
		CreatedTime:      rec.CreatedTime,
		Description:      rec.Description,
		Name:             rec.Name,
		NameAliases:      rec.NameAliases,
		Version:          rec.Version,
		UpdatedTime:      rec.UpdatedTime,
		Hashes:           (*clientinternalapi.HashInfo)(rec.Hashes),
		Organization:     rec.Organization,
		Project:          rec.Project,
	}
	return out
}

func serverRecordFromClient(rec clientinternalapi.InternalRecord) serverinternalapi.InternalRecord {
	out := serverinternalapi.InternalRecord{
		Did:              rec.Did,
		AccessMethods:    serverAccessMethodsFromClient(rec.AccessMethods),
		ControlledAccess: rec.ControlledAccess,
		Size:             rec.Size,
		CreatedTime:      rec.CreatedTime,
		Description:      rec.Description,
		Name:             rec.Name,
		NameAliases:      rec.NameAliases,
		Version:          rec.Version,
		UpdatedTime:      rec.UpdatedTime,
		Hashes:           (*serverinternalapi.HashInfo)(rec.Hashes),
		Organization:     rec.Organization,
		Project:          rec.Project,
	}
	return out
}

func clientRecordResponseFromServer(rec serverinternalapi.InternalRecordResponse) clientinternalapi.InternalRecordResponse {
	out := clientinternalapi.InternalRecordResponse{
		Did:              rec.Did,
		AccessMethods:    clientAccessMethodsFromServer(rec.AccessMethods),
		ControlledAccess: rec.ControlledAccess,
		Size:             rec.Size,
		CreatedTime:      rec.CreatedTime,
		Description:      rec.Description,
		Name:             rec.Name,
		NameAliases:      rec.NameAliases,
		Version:          rec.Version,
		UpdatedTime:      rec.UpdatedTime,
		Hashes:           (*clientinternalapi.HashInfo)(rec.Hashes),
		Organization:     rec.Organization,
		Project:          rec.Project,
	}
	return out
}

func clientAccessMethodsFromServer(in *[]serverdrs.AccessMethod) *[]clientdrs.AccessMethod {
	if in == nil {
		return nil
	}
	out := make([]clientdrs.AccessMethod, 0, len(*in))
	for _, method := range *in {
		var accessURL *struct {
			Headers *[]string `json:"headers,omitempty"`
			Url     string    `json:"url"`
		}
		if method.AccessUrl != nil {
			accessURL = &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{
				Headers: method.AccessUrl.Headers,
				Url:     method.AccessUrl.Url,
			}
		}
		out = append(out, clientdrs.AccessMethod{
			AccessId:  method.AccessId,
			AccessUrl: accessURL,
			Region:    method.Region,
			Type:      clientdrs.AccessMethodType(method.Type),
		})
	}
	return &out
}

func serverAccessMethodsFromClient(in *[]clientdrs.AccessMethod) *[]serverdrs.AccessMethod {
	if in == nil {
		return nil
	}
	out := make([]serverdrs.AccessMethod, 0, len(*in))
	for _, method := range *in {
		var accessURL *struct {
			Headers *[]string `json:"headers,omitempty"`
			Url     string    `json:"url"`
		}
		if method.AccessUrl != nil {
			accessURL = &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{
				Headers: method.AccessUrl.Headers,
				Url:     method.AccessUrl.Url,
			}
		}
		out = append(out, serverdrs.AccessMethod{
			AccessId:  method.AccessId,
			AccessUrl: accessURL,
			Region:    method.Region,
			Type:      serverdrs.AccessMethodType(method.Type),
		})
	}
	return &out
}

type storageRepairInspector struct {
	om *core.ObjectManager
}

var _ repair.StorageInspector = storageRepairInspector{}

func (r storageRepairInspector) Inspect(ctx context.Context, req repair.StorageInspectRequest) (repair.StorageInspectResult, error) {
	meta, err := r.om.InspectStorageObject(ctx, core.InspectStorageRequest{
		Organization: req.Organization,
		Project:      req.Project,
		ObjectURL:    req.ObjectURL,
	})
	if err != nil {
		var inspectErr *core.StorageInspectError
		if errors.As(err, &inspectErr) && inspectErr.Kind == core.StorageInspectObjectNotFound {
			return repair.StorageInspectResult{}, repair.ErrStorageObjectNotFound
		}
		return repair.StorageInspectResult{}, err
	}
	return repair.StorageInspectResult{ObjectURL: meta.ObjectURL}, nil
}
