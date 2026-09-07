package storage

import (
	"path"
	"sort"
	"strconv"
	"strings"
)

func NormalizedMultipartParts(parts []CompletedPart) []CompletedPart {
	partList := append([]CompletedPart(nil), parts...)
	sort.Slice(partList, func(i, j int) bool {
		return partList[i].PartNumber < partList[j].PartNumber
	})
	return partList
}

func MultipartPartObjectKey(key string, uploadID UploadID, partNumber int32) string {
	cleanKey := strings.Trim(strings.TrimSpace(key), "/")
	return path.Join(".syfon-multipart", strings.TrimSpace(string(uploadID)), cleanKey, "parts", strconv.Itoa(int(partNumber)))
}
