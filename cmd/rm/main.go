package rm

import (
	"fmt"
	"strings"

	"github.com/calypr/syfon/cmd/cliauth"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/spf13/cobra"
)

var (
	rmDID          string
	rmOrganization string
	rmProject      string
)

var Cmd = &cobra.Command{
	Use:     "rm",
	Aliases: []string{"delete"},
	Short:   "Remove a record by DID",
	RunE: func(cmd *cobra.Command, args []string) error {
		did := strings.TrimSpace(rmDID)
		if did == "" {
			return fmt.Errorf("--did is required")
		}
		c, err := cliauth.NewServerClient(cmd)
		if err != nil {
			return err
		}
		rec, err := c.Index().Get(cmd.Context(), did)
		if err != nil {
			return err
		}

		controlled := clientaccess.NormalizeAccessResources(derefStringSlice(rec.ControlledAccess))
		if len(controlled) <= 1 {
			if err := c.DRS().DeleteObject(cmd.Context(), did, true); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s and attempted storage purge\n", did)
			return nil
		}

		resource, err := clientaccess.ResourcePath(strings.TrimSpace(rmOrganization), strings.TrimSpace(rmProject))
		if err != nil {
			return err
		}
		if resource == "" {
			return fmt.Errorf("--organization is required when the record has multiple controlled-access resources")
		}
		if _, err := c.Index().RemoveControlledAccess(cmd.Context(), did, resource); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "removed scoped access %s from %s\n", resource, did)
		return nil
	},
}

func init() {
	Cmd.Flags().StringVar(&rmDID, "did", "", "DRS object DID")
	Cmd.Flags().StringVar(&rmOrganization, "organization", "", "Organization scope for controlled-access removal")
	Cmd.Flags().StringVar(&rmProject, "project", "", "Project scope for controlled-access removal")
}

func derefStringSlice(in *[]string) []string {
	if in == nil {
		return nil
	}
	return *in
}
