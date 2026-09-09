package storage

import (
	"path"
	"strconv"
	"strings"
)

func MultipartPartObjectKey(key string, uploadID UploadID, partNumber int32) string {
	cleanKey := strings.Trim(strings.TrimSpace(key), "/")
	return path.Join(".syfon-multipart", strings.TrimSpace(string(uploadID)), cleanKey, "parts", strconv.Itoa(int(partNumber)))
}
