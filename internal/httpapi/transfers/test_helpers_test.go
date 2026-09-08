package transfers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/access"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/storage"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

var _ domaintransfers.AccessPort = (*internalDRSStorageFake)(nil)
var _ domaintransfers.MultipartPort = (*internalDRSStorageFake)(nil)

type internalDRSStorageFake struct {
	mu sync.Mutex

	bucket        string
	key           string
	signURL       string
	signID        string
	signOpts      storage.AccessOptions
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

func (m *internalDRSStorageFake) BeginMultipart(_ context.Context, target storage.Target) (storage.UploadID, error) {
	m.mu.Lock()
	m.bucket = target.PhysicalBucket
	m.key = target.Key
	m.mu.Unlock()
	return storage.UploadID("mock-upload-id"), nil
}

func (m *internalDRSStorageFake) SignMultipartPart(_ context.Context, request storage.MultipartPartRequest) (storage.SignedAccess, error) {
	m.mu.Lock()
	m.bucket = request.Target.PhysicalBucket
	m.key = request.Target.Key
	m.mu.Unlock()
	return storage.SignedAccess{Location: fmt.Sprintf("s3://%s/%s?uploadId=%s&partNumber=%d", request.Target.PhysicalBucket, request.Target.Key, request.UploadID, request.PartNumber)}, nil
}

func (m *internalDRSStorageFake) CompleteMultipart(_ context.Context, request storage.CompleteMultipartRequest) error {
	m.mu.Lock()
	m.bucket = request.Target.PhysicalBucket
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
	registerTransferRoutes(app, fixture.ObjectService, fixture.TransferService, fixture.FileCounters)

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

type unimplementedInternalServer struct {
	internalapi.ServerInterface
}

type transfersTestServer struct {
	*TransfersServer
	unimplementedInternalServer
}

func registerTransferRoutes(router fiber.Router, objectService *objectrecords.Service, transferService *domaintransfers.Service, fileCounters usage.FileCounterRecorder) {
	internalapi.RegisterHandlers(router, &transfersTestServer{
		TransfersServer: NewTransfersServer(objectService, transferService, fileCounters),
	})
}
