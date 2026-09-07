package common

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

// DownloadFilename returns the basename to present to clients for downloads.
// Object names may carry storage-relative paths; download UX should not.
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

// ContentDispositionAttachment returns a conservative attachment disposition for
// a download filename, including RFC 5987 UTF-8 encoding.
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
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f:
			continue
		case r == '"' || r == '\\':
			b.WriteRune('_')
		case r > 0x7e:
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func escapeRFC5987(name string) string {
	escaped := url.QueryEscape(name)
	return strings.ReplaceAll(escaped, "+", "%20")
}
