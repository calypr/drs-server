package records

import (
	"context"
	"fmt"
	"sort"
	"strings"

	objectmodel "github.com/calypr/syfon/internal/objects"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/faults"
)

const maxDeniedAccessResources = 25

func (m *mutationService) UpdateObjectAccessMethods(ctx context.Context, objectID string, accessMethods []objectmodel.AccessMethod) error {
	obj, err := m.recordReader.GetObject(ctx, objectID)
	if err != nil {
		return err
	}
	if err := requireAllObjectMethod(ctx, obj, objectMethodUpdate); err != nil {
		return err
	}
	return m.accessMethods.UpdateObjectAccessMethods(ctx, objectID, accessMethods)
}

func (m *mutationService) BulkUpdateAccessMethods(ctx context.Context, updates map[string][]objectmodel.AccessMethod) error {
	if len(updates) == 0 {
		return nil
	}

	ids := make([]string, 0, len(updates))
	for objectID := range updates {
		ids = append(ids, objectID)
	}
	objects, err := m.recordReader.GetBulkObjects(ctx, ids)
	if err != nil {
		return err
	}
	byID := make(map[string]*objectmodel.Record, len(objects))
	for i := range objects {
		byID[string(objects[i].Id)] = &objects[i]
	}
	for _, objectID := range ids {
		obj, ok := byID[objectID]
		if !ok {
			return faults.ErrNotFound
		}
		if err := requireAllObjectMethod(ctx, obj, objectMethodUpdate); err != nil {
			return err
		}
	}
	return m.accessMethods.BulkUpdateAccessMethods(ctx, updates)
}

func (m *mutationService) RemoveObjectControlledAccess(ctx context.Context, objectID, resource string) (*objectmodel.Record, error) {
	obj, err := m.recordReader.GetObject(ctx, objectID)
	if err != nil {
		return nil, err
	}
	if err := requireObjectMethod(ctx, obj, objectMethodUpdate); err != nil {
		return nil, err
	}

	normalized := clientaccess.NormalizeAccessResources([]string{resource})
	if len(normalized) == 0 {
		return nil, fmt.Errorf("resource is required")
	}
	resource = normalized[0]

	resources := objectmodel.AccessResources(obj)
	found := false
	for _, existing := range resources {
		if strings.TrimSpace(existing) == resource {
			found = true
		}
	}
	if !found {
		return nil, faults.ErrNotFound
	}

	if err := m.accessPolicy.RemoveObjectControlledAccess(ctx, objectID, resource); err != nil {
		return nil, err
	}

	updated, err := m.recordReader.GetObject(ctx, objectID)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (m *mutationService) RequireObjectResources(ctx context.Context, method string, resources []string) error {
	if strings.TrimSpace(method) == "" {
		return nil
	}
	if access.HasObjectMethodAccess(ctx, method, resources) {
		return nil
	}
	return faults.ErrUnauthorized
}

func requireScopeMethod(ctx context.Context, organization, project, method string) error {
	resource, err := clientaccess.ResourcePath(organization, project)
	if err != nil {
		return err
	}
	if strings.TrimSpace(resource) == "" {
		return faults.ErrUnauthorized
	}
	if access.HasObjectMethodAccess(ctx, method, []string{resource}) {
		return nil
	}
	return faults.ErrUnauthorized
}

func requireObjectMethod(ctx context.Context, obj *objectmodel.Record, method string) error {
	if hasObjectMethod(ctx, obj, method) {
		return nil
	}
	return faults.ErrUnauthorized
}

func requireAllObjectMethod(ctx context.Context, obj *objectmodel.Record, method string) error {
	resources := objectmodel.AccessResources(obj)
	if len(resources) == 0 {
		if access.HasObjectMethodAccess(ctx, method, resources) {
			return nil
		}
		return faults.ErrUnauthorized
	}
	if access.HasMethodAccess(ctx, method, resources) {
		return nil
	}
	return faults.ErrUnauthorized
}

func hasObjectMethod(ctx context.Context, obj *objectmodel.Record, method string) bool {
	method = strings.TrimSpace(method)
	if method == "" {
		return true
	}
	if strings.EqualFold(method, objectMethodRead) && obj != nil && obj.PublicRead {
		return true
	}
	if strings.EqualFold(method, objectMethodRead) && obj != nil && obj.PublicReadPolicyKnown && len(objectmodel.AccessResources(obj)) == 0 {
		return false
	}
	return access.HasObjectMethodAccess(ctx, method, objectmodel.AccessResources(obj))
}

func bulkObjectMethodError(ctx context.Context, objs []objectmodel.Record, method string) error {
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
		for _, resource := range objectmodel.AccessResources(&objs[i]) {
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
