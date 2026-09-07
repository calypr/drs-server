package lfs

import (
	"context"
	"io"
	"net/http"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/testutils"
	"github.com/calypr/syfon/internal/urlmanager"
	"github.com/gofiber/fiber/v3"
)

type fiberTestRouter struct {
	app *fiber.App
}

func ptr[T any](v T) *T { return &v }

func (r *fiberTestRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	resp, err := r.app.Test(req)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(err.Error()))
		return
	}
	defer resp.Body.Close()
	for k, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func newLFSRouterWithOptions(opts Options) (*fiberTestRouter, *testutils.MockDatabase) {
	db := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{},
	}
	uM := &testutils.MockUrlManager{}
	app := fiber.New()
	om := core.NewObjectManager(db, uM)
	RegisterLFSRoutes(app, om, opts)
	return &fiberTestRouter{app: app}, db
}

func newLFSRouter() (*fiberTestRouter, *testutils.MockDatabase) {
	return newLFSRouterWithOptions(DefaultOptions())
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func resolveObjectForOID(ctx context.Context, database *testutils.MockDatabase, oid string) (*models.InternalObject, error) {
	om := core.NewObjectManager(database, nil)
	return om.GetObject(ctx, oid, "")
}

type customMockUrlManager struct {
	testutils.MockUrlManager
	uploadURL      string
	initCalled     int
	initBucket     string
	initKey        string
	completeCalled int
}

func (m *customMockUrlManager) InitMultipartUpload(ctx context.Context, bucket string, key string) (string, error) {
	m.initCalled++
	m.initBucket = bucket
	m.initKey = key
	return "mock-upload-id", nil
}

func (m *customMockUrlManager) SignMultipartPart(ctx context.Context, bucket string, key string, uploadId string, partNumber int32) (string, error) {
	return m.uploadURL, nil
}

func (m *customMockUrlManager) CompleteMultipartUpload(ctx context.Context, bucket string, key string, uploadId string, parts []urlmanager.MultipartPart) error {
	m.completeCalled++
	return nil
}
