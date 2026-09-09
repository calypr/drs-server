package objects

import (
	"context"
	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"sort"
	"strings"
)

const maxDeniedAccessResources = 25

func (s *Service) RequireObjectResources(ctx context.Context, method string, resources []string) error {
	if strings.TrimSpace(method) == "" {
		return nil
	}
	if access.HasObjectMethodAccess(ctx, method, resources) {
		return nil
	}
	return errorapi.ErrAccessDenied
}

func requireScopeMethod(ctx context.Context, organization, project, method string) error {
	resource, err := clientaccess.ResourcePath(organization, project)
	if err != nil {
		return err
	}
	if strings.TrimSpace(resource) == "" {
		return errorapi.ErrAccessDenied
	}
	if access.HasObjectMethodAccess(ctx, method, []string{resource}) {
		return nil
	}
	return errorapi.ErrAccessDenied
}

func requireObjectMethod(ctx context.Context, obj *Record, method string) error {
	if hasObjectMethod(ctx, obj, method) {
		return nil
	}
	return errorapi.ErrAccessDenied
}

func requireAllObjectMethod(ctx context.Context, obj *Record, method string) error {
	resources := AccessResources(obj)
	if len(resources) == 0 {
		if access.HasObjectMethodAccess(ctx, method, resources) {
			return nil
		}
		return errorapi.ErrAccessDenied
	}
	if access.HasMethodAccess(ctx, method, resources) {
		return nil
	}
	return errorapi.ErrAccessDenied
}

func hasObjectMethod(ctx context.Context, obj *Record, method string) bool {
	method = strings.TrimSpace(method)
	if method == "" {
		return true
	}
	if strings.EqualFold(method, objectMethodRead) && obj != nil && obj.PublicRead {
		return true
	}
	if strings.EqualFold(method, objectMethodRead) && obj != nil && obj.PublicReadPolicyKnown && len(AccessResources(obj)) == 0 {
		return false
	}
	return access.HasObjectMethodAccess(ctx, method, AccessResources(obj))
}

func bulkObjectMethodError(ctx context.Context, objs []Record, method string) error {
	resources := make(map[string]struct{})
	var firstDeniedID string
	deniedRecords := 0
	for i := range objs {
		if hasObjectMethod(ctx, &objs[i], method) {
			continue
		}
		deniedRecords++
		if firstDeniedID == "" {
			firstDeniedID = string(objs[i].Id)
		}
		for _, resource := range AccessResources(&objs[i]) {
			if strings.TrimSpace(resource) == "" {
				continue
			}
			resources[resource] = struct{}{}
		}
	}
	if deniedRecords == 0 {
		return nil
	}

	resourceList := make([]string, 0, len(resources))
	for resource := range resources {
		resourceList = append(resourceList, resource)
	}
	sort.Strings(resourceList)

	truncated := 0
	if len(resourceList) > maxDeniedAccessResources {
		truncated = len(resourceList) - maxDeniedAccessResources
		resourceList = resourceList[:maxDeniedAccessResources]
	}

	return &access.AuthorizationError{
		Method:             method,
		RecordID:           firstDeniedID,
		Resources:          resourceList,
		DeniedRecords:      deniedRecords,
		TotalRecords:       len(objs),
		TruncatedResources: truncated,
	}
}
