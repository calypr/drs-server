package usage

import (
	"time"

	"github.com/calypr/syfon/apigen/metricsapi"
)

// Report result shapes are the generated API models. Keeping one result
// representation means persistence can produce exactly what the HTTP API
// returns without a second set of structs and converters.
type FileUsage = metricsapi.FileUsage
type FileUsageSummary = metricsapi.FileUsageSummary
type Summary = metricsapi.TransferAttributionSummary
type Freshness = metricsapi.TransferMetricsFreshness
type Breakdown = metricsapi.TransferAttributionBreakdown

type Filter struct {
	Organization         string
	Project              string
	EventType            string
	Direction            string
	From                 *time.Time
	To                   *time.Time
	Provider             string
	Bucket               string
	SHA256               string
	User                 string
	ReconciliationStatus string
}
