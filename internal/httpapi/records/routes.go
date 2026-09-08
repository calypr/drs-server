package records

const (
	RouteIndex                       = "/index"
	RouteIndexDetail                 = "/index/:id"
	RouteIndexControlledAccessRemove = "/index/:id/controlled-access/remove"
	RouteBulkHashes                  = "/index/bulk/hashes"
	RouteBulkDeleteHashes            = "/index/bulk/delete"
	RouteBulkSHA256                  = "/index/bulk/sha256/validity"
	RouteBulkSHA256Missing           = "/index/bulk/sha256/missing"
	RouteBulkCreate                  = "/index/bulk"
	RouteBulkDocs                    = "/index/bulk/documents"
	RouteBulkOverwrite               = "/index/bulk/overwrite"
)
