package bucket

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/calypr/syfon/apigen/bucketapi"
	sybucket "github.com/calypr/syfon/client/bucket"
	"github.com/calypr/syfon/cmd/cliauth"
	"github.com/spf13/cobra"
)

var (
	bucketProvider  string
	bucketRegion    string
	bucketAccessKey string
	bucketSecretKey string
	bucketEndpoint  string
	bucketPath      string
)

var Cmd = &cobra.Command{
	Use:   "bucket",
	Short: "Manage Syfon bucket credentials and org/project scopes",
}

var addCmd = &cobra.Command{
	Use:   "add <bucket>",
	Short: "Create or update bucket credentials on the server",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bucket := strings.TrimSpace(args[0])
		if bucket == "" {
			return fmt.Errorf("bucket is required")
		}

		provider := strings.TrimSpace(bucketProvider)
		if provider == "" {
			provider = "s3"
		}

		payload := bucketapi.PutBucketRequest{Bucket: bucket, Provider: &provider}
		if v := strings.TrimSpace(bucketRegion); v != "" {
			payload.Region = &v
		}
		if v := strings.TrimSpace(bucketAccessKey); v != "" {
			payload.AccessKey = &v
		}
		if v := strings.TrimSpace(bucketSecretKey); v != "" {
			payload.SecretKey = &v
		}
		if v := strings.TrimSpace(bucketEndpoint); v != "" {
			payload.Endpoint = &v
		}
		if err := sybucket.ValidateBucket(cmd.Context(), payload); err != nil {
			return fmt.Errorf("local bucket validation failed: %w", err)
		}

		c, err := cliauth.NewServerClient(cmd)
		if err != nil {
			return err
		}
		if err := c.Buckets().Put(cmd.Context(), payload); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "bucket credential configured: %s (provider=%s)\n", bucket, provider)
		return nil
	},
}

var addOrganizationCmd = &cobra.Command{
	Use:   "add-organization <organization>",
	Short: "Assign an organization/program storage URL",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		org := strings.TrimSpace(args[0])
		if org == "" {
			return fmt.Errorf("organization is required")
		}
		scopePath := strings.TrimSpace(bucketPath)
		bucket, err := bucketFromStoragePath(scopePath)
		if err != nil {
			return err
		}
		return addBucketScope(cmd, bucket, bucketapi.AddBucketScopeRequest{
			Organization: org,
			ProjectId:    "",
			Path:         &scopePath,
		}, fmt.Sprintf("bucket organization scope configured: bucket=%s org=%s", bucket, org))
	},
}

var addProjectCmd = &cobra.Command{
	Use:   "add-project <organization> <project-id>",
	Short: "Assign a project storage URL",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		org := strings.TrimSpace(args[0])
		projectID := strings.TrimSpace(args[1])
		if org == "" || projectID == "" {
			return fmt.Errorf("organization and project-id are required")
		}
		scopePath := strings.TrimSpace(bucketPath)
		bucket, err := bucketFromStoragePath(scopePath)
		if err != nil {
			return err
		}
		return addBucketScope(cmd, bucket, bucketapi.AddBucketScopeRequest{
			Organization: org,
			ProjectId:    projectID,
			Path:         &scopePath,
		}, fmt.Sprintf("bucket project scope configured: bucket=%s org=%s project=%s", bucket, org, projectID))
	},
}

func bucketFromStoragePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("--path is required and must include the bucket URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid --path %q: %w", raw, err)
	}
	if strings.TrimSpace(u.Scheme) == "" || strings.TrimSpace(u.Host) == "" {
		return "", fmt.Errorf("--path must be a storage URL like s3://bucket/prefix")
	}
	switch strings.ToLower(strings.TrimSpace(u.Scheme)) {
	case "s3", "gs", "gcs", "az", "azblob":
	default:
		return "", fmt.Errorf("--path must use s3://, gs://, or azblob:// storage URL format")
	}
	return strings.TrimSpace(u.Host), nil
}

func addBucketScope(cmd *cobra.Command, bucket string, req bucketapi.AddBucketScopeRequest, message string) error {
	c, err := cliauth.NewServerClient(cmd)
	if err != nil {
		return err
	}
	if err := c.Buckets().AddScope(cmd.Context(), bucket, req); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), message)
	return nil
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured buckets",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := cliauth.NewServerClient(cmd)
		if err != nil {
			return err
		}
		resp, err := c.Buckets().List(cmd.Context())
		if err != nil {
			return err
		}
		buckets := resp.S3BUCKETS
		if len(buckets) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no buckets configured")
			return nil
		}
		names := make([]string, 0, len(buckets))
		for name := range buckets {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintf(cmd.OutOrStdout(), "%-20s  %-10s  %-12s  %-24s  %s\n", "BUCKET", "PROVIDER", "REGION", "PROGRAMS", "ENDPOINT")
		for _, name := range names {
			md := buckets[name]
			provider := ""
			if md.Provider != nil {
				provider = *md.Provider
			}
			region := ""
			if md.Region != nil {
				region = *md.Region
			}
			programs := []string{}
			if md.Programs != nil {
				programs = *md.Programs
			}
			endpoint := ""
			if md.EndpointUrl != nil {
				endpoint = *md.EndpointUrl
			}
			fmt.Fprintf(
				cmd.OutOrStdout(),
				"%-20s  %-10s  %-12s  %-24s  %s\n",
				name,
				strings.TrimSpace(provider),
				strings.TrimSpace(region),
				strings.Join(programs, ","),
				strings.TrimSpace(endpoint),
			)
		}
		return nil
	},
}

var listScopesCmd = &cobra.Command{
	Use:   "list-scopes <bucket>",
	Short: "List org/project mappings configured on a bucket",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bucket := strings.TrimSpace(args[0])
		if bucket == "" {
			return fmt.Errorf("bucket is required")
		}
		c, err := cliauth.NewServerClient(cmd)
		if err != nil {
			return err
		}
		scopes, err := c.Buckets().ListScopes(cmd.Context(), bucket)
		if err != nil {
			return err
		}
		if len(scopes) == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "no mappings configured for bucket %s\n", bucket)
			return nil
		}
		sort.Slice(scopes, func(i, j int) bool {
			leftOrg := strings.TrimSpace(scopes[i].Organization)
			rightOrg := strings.TrimSpace(scopes[j].Organization)
			if leftOrg != rightOrg {
				return leftOrg < rightOrg
			}
			return strings.TrimSpace(scopes[i].ProjectId) < strings.TrimSpace(scopes[j].ProjectId)
		})
		fmt.Fprintf(cmd.OutOrStdout(), "%-24s  %-24s  %s\n", "ORGANIZATION", "PROJECT", "PATH")
		for _, scope := range scopes {
			path := ""
			if scope.Path != nil {
				path = strings.TrimSpace(*scope.Path)
			}
			project := strings.TrimSpace(scope.ProjectId)
			if project == "" {
				project = "-"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-24s  %-24s  %s\n", strings.TrimSpace(scope.Organization), project, path)
		}
		return nil
	},
}

var removeCmd = &cobra.Command{
	Use:     "remove <bucket>",
	Aliases: []string{"rm", "delete"},
	Short:   "Remove bucket credentials and scopes",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bucket := strings.TrimSpace(args[0])
		if bucket == "" {
			return fmt.Errorf("bucket is required")
		}
		c, err := cliauth.NewServerClient(cmd)
		if err != nil {
			return err
		}
		if err := c.Buckets().Delete(cmd.Context(), bucket); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "bucket removed: %s\n", bucket)
		return nil
	},
}

var removeScopeCmd = &cobra.Command{
	Use:     "remove-scope <bucket> <organization> <path> [project-id]",
	Aliases: []string{"rm-scope", "delete-scope"},
	Short:   "Remove one org or project mapping from a bucket",
	Args:    cobra.RangeArgs(3, 4),
	RunE: func(cmd *cobra.Command, args []string) error {
		bucket := strings.TrimSpace(args[0])
		organization := strings.TrimSpace(args[1])
		scopePath := strings.TrimSpace(args[2])
		projectID := ""
		if len(args) == 4 {
			projectID = strings.TrimSpace(args[3])
		}
		if bucket == "" || organization == "" || scopePath == "" {
			return fmt.Errorf("bucket, organization, and path are required")
		}
		c, err := cliauth.NewServerClient(cmd)
		if err != nil {
			return err
		}
		if err := c.Buckets().DeleteScope(cmd.Context(), bucket, organization, scopePath, projectID); err != nil {
			return err
		}
		if projectID == "" {
			fmt.Fprintf(cmd.OutOrStdout(), "bucket scope removed: bucket=%s org=%s path=%s\n", bucket, organization, scopePath)
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "bucket scope removed: bucket=%s org=%s project=%s path=%s\n", bucket, organization, projectID, scopePath)
		return nil
	},
}

func init() {
	addCmd.Flags().StringVar(&bucketProvider, "provider", "s3", "Bucket provider: s3|gcs|azure")
	addCmd.Flags().StringVar(&bucketRegion, "region", "us-east-1", "Bucket region")
	addCmd.Flags().StringVar(&bucketAccessKey, "access-key", "", "S3 access key (required for new s3 creds)")
	addCmd.Flags().StringVar(&bucketSecretKey, "secret-key", "", "S3 secret key (required for new s3 creds)")
	addCmd.Flags().StringVar(&bucketEndpoint, "endpoint", "", "Custom endpoint URL")

	addOrganizationCmd.Flags().StringVar(&bucketPath, "path", "", "Organization storage root as <scheme>://<bucket>/<prefix>")
	addProjectCmd.Flags().StringVar(&bucketPath, "path", "", "Project storage root as <scheme>://<bucket>/<prefix>")

	Cmd.AddCommand(addCmd)
	Cmd.AddCommand(addOrganizationCmd)
	Cmd.AddCommand(addProjectCmd)
	Cmd.AddCommand(listCmd)
	Cmd.AddCommand(listScopesCmd)
	Cmd.AddCommand(removeCmd)
	Cmd.AddCommand(removeScopeCmd)
}
