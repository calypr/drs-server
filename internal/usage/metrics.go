package usage

import "time"

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
