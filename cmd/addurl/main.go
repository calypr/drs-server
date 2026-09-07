package addurl

import (
	"fmt"
	"strings"

	"github.com/calypr/syfon/cmd/cliauth"
	syfoncommon "github.com/calypr/syfon/common"
	"github.com/spf13/cobra"
)

var (
	addURLDid     string
	addURL        string
	addURLSize    int64
	addURLName    string
	addURLSHA256  string
	addURLOrg     string
	addURLProject string
)

var Cmd = &cobra.Command{
	Use:     "add-url",
	Aliases: []string{"addurl"},
	Short:   "Create or update a DRS record with an access URL",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if strings.TrimSpace(addURLDid) == "" {
			return fmt.Errorf("--did is required")
		}
		if strings.TrimSpace(addURL) == "" {
			return fmt.Errorf("--url is required")
		}
		org := strings.TrimSpace(addURLOrg)
		if org == "" {
			return fmt.Errorf("--org is required")
		}
		c, err := cliauth.NewServerClient(cmd)
		if err != nil {
			return err
		}
		authzMap := syfoncommon.AuthzMapFromScope(org, strings.TrimSpace(addURLProject))
		if err := c.Index().Upsert(ctx, addURLDid, addURL, addURLName, addURLSize, addURLSHA256, authzMap); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "record updated: %s\n", addURLDid)
		return nil
	},
}

func init() {
	Cmd.Flags().StringVar(&addURLDid, "did", "", "DRS object DID")
	Cmd.Flags().StringVar(&addURL, "url", "", "Access URL to register")
	Cmd.Flags().StringVar(&addURLOrg, "org", "", "Required organization for the authz scope")
	Cmd.Flags().StringVar(&addURLProject, "project", "", "Optional project for the authz scope (omit for org-wide)")
	Cmd.Flags().Int64Var(&addURLSize, "size", 0, "Object size in bytes")
	Cmd.Flags().StringVar(&addURLName, "name", "", "Object file name")
	Cmd.Flags().StringVar(&addURLSHA256, "sha256", "", "Optional sha256 checksum")
}
