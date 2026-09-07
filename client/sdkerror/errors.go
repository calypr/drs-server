package sdkerror

import "errors"

var (
	ErrNoRecordsForHash = errors.New("no records found for hash")
	ErrProfileNotFound  = errors.New("profile not found in config file")
	ErrRangeIgnored     = errors.New("server ignored range request and returned full body")
)
