package copyproject

import (
	"fmt"
	"os"

	"github.com/calypr/syfon/cmd/projectcopy"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/spf13/cobra"
)

var Cmd = &cobra.Command{
	Use:   "copy-project <source-scope> <destination-scope>",
	Short: "Copy project records and storage objects to a different scope",
	Long: `Copy a project's data from one scope to another.

The source scope is the org/project you are copying from, written as <organization>/<project-id>.
The destination scope is the org/project you are copying to, also written as <organization>/<project-id>.

Use source-prefixed auth flags for source reads when needed:
  --source-profile
  --source-token
  --source-basic-user / --source-basic-password

Use target-prefixed auth flags for destination writes when needed:
  --target-server
  --target-profile
  --target-token
  --target-basic-user / --target-basic-password

For example, in:
  syfon copy-project Ellrott_Lab/embedding_rotation CBDS_COLLAB/embedding_rotation

the source scope is Ellrott_Lab/embedding_rotation and the destination scope is CBDS_COLLAB/embedding_rotation.

The destination bucket is resolved on the destination Syfon instance when it already exists; otherwise Syfon creates the missing destination mappings and copies into a bucket with the same credential id as the source bucket, if that bucket is configured on the destination instance. Use -I/--individual to copy only one DID within the source scope.`,
	Example: `  syfon copy-project Ellrott_Lab/embedding_rotation CBDS_COLLAB/embedding_rotation
  syfon copy-project Ellrott_Lab/embedding_rotation CBDS_COLLAB/embedding_rotation --target-server https://other-calypr.example
  syfon copy-project Ellrott_Lab/embedding_rotation CBDS_COLLAB/embedding_rotation -I fb4284fe-2c39-5939-bae9-69c4bf4c608a`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		srcScope, err := projectcopy.ParseScopeArg(args[0], "source")
		if err != nil {
			return err
		}
		dstScope, err := projectcopy.ParseScopeArg(args[1], "destination")
		if err != nil {
			return err
		}

		sourceClient, sourceServer, err := copyFlags.NewSourceClient(ctx, cmd)
		if err != nil {
			return err
		}
		targetClient, targetServer, err := copyFlags.NewTargetClient(ctx, cmd, true, true)
		if err != nil {
			return err
		}

		sourceBuckets, err := sourceClient.Buckets().List(ctx)
		if err != nil {
			return fmt.Errorf("failed to list source buckets: %w", err)
		}
		targetBuckets, err := targetClient.Buckets().List(ctx)
		if err != nil {
			return fmt.Errorf("failed to list destination buckets: %w", err)
		}

		resolved, err := resolveCopyScopes(ctx, sourceClient.Buckets(), targetClient.Buckets(), sourceBuckets.S3BUCKETS, targetBuckets.S3BUCKETS, srcScope, dstScope)
		if err != nil {
			return err
		}

		if resolved.sourceProject == nil {
			return fmt.Errorf("source project scope %s/%s does not exist", srcScope.Organization, srcScope.Project)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Source bucket resolved: %s\n", resolved.sourceBucket)

		if sameServerURL(sourceServer, targetServer) && srcScope == dstScope && resolved.sourceBucket == resolved.targetBucket {
			fmt.Fprintln(cmd.OutOrStdout(), "Source and destination scopes and buckets are identical. Nothing to copy.")
			return nil
		}

		if err := ensureDestinationScopes(ctx, cmd, targetClient.Buckets(), resolved); err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Target bucket resolved: %s\n", resolved.targetBucket)
		records, err := projectcopy.RecordsToCopy(ctx, cmd, sourceClient.Index(), srcScope, copyFlags.IndividualDID)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Found %d records to copy.\n", len(records))

		tempDir := ".tmp"
		if err := os.MkdirAll(tempDir, 0o755); err != nil {
			return fmt.Errorf("failed to create temp directory: %w", err)
		}

		dstResource, err := clientaccess.ResourcePath(dstScope.Organization, dstScope.Project)
		if err != nil {
			return fmt.Errorf("failed to resolve target resource path: %w", err)
		}

		targetProjectPath := pathOrEmpty(resolved.targetProject)
		copiedCount := 0
		skippedCount := 0
		for i, rec := range records {
			if err := copyRecord(ctx, cmd, sourceClient, targetClient, rec, resolved.targetBucket, targetProjectPath, dstResource, i+1, len(records), tempDir); err != nil {
				skippedCount++
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping %s: %v\n", rec.Did, err)
				continue
			}
			copiedCount++
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Successfully copied project %s/%s to %s/%s (%d copied, %d skipped, %d total).\n", srcScope.Organization, srcScope.Project, dstScope.Organization, dstScope.Project, copiedCount, skippedCount, len(records))
		return nil
	},
}

var copyFlags projectcopy.AuthFlags

func init() {
	copyFlags.RegisterIndividualFlag(Cmd.Flags())
	copyFlags.RegisterSourceFlags(Cmd.Flags())
	copyFlags.RegisterTargetFlags(Cmd.Flags())
}
