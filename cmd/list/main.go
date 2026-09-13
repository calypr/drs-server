package listcmd

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/client/common"
	syfonclient "github.com/calypr/syfon/client/services"
	"github.com/calypr/syfon/cmd/cliauth"
	"github.com/spf13/cobra"
)

var (
	listLimit        int
	listPage         int
	listPath         string
	listStart        string
	listOrganization string
	listProject      string
	listRecursive    bool
)

type indexLister interface {
	List(ctx context.Context, opts syfonclient.ListRecordsOptions) (internalapi.ListRecordsResponse, error)
}

var Cmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List indexed files",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := cliauth.NewServerClient(cmd)
		if err != nil {
			return err
		}
		records, err := listRecords(cmd.Context(), c.Index(), syfonclient.ListRecordsOptions{
			Limit:        listLimit,
			Page:         listPage,
			Start:        strings.TrimSpace(listStart),
			Organization: strings.TrimSpace(listOrganization),
			ProjectID:    strings.TrimSpace(listProject),
		}, listRecursive, strings.TrimSpace(listPath))
		if err != nil {
			return err
		}
		if len(records) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no records found")
			return nil
		}

		sort.Slice(records, func(i, j int) bool {
			return strings.TrimSpace(records[i].Did) < strings.TrimSpace(records[j].Did)
		})
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tORG\tPROJECT\tSIZE\tDID")
		for _, rec := range records {
			did := strings.TrimSpace(rec.Did)
			name := "-"
			if rec.Name != nil {
				name = strings.TrimSpace(*rec.Name)
			}
			size := int64(0)
			if rec.Size != nil {
				size = *rec.Size
			}
			org := ""
			if rec.Organization != nil {
				org = strings.TrimSpace(*rec.Organization)
			}
			project := ""
			if rec.Project != nil {
				project = strings.TrimSpace(*rec.Project)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", name, org, project, common.FormatSize(size), did)
		}
		return tw.Flush()
	},
}

func init() {
	Cmd.Flags().IntVar(&listLimit, "limit", 100, "Maximum number of records to return")
	Cmd.Flags().IntVar(&listPage, "page", 0, "Page number for pagination")
	Cmd.Flags().StringVar(&listPath, "path", "", "Optional path filter within an exact organization/project scope")
	Cmd.Flags().BoolVar(&listRecursive, "recursive", false, "Recursively list all files under the selected path within an exact organization/project scope")
	Cmd.Flags().StringVar(&listStart, "start", "", "List records after this object ID")
	Cmd.Flags().StringVar(&listOrganization, "organization", "", "Optional organization/program filter")
	Cmd.Flags().StringVar(&listProject, "project", "", "Optional project filter")
}

func listRecords(ctx context.Context, lister indexLister, opts syfonclient.ListRecordsOptions, recursive bool, listPath string) ([]internalapi.InternalRecord, error) {
	if strings.TrimSpace(listPath) != "" {
		return nil, fmt.Errorf("path-based listing is no longer supported")
	}
	if strings.TrimSpace(opts.Organization) == "" || strings.TrimSpace(opts.ProjectID) == "" {
		if recursive {
			return nil, fmt.Errorf("--recursive requires both --organization and --project")
		}
	}
	if recursive {
		return nil, fmt.Errorf("path-based recursive listing is no longer supported")
	}
	resp, err := lister.List(ctx, opts)
	if err != nil {
		return nil, err
	}
	if resp.Records == nil {
		return nil, nil
	}
	return *resp.Records, nil
}
