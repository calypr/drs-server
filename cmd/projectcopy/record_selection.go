package projectcopy

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/syfon/apigen/client/internalapi"
	"github.com/calypr/syfon/client/services"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/spf13/cobra"
)

type Scope struct {
	Organization string
	Project      string
}

func ParseScopeArg(raw, label string) (Scope, error) {
	parts := strings.Split(strings.TrimSpace(raw), "/")
	if len(parts) != 2 {
		return Scope{}, fmt.Errorf("invalid %s path %q: must be in format <organization>/<project-id>", label, raw)
	}
	scope := Scope{
		Organization: strings.TrimSpace(parts[0]),
		Project:      strings.TrimSpace(parts[1]),
	}
	if scope.Organization == "" || scope.Project == "" {
		return Scope{}, fmt.Errorf("%s scope must be non-empty and in <organization>/<project-id> format", label)
	}
	return scope, nil
}

func RecordsToCopy(ctx context.Context, cmd *cobra.Command, index *services.IndexService, srcScope Scope, individualDID string) ([]internalapi.InternalRecord, error) {
	if strings.TrimSpace(individualDID) == "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Retrieving records for project %s/%s...\n", srcScope.Organization, srcScope.Project)
		records, err := listAllProjectRecords(ctx, index, srcScope.Organization, srcScope.Project)
		if err != nil {
			return nil, fmt.Errorf("failed to list project records: %w", err)
		}
		return records, nil
	}

	did := strings.TrimSpace(individualDID)
	fmt.Fprintf(cmd.OutOrStdout(), "Retrieving individual record %s from project %s/%s...\n", did, srcScope.Organization, srcScope.Project)

	rec, err := index.Get(ctx, did)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve individual record %q: %w", did, err)
	}
	if !recordMatchesScope(rec.ControlledAccess, srcScope) {
		return nil, fmt.Errorf("record %s does not belong to source scope %s/%s", did, srcScope.Organization, srcScope.Project)
	}

	return []internalapi.InternalRecord{internalRecordFromResponse(rec)}, nil
}

func recordMatchesScope(controlledAccess *[]string, srcScope Scope) bool {
	resource, err := clientaccess.ResourcePath(srcScope.Organization, srcScope.Project)
	if err != nil {
		return false
	}
	for _, candidate := range clientaccess.NormalizeAccessResources(derefStringSlice(controlledAccess)) {
		if candidate == resource {
			return true
		}
	}
	return false
}

func derefStringSlice(values *[]string) []string {
	if values == nil {
		return nil
	}
	return *values
}

func internalRecordFromResponse(rec internalapi.InternalRecordResponse) internalapi.InternalRecord {
	return internalapi.InternalRecord{
		Did:              rec.Did,
		AccessMethods:    rec.AccessMethods,
		ControlledAccess: rec.ControlledAccess,
		CreatedTime:      rec.CreatedTime,
		Description:      rec.Description,
		Name:             rec.Name,
		Hashes:           rec.Hashes,
		Organization:     rec.Organization,
		Project:          rec.Project,
		Size:             rec.Size,
		UpdatedTime:      rec.UpdatedTime,
		Version:          rec.Version,
	}
}

func listAllProjectRecords(ctx context.Context, index *services.IndexService, org, project string) ([]internalapi.InternalRecord, error) {
	var records []internalapi.InternalRecord
	start := ""
	for {
		resp, err := index.List(ctx, services.ListRecordsOptions{
			Limit:        1000,
			Start:        start,
			Organization: org,
			ProjectID:    project,
		})
		if err != nil {
			return nil, err
		}
		if resp.Records == nil || len(*resp.Records) == 0 {
			break
		}
		pageCount := 0
		for _, rec := range *resp.Records {
			did := strings.TrimSpace(rec.Did)
			if did == "" {
				continue
			}
			records = append(records, rec)
			start = did
			pageCount++
		}
		if pageCount < 1000 {
			break
		}
	}
	return records, nil
}
