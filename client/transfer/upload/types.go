package upload

import "github.com/calypr/syfon/client/common"

type UploadConfig struct {
	BucketName        string
	NumParallel       int
	ForceMultipart    bool
	IncludeSubDirName bool
	HasMetadata       bool
	ShowProgress      bool
}

type uploadRequest struct {
	sourcePath   string
	objectKey    string
	metadata     common.FileMetadata
	guid         string
	presignedURL string
	bucket       string
}

// RenamedOrSkippedFileInfo is a helper struct for recording renamed or skipped files
type RenamedOrSkippedFileInfo struct {
	GUID        string
	OldFilename string
	NewFilename string
}
