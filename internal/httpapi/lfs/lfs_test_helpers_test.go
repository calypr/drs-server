package lfs

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
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
	initTarget     storage.ObjectTarget
	partRequest    storage.MultipartPartRequest
	complete       storage.CompleteMultipartRequest
}

func (f *lfsTestStorage) Access(_ context.Context, request storage.AccessRequest) (storage.Access, error) {
	location := request.Target.Location
	if f.accessLocation != "" {
		location = f.accessLocation
	}
	return storage.Access{Location: location + "?signed=true"}, nil
}

func (f *lfsTestStorage) BeginMultipart(_ context.Context, target storage.ObjectTarget) (storage.UploadID, error) {
	f.initTarget = target
	return storage.UploadID("opaque-upload-id"), nil
}

func (f *lfsTestStorage) AccessMultipartPart(_ context.Context, request storage.MultipartPartRequest) (storage.Access, error) {
	f.partRequest = request
	if f.partLocation != "" {
		return storage.Access{Location: f.partLocation}, nil
	}
	return storage.Access{Location: fmt.Sprintf("s3://%s/%s", request.Target.Bucket, request.Target.Key)}, nil
}

func (f *lfsTestStorage) CompleteMultipart(_ context.Context, request storage.CompleteMultipartRequest) error {
	f.complete = request
	return nil
}

func newLFSTestDependencies(ports *lfsTestServicePorts, storageFake *lfsTestStorage) Dependencies {
	objectService := objects.NewService(objects.Dependencies{
		Reader:  ports.objectReader,
		Writer:  ports.objectWriter,
		Aliases: ports.aliases,
		Content: ports.contentReader,
	})
	transferService := newLFSTransferService(storageFake, ports)
	return Dependencies{
		ObjectService:   objectService,
		TransferService: transferService,
		FileCounters:    ports.fileCounters,
		Credentials:     ports.credentials,
	}
}

func newLFSTestRouter(ports *lfsTestServicePorts, storageFake *lfsTestStorage, opts Options) *lfsTestRouter {
	app := fiber.New()
	RegisterLFSRoutes(app, newLFSTestDependencies(ports, storageFake), opts)
	return &lfsTestRouter{app: app}
}
