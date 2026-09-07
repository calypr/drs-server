package copyprojectrefs

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/client/services"
	"github.com/calypr/syfon/cmd/projectcopy"
	"github.com/spf13/cobra"
)

var copyFlags projectcopy.AuthFlags

var Cmd = &cobra.Command{
	Use:   "copy-project-refs <source-scope>",
	Short: "Copy project record references to another Syfon instance",
	Long: `Copy the exact Syfon records for one source scope to another Syfon instance.

The source scope is the org/project you are copying from, written as <organization>/<project-id>.

Use source-prefixed auth flags for the source Syfon instance:
  --source-profile
  --source-token
  --source-basic-user / --source-basic-password

Use target-prefixed auth flags for the destination Syfon instance:
  --target-profile
  --target-token
  --target-basic-user / --target-basic-password

For example, in:
  syfon copy-project-refs Ellrott_Lab/embedding_rotation --source-profile calypr-dev --target-server https://other-calypr.example --target-profile calypr-prod

the source scope is Ellrott_Lab/embedding_rotation.

This command copies metadata only: DID, access methods, controlled_access, hashes, filenames, size, description, version, and related record fields. It does not copy file bytes or modify bucket mappings.`,
	Example: `  syfon copy-project-refs Ellrott_Lab/embedding_rotation --source-profile calypr-dev --target-server https://other-calypr.example --target-profile calypr-prod
  syfon copy-project-refs Ellrott_Lab/embedding_rotation --target-server https://other-calypr.example -I fb4284fe-2c39-5939-bae9-69c4bf4c608a`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		scope, err := projectcopy.ParseScopeArg(args[0], "source")
		if err != nil {
			return err
		}

		sourceClient, _, err := copyFlags.NewSourceClient(ctx, cmd)
		if err != nil {
			return err
		}
		targetClient, targetServer, err := copyFlags.NewTargetClient(ctx, cmd, false, false)
		if err != nil {
			return err
		}

		records, err := projectcopy.RecordsToCopy(ctx, cmd, sourceClient.Index(), scope, copyFlags.IndividualDID)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Found %d records to copy.\n", len(records))

		for i, rec := range records {
			fmt.Fprintf(cmd.OutOrStdout(), "[%d/%d] Copying record %s...\n", i+1, len(records), rec.Did)
			if err := upsertExactRecord(ctx, targetClient.Index(), rec); err != nil {
				return fmt.Errorf("failed to copy record %s: %w", rec.Did, err)
			}
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Successfully copied %d records for %s to %s.\n", len(records), args[0], strings.TrimRight(strings.TrimSpace(targetServer), "/"))
		return nil
	},
}

func init() {
	copyFlags.RegisterSourceFlags(Cmd.Flags())
	copyFlags.RegisterTargetFlags(Cmd.Flags())
	copyFlags.RegisterIndividualFlag(Cmd.Flags())
}

func upsertExactRecord(ctx context.Context, index *services.IndexService, rec internalapi.InternalRecord) error {
	existing, err := index.Get(ctx, rec.Did)
	if err == nil {
		rec.CreatedTime = existing.CreatedTime
		rec.UpdatedTime = existing.UpdatedTime
		_, err = index.Update(ctx, rec.Did, rec)
		return err
	}
	return createRecord(ctx, index, rec)
}

func createRecord(ctx context.Context, index *services.IndexService, rec internalapi.InternalRecord) error {
	_, err := index.Create(ctx, rec)
	return err
}
