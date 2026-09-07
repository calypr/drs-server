package metricscmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	syfonclient "github.com/calypr/syfon/client/services"
	"github.com/calypr/syfon/cmd/cliauth"
	"github.com/calypr/syfon/internal/models"
	"github.com/spf13/cobra"
)

var (
	metricsOrganization string
	metricsProject      string
	metricsDirection    string
	metricsReconcile    string
	metricsFrom         string
	metricsTo           string
	metricsProvider     string
	metricsBucket       string
	metricsSHA256       string
	metricsUser         string
	metricsGroupBy      string
	metricsSortBy       string
	metricsSortOrder    string
	metricsLimit        int
	metricsAllowStale   bool
)

var Cmd = &cobra.Command{
	Use:   "metrics",
	Short: "Query Syfon metrics",
}

var transfersCmd = &cobra.Command{
	Use:   "transfers",
	Short: "Query transfer attribution metrics",
}

var transfersSummaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "Summarize transfer attribution metrics",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newMetricsClient(cmd)
		if err != nil {
			return err
		}
		summary, err := c.Metrics().TransferSummary(cmd.Context(), transferOptions())
		if err != nil {
			return err
		}
		return writeJSON(cmd, summary)
	},
}

var transfersBreakdownCmd = &cobra.Command{
	Use:   "breakdown",
	Short: "Group transfer attribution metrics",
	RunE: func(cmd *cobra.Command, args []string) error {
		groupBy, err := normalizedBreakdownGroupBy(metricsGroupBy)
		if err != nil {
			return err
		}
		c, err := newMetricsClient(cmd)
		if err != nil {
			return err
		}
		opts := transferOptions()
		opts.GroupBy = groupBy
		resp, err := c.Metrics().TransferBreakdown(cmd.Context(), opts)
		if err != nil {
			return err
		}
		resp.GroupBy = groupBy
		sortBy, order, err := normalizedBreakdownSort(metricsSortBy, metricsSortOrder, groupBy, opts.Direction)
		if err != nil {
			return err
		}
		sortTransferBreakdowns(resp.Data, sortBy, order)
		resp.Data = limitedTransferBreakdowns(resp.Data, metricsLimit)
		return writeJSON(cmd, resp)
	},
}

type transferUsersReport struct {
	Summary    models.TransferAttributionSummary `json:"summary"`
	Users      []transferUserMetrics             `json:"users"`
	Freshness  *models.TransferMetricsFreshness  `json:"freshness,omitempty"`
	SortBy     string                            `json:"sort_by"`
	SortOrder  string                            `json:"sort_order"`
	TotalUsers int                               `json:"total_users"`
}

type transferUserMetrics struct {
	User             string     `json:"user"`
	ActorEmail       string     `json:"actor_email,omitempty"`
	ActorSubject     string     `json:"actor_subject,omitempty"`
	EventCount       int64      `json:"event_count"`
	BytesRequested   int64      `json:"bytes_requested"`
	BytesDownloaded  int64      `json:"bytes_downloaded"`
	BytesUploaded    int64      `json:"bytes_uploaded"`
	LastTransferTime *time.Time `json:"last_transfer_time,omitempty"`
}

var transfersUsersCmd = &cobra.Command{
	Use:   "users",
	Short: "Rank transfer activity by user",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newMetricsClient(cmd)
		if err != nil {
			return err
		}
		opts := transferOptions()
		summary, err := c.Metrics().TransferSummary(cmd.Context(), opts)
		if err != nil {
			return err
		}
		opts.GroupBy = "user"
		breakdown, err := c.Metrics().TransferBreakdown(cmd.Context(), opts)
		if err != nil {
			return err
		}
		sortBy, order, err := normalizedBreakdownSort(metricsSortBy, metricsSortOrder, "user", opts.Direction)
		if err != nil {
			return err
		}
		sortTransferBreakdowns(breakdown.Data, sortBy, order)
		breakdown.Data = limitedTransferBreakdowns(breakdown.Data, metricsLimit)
		users := make([]transferUserMetrics, 0, len(breakdown.Data))
		for _, item := range breakdown.Data {
			users = append(users, transferUserMetrics{
				User:             transferUserLabel(item),
				ActorEmail:       item.ActorEmail,
				ActorSubject:     item.ActorSubject,
				EventCount:       item.EventCount,
				BytesRequested:   item.BytesRequested,
				BytesDownloaded:  item.BytesDownloaded,
				BytesUploaded:    item.BytesUploaded,
				LastTransferTime: item.LastTransferTime,
			})
		}
		return writeJSON(cmd, transferUsersReport{
			Summary:    summary,
			Users:      users,
			Freshness:  breakdown.Freshness,
			SortBy:     sortBy,
			SortOrder:  order,
			TotalUsers: len(users),
		})
	},
}

type transferBillingReport struct {
	Summary          models.TransferAttributionSummary     `json:"summary"`
	StorageLocations []models.TransferAttributionBreakdown `json:"storage_locations"`
	Files            []models.TransferAttributionBreakdown `json:"files"`
}

var transfersBillingCmd = &cobra.Command{
	Use:   "billing",
	Short: "Report transfer billing totals by storage location and file",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newMetricsClient(cmd)
		if err != nil {
			return err
		}
		opts := transferOptions()
		summary, err := c.Metrics().TransferSummary(cmd.Context(), opts)
		if err != nil {
			return err
		}
		providerOpts := opts
		providerOpts.GroupBy = "provider"
		storageLocations, err := c.Metrics().TransferBreakdown(cmd.Context(), providerOpts)
		if err != nil {
			return err
		}
		objectOpts := opts
		objectOpts.GroupBy = "object"
		files, err := c.Metrics().TransferBreakdown(cmd.Context(), objectOpts)
		if err != nil {
			return err
		}
		return writeJSON(cmd, transferBillingReport{
			Summary:          summary,
			StorageLocations: storageLocations.Data,
			Files:            files.Data,
		})
	},
}

func init() {
	Cmd.AddCommand(transfersCmd)
	transfersCmd.AddCommand(transfersSummaryCmd)
	transfersCmd.AddCommand(transfersBreakdownCmd)
	transfersCmd.AddCommand(transfersBillingCmd)
	transfersCmd.AddCommand(transfersUsersCmd)

	for _, c := range []*cobra.Command{transfersSummaryCmd, transfersBreakdownCmd, transfersBillingCmd, transfersUsersCmd} {
		c.Flags().StringVar(&metricsOrganization, "organization", "", "Organization/program filter")
		c.Flags().StringVar(&metricsProject, "project", "", "Project filter")
		c.Flags().StringVar(&metricsFrom, "from", "", "RFC3339 start time filter")
		c.Flags().StringVar(&metricsTo, "to", "", "RFC3339 end time filter")
		c.Flags().StringVar(&metricsProvider, "provider", "", "Storage provider filter")
		c.Flags().StringVar(&metricsBucket, "bucket", "", "Bucket filter")
	}
	for _, c := range []*cobra.Command{transfersSummaryCmd, transfersBreakdownCmd, transfersBillingCmd, transfersUsersCmd} {
		c.Flags().StringVar(&metricsDirection, "direction", "", "Transfer direction filter: download or upload")
		c.Flags().StringVar(&metricsReconcile, "reconciliation-status", "", "Provider reconciliation filter: matched, ambiguous, unmatched, or all")
		c.Flags().StringVar(&metricsSHA256, "sha256", "", "SHA256 filter")
		c.Flags().StringVar(&metricsUser, "user", "", "Actor email or subject filter")
		c.Flags().BoolVar(&metricsAllowStale, "allow-stale", false, "Deprecated: metrics always return persisted data with freshness metadata")
	}
	for _, c := range []*cobra.Command{transfersBreakdownCmd, transfersUsersCmd} {
		c.Flags().StringVar(&metricsSortBy, "sort-by", "", "Sort rows by downloaded, uploaded, requested, events, last-transfer, or key")
		c.Flags().StringVar(&metricsSortOrder, "sort-order", "desc", "Sort order: asc or desc")
		c.Flags().IntVar(&metricsLimit, "limit", 0, "Limit the number of returned rows")
	}
	transfersBreakdownCmd.Flags().StringVar(&metricsGroupBy, "group-by", "user", "Breakdown grouping: user, scope, provider, or object")
}

func newMetricsClient(cmd *cobra.Command) (syfonclient.SyfonClient, error) {
	return cliauth.NewServerClient(cmd)
}

func transferOptions() syfonclient.TransferMetricsOptions {
	return syfonclient.TransferMetricsOptions{
		Organization:         strings.TrimSpace(metricsOrganization),
		ProjectID:            strings.TrimSpace(metricsProject),
		Direction:            strings.TrimSpace(metricsDirection),
		ReconciliationStatus: strings.TrimSpace(metricsReconcile),
		From:                 strings.TrimSpace(metricsFrom),
		To:                   strings.TrimSpace(metricsTo),
		Provider:             strings.TrimSpace(metricsProvider),
		Bucket:               strings.TrimSpace(metricsBucket),
		SHA256:               strings.TrimSpace(metricsSHA256),
		User:                 strings.TrimSpace(metricsUser),
		GroupBy:              strings.TrimSpace(metricsGroupBy),
		AllowStale:           metricsAllowStale,
	}
}

func writeJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func normalizedBreakdownGroupBy(raw string) (string, error) {
	groupBy := strings.ToLower(strings.TrimSpace(raw))
	switch groupBy {
	case "", "user":
		return "user", nil
	case "scope", "provider", "object":
		return groupBy, nil
	default:
		return "", fmt.Errorf("unsupported group-by %q", raw)
	}
}

func normalizedBreakdownSort(rawSortBy, rawOrder, groupBy, direction string) (string, string, error) {
	sortBy := strings.ToLower(strings.TrimSpace(rawSortBy))
	if sortBy == "" {
		if groupBy == "user" {
			if strings.EqualFold(strings.TrimSpace(direction), "upload") {
				sortBy = "uploaded"
			} else {
				sortBy = "downloaded"
			}
		} else {
			sortBy = "last-transfer"
		}
	}
	switch sortBy {
	case "downloaded", "bytes_downloaded":
		sortBy = "downloaded"
	case "uploaded", "bytes_uploaded":
		sortBy = "uploaded"
	case "requested", "bytes_requested":
		sortBy = "requested"
	case "events", "event_count":
		sortBy = "events"
	case "last-transfer", "last_transfer":
		sortBy = "last-transfer"
	case "key", "user":
		sortBy = "key"
	default:
		return "", "", fmt.Errorf("unsupported sort-by %q", rawSortBy)
	}

	order := strings.ToLower(strings.TrimSpace(rawOrder))
	switch order {
	case "", "desc":
		order = "desc"
	case "asc":
	default:
		return "", "", fmt.Errorf("unsupported sort-order %q", rawOrder)
	}
	return sortBy, order, nil
}

func sortTransferBreakdowns(items []models.TransferAttributionBreakdown, sortBy, order string) {
	desc := order != "asc"
	sort.SliceStable(items, func(i, j int) bool {
		left := items[i]
		right := items[j]
		cmp := compareTransferBreakdown(left, right, sortBy)
		if cmp == 0 {
			cmp = compareTransferBreakdown(left, right, "last-transfer")
		}
		if cmp == 0 {
			cmp = strings.Compare(left.Key, right.Key)
		}
		if desc {
			return cmp > 0
		}
		return cmp < 0
	})
}

func compareTransferBreakdown(left, right models.TransferAttributionBreakdown, sortBy string) int {
	switch sortBy {
	case "downloaded":
		return compareInt64(left.BytesDownloaded, right.BytesDownloaded)
	case "uploaded":
		return compareInt64(left.BytesUploaded, right.BytesUploaded)
	case "requested":
		return compareInt64(left.BytesRequested, right.BytesRequested)
	case "events":
		return compareInt64(left.EventCount, right.EventCount)
	case "key":
		return strings.Compare(left.Key, right.Key)
	default:
		return compareTimePtr(left.LastTransferTime, right.LastTransferTime)
	}
}

func compareInt64(left, right int64) int {
	switch {
	case left > right:
		return 1
	case left < right:
		return -1
	default:
		return 0
	}
}

func compareTimePtr(left, right *time.Time) int {
	switch {
	case left == nil && right == nil:
		return 0
	case left == nil:
		return -1
	case right == nil:
		return 1
	case left.After(*right):
		return 1
	case left.Before(*right):
		return -1
	default:
		return 0
	}
}

func limitedTransferBreakdowns(items []models.TransferAttributionBreakdown, limit int) []models.TransferAttributionBreakdown {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func transferUserLabel(item models.TransferAttributionBreakdown) string {
	if key := strings.TrimSpace(item.Key); key != "" {
		return key
	}
	if email := strings.TrimSpace(item.ActorEmail); email != "" {
		return email
	}
	if subject := strings.TrimSpace(item.ActorSubject); subject != "" {
		return subject
	}
	return "(unattributed)"
}
