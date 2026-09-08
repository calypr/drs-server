package lfs

import (
	"context"
	"fmt"
	"io"
	"net/http"

	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/transfers"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
	"github.com/gofiber/fiber/v3"
)

type lfsTestRouter struct{ app *fiber.App }

func (r *lfsTestRouter) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	response, err := r.app.Test(request)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(err.Error()))
		return
	}
	defer response.Body.Close()
	for key, values := range response.Header {
		for _, value := range values {
			writer.Header().Add(key, value)
		}
	}
	writer.WriteHeader(response.StatusCode)
	_, _ = io.Copy(writer, response.Body)
}

type lfsTestStorage struct {
	accessLocation string
	partLocation   string
	uploadPart     func([]byte) (string, error)
	initTarget     storage.Target
	partRequest    storage.MultipartPartRequest
	complete       storage.CompleteMultipartRequest
}

func (f *lfsTestStorage) Sign(_ context.Context, request storage.SignRequest) (storage.SignedAccess, error) {
	location := request.Target.OriginalURL
	if f.accessLocation != "" {
		location = f.accessLocation
	}
	return storage.SignedAccess{Location: location + "?signed=true"}, nil
}

func (f *lfsTestStorage) BeginMultipart(_ context.Context, target storage.Target) (storage.UploadID, error) {
	f.initTarget = target
	return storage.UploadID("opaque-upload-id"), nil
}

func (f *lfsTestStorage) SignMultipartPart(_ context.Context, request storage.MultipartPartRequest) (storage.SignedAccess, error) {
	f.partRequest = request
	if f.partLocation != "" {
		return storage.SignedAccess{Location: f.partLocation}, nil
	}
	return storage.SignedAccess{Location: fmt.Sprintf("s3://%s/%s", request.Target.PhysicalBucket, request.Target.Key)}, nil
}

func (f *lfsTestStorage) CompleteMultipart(_ context.Context, request storage.CompleteMultipartRequest) error {
	f.complete = request
	return nil
}

func newLFSTestDependencies(ports *lfsTestServicePorts, storageFake *lfsTestStorage) Dependencies {
	transferService := newLFSTransferService(storageFake, ports)
	return newLFSTestDependenciesWithTransfer(ports, storageFake, transferService)
}

func newLFSTestDependenciesWithTransfer(ports *lfsTestServicePorts, storageFake *lfsTestStorage, transferService *transfers.Service) Dependencies {
	objectService := objectrecords.NewService(ports)
	lfsService := transferlfs.NewService(transferService, objectService, ports.credentials, ports.pending, ports.fileCounters, storageFakeUploader(storageFake))
	return Dependencies{
		Service: lfsService,
	}
}

func storageFakeUploader(fake *lfsTestStorage) storage.SignedPartUploader {
	return func(_ context.Context, _ string, content []byte) (string, error) {
		if fake.uploadPart != nil {
			return fake.uploadPart(content)
		}
		return "etag", nil
	}
}

func newLFSTestRouter(ports *lfsTestServicePorts, storageFake *lfsTestStorage, opts Options) *lfsTestRouter {
	app := fiber.New()
	RegisterLFSRoutes(app, newLFSTestDependencies(ports, storageFake), opts)
	return &lfsTestRouter{app: app}
}
