package buckets

import (
	"encoding/json"
	"io"
	"strings"
)

func readOptionalPath(path *string) string {
	if path == nil {
		return ""
	}
	return strings.TrimSpace(*path)
}

func decodeStrictJSON(body []byte, dst any) error {
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return io.ErrUnexpectedEOF
	}
	return nil
}
