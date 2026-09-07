package storage

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

func DownloadFilename(name string) string {
	clean := strings.TrimSpace(name)
	if clean == "" {
		return ""
	}
	clean = strings.ReplaceAll(clean, "\\", "/")
	base := path.Base(clean)
	switch strings.TrimSpace(base) {
	case "", ".", "/":
		return ""
	default:
		return base
	}
}

func ContentDispositionAttachment(name string) string {
	filename := DownloadFilename(name)
	if filename == "" {
		return ""
	}
	fallback := sanitizeASCIIFilename(filename)
	if fallback == "" {
		fallback = "download"
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, fallback, escapeRFC5987(filename))
}

func sanitizeASCIIFilename(name string) string {
	var builder strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f:
			continue
		case r == '"' || r == '\\':
			builder.WriteRune('_')
		case r > 0x7e:
			builder.WriteRune('_')
		default:
			builder.WriteRune(r)
		}
	}
	return strings.TrimSpace(builder.String())
}

func escapeRFC5987(name string) string {
	escaped := url.QueryEscape(name)
	return strings.ReplaceAll(escaped, "+", "%20")
}
