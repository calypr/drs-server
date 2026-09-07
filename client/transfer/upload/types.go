package upload

type UploadConfig struct {
	BucketName        string
	NumParallel       int
	ForceMultipart    bool
	IncludeSubDirName bool
	HasMetadata       bool
	ShowProgress      bool
}

// RenamedOrSkippedFileInfo is a helper struct for recording renamed or skipped files
type RenamedOrSkippedFileInfo struct {
	GUID        string
	OldFilename string
	NewFilename string
}
