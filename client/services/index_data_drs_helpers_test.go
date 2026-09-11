package services

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	internalapi "github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/client/logs"
)

type recordingRequester struct {
	mu       sync.Mutex
	method   string
	path     string
	body     []byte
	request  *http.Request
	response *http.Response
	err      error
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func (r *recordingRequester) Do(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.request = req
	r.method = req.Method
	r.path = req.URL.RequestURI()
	if req.Body != nil {
		data, _ := io.ReadAll(req.Body)
		r.body = data
		req.Body = io.NopCloser(bytes.NewReader(data))
	}
	resp := r.response
	if resp == nil {
		resp = &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: req}
	}
	return resp, r.err
}

func discardLogger() *logs.Gen3Logger {
	return logs.NewGen3Logger(slog.New(slog.NewTextHandler(io.Discard, nil)), "", "")
}

func mustInternalClient(t *testing.T, serverURL string) *internalapi.ClientWithResponses {
	t.Helper()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	httpClient := &http.Client{Transport: transport}
	t.Cleanup(transport.CloseIdleConnections)
	client, err := internalapi.NewClientWithResponses(serverURL, internalapi.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("NewClientWithResponses returned error: %v", err)
	}
	return client
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()

	var sb strings.Builder
	if err := json.NewEncoder(&sb).Encode(v); err != nil {
		t.Errorf("Encode returned error: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, sb.String())
}

func toRecordResponse(rec internalapi.InternalRecord) internalapi.InternalRecordResponse {
	return internalapi.InternalRecordResponse{
		Did:              rec.Did,
		AccessMethods:    rec.AccessMethods,
		ControlledAccess: rec.ControlledAccess,
		Description:      rec.Description,
		Name:             rec.Name,
		Hashes:           rec.Hashes,
		Size:             rec.Size,
		Version:          rec.Version,
		Organization:     rec.Organization,
		Project:          rec.Project,
	}
}

func ptrString(s string) *string { return &s }

func ptrInt64(v int64) *int64 { return &v }
