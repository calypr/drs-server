package services

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	drsapi "github.com/calypr/syfon/apigen/drs"
	internalapi "github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/client/logs"
	"github.com/calypr/syfon/client/request"
)

type recordingRequester struct {
	mu       sync.Mutex
	method   string
	path     string
	body     []byte
	rawBody  any
	builder  request.RequestBuilder
	response *http.Response
	err      error
}

func (r *recordingRequester) Do(ctx context.Context, method, path string, body, out any, opts ...request.RequestOption) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.method = method
	r.path = path
	r.rawBody = body
	r.builder = request.RequestBuilder{Method: method, Url: path, Headers: map[string]string{}}
	for _, opt := range opts {
		opt(&r.builder)
	}
	if reader, ok := body.(io.Reader); ok && reader != nil {
		data, _ := io.ReadAll(reader)
		r.body = data
	}
	if outResp, ok := out.(**http.Response); ok {
		resp := r.response
		if resp == nil {
			resp = &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}
		}
		*outResp = resp
	}
	return r.err
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

func mustDRSClient(t *testing.T, serverURL string) *drsapi.ClientWithResponses {
	t.Helper()
	client, err := drsapi.NewClientWithResponses(serverURL)
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
