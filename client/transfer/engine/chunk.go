package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/calypr/syfon/client/common"
)

// OptimalChunkSize returns a recommended chunk size for the given file size.
func OptimalChunkSize(fileSize int64) int64 {
	if fileSize <= 0 {
		return 1 * common.MB
	}

	switch {
	case fileSize <= 100*common.MB:
		return fileSize
	case fileSize <= 1*common.GB:
		return 10 * common.MB
	case fileSize <= 10*common.GB:
		return scaleLinear(fileSize, 1*common.GB, 10*common.GB, 25*common.MB, 128*common.MB)
	case fileSize <= 100*common.GB:
		return 256 * common.MB
	default:
		return scaleLinear(fileSize, 100*common.GB, 1000*common.GB, 512*common.MB, 1024*common.MB)
	}
}

// CheckpointPath returns the on-disk path for an upload checkpoint.
func CheckpointPath(sourcePath, guid string) (string, error) {
	cacheDir := os.Getenv("DATA_CLIENT_CACHE_DIR")
	if cacheDir == "" {
		cacheDir = os.TempDir()
	}

	base := filepath.Join(cacheDir, "syfon", "multipart")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(sourcePath + "|" + guid))
	return filepath.Join(base, hex.EncodeToString(sum[:])+".json"), nil
}

func scaleLinear(size, minSize, maxSize, minChunk, maxChunk int64) int64 {
	if size <= minSize {
		return minChunk
	}
	if size >= maxSize {
		return maxChunk
	}
	ratio := float64(size-minSize) / float64(maxSize-minSize)
	chunkF := float64(minChunk) + ratio*(float64(maxChunk-minChunk))
	mb := int64(common.MB)
	chunk := int64(chunkF) / mb * mb
	if chunk < minChunk {
		return minChunk
	}
	if chunk > maxChunk {
		return maxChunk
	}
	return chunk
}
