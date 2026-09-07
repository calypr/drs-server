package internaldrs

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/urlmanager"
	"github.com/gofiber/fiber/v3"
)

func ptr[T any](v T) *T { return &v }

type capturingMultipartURLManager struct {
	bucket    string
	key       string
	signURL   string
	signID    string
	signOpts  urlmanager.SignOptions
	uploadURL string
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

func (m *capturingMultipartURLManager) SignURL(ctx context.Context, accessId string, url string, opts urlmanager.SignOptions) (string, error) {
	m.signID = accessId
	m.signURL = url
	m.signOpts = opts
	return url, nil
}

func (m *capturingMultipartURLManager) SignUploadURL(ctx context.Context, accessId string, url string, opts urlmanager.SignOptions) (string, error) {
	m.signID = accessId
	m.signURL = url
	m.signOpts = opts
	return url, nil
}

func (m *capturingMultipartURLManager) InitMultipartUpload(ctx context.Context, bucket string, key string) (string, error) {
	m.bucket = bucket
	m.key = key
	return "upload-1", nil
}

func (m *capturingMultipartURLManager) SignMultipartPart(ctx context.Context, bucket string, key string, uploadId string, partNumber int32) (string, error) {
	return "", nil
}

func (m *capturingMultipartURLManager) SignDownloadPart(ctx context.Context, accessId string, url string, start int64, end int64, opts urlmanager.SignOptions) (string, error) {
	return "", nil
}

func (m *capturingMultipartURLManager) CompleteMultipartUpload(ctx context.Context, bucket string, key string, uploadId string, parts []urlmanager.MultipartPart) error {
	return nil
}

func doInternalDRSTestRequest(req *http.Request, om *core.ObjectManager) *httptest.ResponseRecorder {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(req.Context())
		return c.Next()
	})
	RegisterInternalRoutes(app, om)

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

func doInternalDRSTestRequestWithAlias(req *http.Request, om *core.ObjectManager, method string, pattern string, handler fiber.Handler) *httptest.ResponseRecorder {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(req.Context())
		return c.Next()
	})
	RegisterInternalRoutes(app, om)
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
