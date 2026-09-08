package transfers

const (
	RouteDownload          = "/data/download/:file_id"
	RouteDownloadPart      = "/data/download/:file_id/part"
	RouteUpload            = "/data/upload"
	RouteUploadURL         = "/data/upload/:file_id"
	RouteUploadBulk        = "/data/upload/bulk"
	RouteMultipartInit     = "/data/multipart/init"
	RouteMultipartUpload   = "/data/multipart/upload"
	RouteMultipartComplete = "/data/multipart/complete"
)
