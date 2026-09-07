package transfers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/storage"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/gofiber/fiber/v3"
)

var _ domaintransfers.AccessPort = (*internalDRSStorageFake)(nil)
var _ domaintransfers.MultipartPort = (*internalDRSStorageFake)(nil)

func ptr[T any](v T) *T { return &v }

type internalDRSStorageFake struct {
	mu sync.Mutex

	bucket        string
	key           string
	signURL       string
	signID        string
	signOpts      storage.AccessOptions
	uploadURL     string
	completeErr   error
	completeParts []storage.CompletedPart
}

func withTestAuthzContext(req *http.Request, mode string, privileges map[string]map[string]bool) *http.Request {
	return req.WithContext(dataTestAuthContext(req.Context(), mode, mode == "gen3", privileges))
}

func dataTestAuthContext(base context.Context, mode string, authHeader bool, privileges map[string]map[string]bool) context.Context {
	sessionMode := mode
	if mode == "local-authz" {
		sessionMode = "local"
	}
	session := access.NewSession(sessionMode)
	session.AuthHeaderPresent = authHeader
	session.AuthzEnforced = sessionMode == "gen3" || mode == "local-authz"
	session.SetAuthorizations(nil, privileges, session.AuthzEnforced)
	return access.WithSession(base, session)
}

func policyTestContext(mode string, authHeader bool, privileges map[string]map[string]bool) context.Context {
	session := access.NewSession(mode)
	session.AuthHeaderPresent = authHeader
	session.AuthzEnforced = mode == "gen3" || mode == "local"
	session.SetAuthorizations(nil, privileges, session.AuthzEnforced)
	return access.WithSession(context.Background(), session)
}

func (m *internalDRSStorageFake) Access(_ context.Context, request storage.AccessRequest) (storage.Access, error) {
	m.mu.Lock()
	m.signID = request.Target.AccessID
	m.signURL = request.Target.Location
	m.signOpts = request.Options
	m.mu.Unlock()
	suffix := "?signed=true"
	if strings.EqualFold(strings.TrimSpace(request.Options.Method), http.MethodPut) || strings.EqualFold(strings.TrimSpace(request.Options.Method), http.MethodPost) {
		suffix += "&upload=true"
	}
	if request.Range != nil {
		suffix += fmt.Sprintf("&range=%d-%d", request.Range.Start, request.Range.End)
	}
	return storage.Access{Location: request.Target.Location + suffix}, nil
}

func (m *internalDRSStorageFake) BeginMultipart(_ context.Context, target storage.ObjectTarget) (storage.UploadID, error) {
	m.mu.Lock()
	m.bucket = target.Bucket
	m.key = target.Key
	m.mu.Unlock()
	return storage.UploadID("mock-upload-id"), nil
}

func (m *internalDRSStorageFake) AccessMultipartPart(_ context.Context, request storage.MultipartPartRequest) (storage.Access, error) {
	m.mu.Lock()
	m.bucket = request.Target.Bucket
	m.key = request.Target.Key
	m.mu.Unlock()
	return storage.Access{Location: fmt.Sprintf("s3://%s/%s?uploadId=%s&partNumber=%d", request.Target.Bucket, request.Target.Key, request.UploadID, request.PartNumber)}, nil
}

func (m *internalDRSStorageFake) CompleteMultipart(_ context.Context, request storage.CompleteMultipartRequest) error {
	m.mu.Lock()
	m.bucket = request.Target.Bucket
	m.key = request.Target.Key
	m.completeParts = append([]storage.CompletedPart(nil), request.Parts...)
	err := m.completeErr
	m.mu.Unlock()
	return err
}

func doInternalDRSTestRequest(req *http.Request, fixture internalDRSTestFixture) *httptest.ResponseRecorder {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(req.Context())
		return c.Next()
	})
	RegisterObjectRoutes(app, fixture.ObjectService, fixture.TransferService, fixture.FileCounters)
	RegisterBulkAndMultipartRoutes(app, fixture.ObjectService, fixture.TransferService)

	rr := httptest.NewRecorder()
	resp, err := app.Test(req)
	if err != nil {
		rr.WriteHeader(http.StatusInternalServerError)
		_, _ = rr.WriteString(err.Error())
		return rr
	}
	defer resp.Body.Close()
	for k, vals := range resp.Header {
		for _, v := range vals {
			rr.Header().Add(k, v)
		}
	}
	rr.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(rr, resp.Body)
	return rr
}

func doInternalDRSTestRequestWithAlias(req *http.Request, fixture internalDRSTestFixture, method string, pattern string, handler fiber.Handler) *httptest.ResponseRecorder {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(req.Context())
		return c.Next()
	})
	RegisterObjectRoutes(app, fixture.ObjectService, fixture.TransferService, fixture.FileCounters)
	RegisterBulkAndMultipartRoutes(app, fixture.ObjectService, fixture.TransferService)
	app.Add([]string{method}, pattern, handler)

	rr := httptest.NewRecorder()
	resp, err := app.Test(req)
	if err != nil {
		rr.WriteHeader(http.StatusInternalServerError)
		_, _ = rr.WriteString(err.Error())
		return rr
	}
	defer resp.Body.Close()
	for k, vals := range resp.Header {
		for _, v := range vals {
			rr.Header().Add(k, v)
		}
	}
	rr.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(rr, resp.Body)
	return rr
}
