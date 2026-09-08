package copyproject

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/bucketapi"
	"github.com/calypr/syfon/client/services"
	"github.com/calypr/syfon/cmd/projectcopy"
	"github.com/spf13/cobra"
)

type copyBucketAPI struct {
	bucketapi.ClientWithResponsesInterface
	scopes   map[string][]bucketapi.BucketScopeResponse
	listErr  map[string]error
	addErr   []error
	addCalls []copyScopeAddCall
}

type copyScopeAddCall struct {
	bucket  string
	request bucketapi.AddBucketScopeRequest
}

func (f *copyBucketAPI) ListBucketScopesWithResponse(_ context.Context, bucket string, _ ...bucketapi.RequestEditorFn) (*bucketapi.ListBucketScopesResp, error) {
	if err := f.listErr[bucket]; err != nil {
		return nil, err
	}
	scopes := append([]bucketapi.BucketScopeResponse(nil), f.scopes[bucket]...)
	return &bucketapi.ListBucketScopesResp{
		HTTPResponse: &http.Response{StatusCode: http.StatusOK},
		JSON200:      &scopes,
	}, nil
}

func (f *copyBucketAPI) AddBucketScopeWithResponse(_ context.Context, bucket string, body bucketapi.AddBucketScopeJSONRequestBody, _ ...bucketapi.RequestEditorFn) (*bucketapi.AddBucketScopeResp, error) {
	f.addCalls = append(f.addCalls, copyScopeAddCall{bucket: bucket, request: bucketapi.AddBucketScopeRequest(body)})
	var err error
	if len(f.addCalls) <= len(f.addErr) {
		err = f.addErr[len(f.addCalls)-1]
	}
	if err != nil {
		return nil, err
	}
	return &bucketapi.AddBucketScopeResp{HTTPResponse: &http.Response{StatusCode: http.StatusCreated}}, nil
}

func TestResolveCopyScopes(t *testing.T) {
	srcScope := projectcopy.Scope{Organization: "source-org", Project: "source-project"}
	dstScope := projectcopy.Scope{Organization: "target-org", Project: "target-project"}
	sourceProject := bucketapi.BucketScopeResponse{Organization: srcScope.Organization, ProjectId: srcScope.Project, Path: stringPtr("s3://source/organizations/source-org/projects/source-project")}
	sourceOrg := bucketapi.BucketScopeResponse{Organization: srcScope.Organization, Path: stringPtr("s3://source/organizations/source-org")}
	targetProject := bucketapi.BucketScopeResponse{Organization: dstScope.Organization, ProjectId: dstScope.Project, Path: stringPtr("s3://target/organizations/target-org/projects/target-project")}
	targetOrg := bucketapi.BucketScopeResponse{Organization: dstScope.Organization, Path: stringPtr("s3://target/organizations/target-org")}

	t.Run("resolves source and destination scopes", func(t *testing.T) {
		sourceAPI := &copyBucketAPI{scopes: map[string][]bucketapi.BucketScopeResponse{"source-bucket": {sourceProject, sourceOrg}}, listErr: map[string]error{}}
		targetAPI := &copyBucketAPI{scopes: map[string][]bucketapi.BucketScopeResponse{"target-bucket": {targetProject, targetOrg}}, listErr: map[string]error{}}
		resolved, err := resolveCopyScopes(context.Background(), services.NewBucketsService(sourceAPI), services.NewBucketsService(targetAPI), map[string]bucketapi.BucketMetadata{"source-bucket": {}}, map[string]bucketapi.BucketMetadata{"target-bucket": {}}, srcScope, dstScope)
		if err != nil {
			t.Fatalf("resolveCopyScopes returned error: %v", err)
		}
		if resolved.sourceBucket != "source-bucket" || resolved.targetBucket != "target-bucket" || resolved.sourceProject == nil || resolved.sourceOrg == nil || resolved.targetProject == nil || resolved.targetOrg == nil {
			t.Fatalf("unexpected resolved scopes: %+v", resolved)
		}
	})

	t.Run("returns partial result when source project is absent", func(t *testing.T) {
		sourceAPI := &copyBucketAPI{scopes: map[string][]bucketapi.BucketScopeResponse{"source-bucket": {sourceOrg}}, listErr: map[string]error{}}
		resolved, err := resolveCopyScopes(context.Background(), services.NewBucketsService(sourceAPI), services.NewBucketsService(&copyBucketAPI{scopes: map[string][]bucketapi.BucketScopeResponse{}, listErr: map[string]error{}}), map[string]bucketapi.BucketMetadata{"source-bucket": {}}, nil, srcScope, dstScope)
		if err != nil || resolved == nil || resolved.sourceProject != nil {
			t.Fatalf("resolveCopyScopes = %+v, %v; want partial result without source project", resolved, err)
		}
	})

	t.Run("falls back to source bucket when destination has no scope match", func(t *testing.T) {
		sourceAPI := &copyBucketAPI{scopes: map[string][]bucketapi.BucketScopeResponse{"shared-bucket": {sourceProject}}, listErr: map[string]error{}}
		targetAPI := &copyBucketAPI{scopes: map[string][]bucketapi.BucketScopeResponse{"shared-bucket": {}}, listErr: map[string]error{}}
		resolved, err := resolveCopyScopes(context.Background(), services.NewBucketsService(sourceAPI), services.NewBucketsService(targetAPI), map[string]bucketapi.BucketMetadata{"shared-bucket": {}}, map[string]bucketapi.BucketMetadata{"shared-bucket": {}}, srcScope, dstScope)
		if err != nil || resolved == nil || resolved.targetBucket != "shared-bucket" {
			t.Fatalf("resolveCopyScopes = %+v, %v; want source bucket fallback", resolved, err)
		}
	})

	t.Run("reports missing destination bucket", func(t *testing.T) {
		sourceAPI := &copyBucketAPI{scopes: map[string][]bucketapi.BucketScopeResponse{"source-bucket": {sourceProject}}, listErr: map[string]error{}}
		targetAPI := &copyBucketAPI{scopes: map[string][]bucketapi.BucketScopeResponse{"other-bucket": {}}, listErr: map[string]error{}}
		_, err := resolveCopyScopes(context.Background(), services.NewBucketsService(sourceAPI), services.NewBucketsService(targetAPI), map[string]bucketapi.BucketMetadata{"source-bucket": {}}, map[string]bucketapi.BucketMetadata{"other-bucket": {}}, srcScope, dstScope)
		if err == nil || !strings.Contains(err.Error(), "source bucket \"source-bucket\" is not configured") {
			t.Fatalf("missing destination bucket error = %v", err)
		}
	})

	t.Run("reports empty source bucket mapping", func(t *testing.T) {
		sourceAPI := &copyBucketAPI{scopes: map[string][]bucketapi.BucketScopeResponse{"": {sourceProject}}, listErr: map[string]error{}}
		_, err := resolveCopyScopes(context.Background(), services.NewBucketsService(sourceAPI), services.NewBucketsService(&copyBucketAPI{scopes: map[string][]bucketapi.BucketScopeResponse{}, listErr: map[string]error{}}), map[string]bucketapi.BucketMetadata{"": {}}, nil, srcScope, dstScope)
		if err == nil || !strings.Contains(err.Error(), "exists but has no bucket mapping") {
			t.Fatalf("empty source bucket error = %v", err)
		}
	})

	t.Run("wraps source and destination list errors", func(t *testing.T) {
		sourceErr := errors.New("source list failed")
		sourceAPI := &copyBucketAPI{scopes: map[string][]bucketapi.BucketScopeResponse{}, listErr: map[string]error{"source-bucket": sourceErr}}
		_, err := resolveCopyScopes(context.Background(), services.NewBucketsService(sourceAPI), services.NewBucketsService(&copyBucketAPI{scopes: map[string][]bucketapi.BucketScopeResponse{}, listErr: map[string]error{}}), map[string]bucketapi.BucketMetadata{"source-bucket": {}}, nil, srcScope, dstScope)
		if err == nil || !strings.Contains(err.Error(), "failed to list source scopes") || !errors.Is(err, sourceErr) {
			t.Fatalf("source list error = %v", err)
		}

		destinationErr := errors.New("destination list failed")
		targetAPI := &copyBucketAPI{scopes: map[string][]bucketapi.BucketScopeResponse{}, listErr: map[string]error{"target-bucket": destinationErr}}
		_, err = resolveCopyScopes(context.Background(), services.NewBucketsService(&copyBucketAPI{scopes: map[string][]bucketapi.BucketScopeResponse{}, listErr: map[string]error{}}), services.NewBucketsService(targetAPI), nil, map[string]bucketapi.BucketMetadata{"target-bucket": {}}, srcScope, dstScope)
		if err == nil || !strings.Contains(err.Error(), "failed to list destination scopes") || !errors.Is(err, destinationErr) {
			t.Fatalf("destination list error = %v", err)
		}
	})
}

func TestEnsureDestinationScopes(t *testing.T) {
	source := projectcopy.Scope{Organization: "source-org", Project: "source-project"}
	target := projectcopy.Scope{Organization: "target-org", Project: "target-project"}

	t.Run("creates remapped organization and project paths", func(t *testing.T) {
		api := &copyBucketAPI{scopes: map[string][]bucketapi.BucketScopeResponse{}, listErr: map[string]error{}}
		resolved := &resolvedCopyScopes{
			source:        source,
			target:        target,
			targetBucket:  "target-bucket",
			sourceOrg:     &bucketapi.BucketScopeResponse{Organization: source.Organization, Path: stringPtr("s3://source-base/organizations/source-org")},
			sourceProject: &bucketapi.BucketScopeResponse{Organization: source.Organization, ProjectId: source.Project, Path: stringPtr("s3://source-base/organizations/source-org/projects/source-project")},
		}
		cmd := &cobra.Command{}
		var output strings.Builder
		cmd.SetOut(&output)
		if err := ensureDestinationScopes(context.Background(), cmd, services.NewBucketsService(api), resolved); err != nil {
			t.Fatalf("ensureDestinationScopes returned error: %v", err)
		}
		if len(api.addCalls) != 2 {
			t.Fatalf("AddScope calls = %d; want 2", len(api.addCalls))
		}
		if got := api.addCalls[0]; got.bucket != "target-bucket" || got.request.Organization != target.Organization || got.request.ProjectId != "" || got.request.Path == nil || *got.request.Path != "s3://target-bucket/organizations/target-org" {
			t.Fatalf("organization AddScope call = %+v", got)
		}
		if got := api.addCalls[1]; got.bucket != "target-bucket" || got.request.Organization != target.Organization || got.request.ProjectId != target.Project || got.request.Path == nil || *got.request.Path != "s3://target-bucket/organizations/target-org/projects/source-project" {
			t.Fatalf("project AddScope call = %+v", got)
		}
		wantOutput := "Creating organization scope mapping on bucket target-bucket: target-org -> s3://target-bucket/organizations/target-org\n" +
			"Creating project scope mapping on bucket target-bucket: target-org/target-project -> s3://target-bucket/organizations/target-org/projects/source-project\n"
		if output.String() != wantOutput {
			t.Fatalf("scope creation output = %q; want %q", output.String(), wantOutput)
		}
		if resolved.targetOrg == nil || resolved.targetProject == nil || resolved.targetProject.Path == nil || *resolved.targetProject.Path != "s3://target-bucket/organizations/target-org/projects/source-project" {
			t.Fatalf("resolved destination scopes = %+v", resolved)
		}
	})

	t.Run("uses existing destination project without writes", func(t *testing.T) {
		api := &copyBucketAPI{scopes: map[string][]bucketapi.BucketScopeResponse{}, listErr: map[string]error{}}
		resolved := &resolvedCopyScopes{target: target, targetBucket: "target-bucket", targetOrg: &bucketapi.BucketScopeResponse{}, targetProject: &bucketapi.BucketScopeResponse{}}
		cmd := &cobra.Command{}
		if err := ensureDestinationScopes(context.Background(), cmd, services.NewBucketsService(api), resolved); err != nil {
			t.Fatalf("ensureDestinationScopes returned error: %v", err)
		}
		if len(api.addCalls) != 0 {
			t.Fatalf("AddScope calls = %d; want 0", len(api.addCalls))
		}
	})

	t.Run("defaults project path from existing organization path", func(t *testing.T) {
		api := &copyBucketAPI{scopes: map[string][]bucketapi.BucketScopeResponse{}, listErr: map[string]error{}}
		orgPath := "  s3://target-bucket/custom/org/  "
		resolved := &resolvedCopyScopes{target: target, targetBucket: "target-bucket", targetOrg: &bucketapi.BucketScopeResponse{Path: &orgPath}}
		if err := ensureDestinationScopes(context.Background(), &cobra.Command{}, services.NewBucketsService(api), resolved); err != nil {
			t.Fatalf("ensureDestinationScopes returned error: %v", err)
		}
		if len(api.addCalls) != 1 || api.addCalls[0].request.Path == nil || *api.addCalls[0].request.Path != "s3://target-bucket/custom/org/target-project" {
			t.Fatalf("project default path = %+v", api.addCalls)
		}
	})

	t.Run("reports destination bucket and add failures", func(t *testing.T) {
		if err := ensureDestinationScopes(context.Background(), &cobra.Command{}, services.NewBucketsService(&copyBucketAPI{}), &resolvedCopyScopes{target: target}); err == nil || !strings.Contains(err.Error(), "failed to resolve a destination bucket") {
			t.Fatalf("missing target bucket error = %v", err)
		}

		orgErr := errors.New("organization write failed")
		api := &copyBucketAPI{addErr: []error{orgErr}}
		resolved := &resolvedCopyScopes{target: target, targetBucket: "target-bucket"}
		err := ensureDestinationScopes(context.Background(), &cobra.Command{}, services.NewBucketsService(api), resolved)
		if err == nil || !strings.Contains(err.Error(), "failed to map organization scope") || !errors.Is(err, orgErr) {
			t.Fatalf("organization write error = %v", err)
		}

		projectErr := errors.New("project write failed")
		api = &copyBucketAPI{addErr: []error{projectErr}}
		resolved = &resolvedCopyScopes{target: target, targetBucket: "target-bucket", targetOrg: &bucketapi.BucketScopeResponse{}}
		err = ensureDestinationScopes(context.Background(), &cobra.Command{}, services.NewBucketsService(api), resolved)
		if err == nil || !strings.Contains(err.Error(), "failed to map project scope") || !errors.Is(err, projectErr) {
			t.Fatalf("project write error = %v", err)
		}
	})
}

func stringPtr(value string) *string {
	return &value
}
