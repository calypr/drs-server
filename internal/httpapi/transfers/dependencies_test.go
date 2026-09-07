package transfers

import (
	"context"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
)

type internalDRSTestFixture struct {
	ObjectService   *objectrecords.Service
	TransferService *domaintransfers.Service
	FileCounters    usage.FileCounterRecorder
	bucketService   *buckets.Service
	objectStore     *transferObjectStoreFake
}

type transferStorageDependency interface {
	domaintransfers.AccessPort
	domaintransfers.MultipartPort
}

func newInternalDRSObjectManager(store *transferHTTPFixture, storageDependency transferStorageDependency) internalDRSTestFixture {
	objectStore := &transferObjectStoreFake{fixture: store}
	aliasStore := &transferAliasStoreFake{fixture: store}
	bucketStore := &transferBucketStoreFake{fixture: store}
	eventStore := &transferEventStoreFake{fixture: store}
	fileCounters := &transferFileCounterFake{fixture: store}
	bucketService := newInternalDRSBucketService(bucketStore)

	objectService := objectrecords.NewService(objectrecords.Dependencies{
		Reader:        objectStore,
		Aliases:       aliasStore,
		Content:       objectStore,
		ChecksumScope: objectStore,
		Scope:         objectStore,
	})
	transferService := domaintransfers.NewService(domaintransfers.Dependencies{
		Access:      storageDependency,
		Multipart:   storageDependency,
		Scopes:      bucketService,
		Credentials: bucketService,
		Events:      eventStore,
	})
	return internalDRSTestFixture{
		ObjectService:   objectService,
		TransferService: transferService,
		FileCounters:    fileCounters,
		bucketService:   bucketService,
		objectStore:     objectStore,
	}
}

func newInternalDRSBucketService(store *transferBucketStoreFake) *buckets.Service {
	service, err := buckets.NewService(buckets.Dependencies{
		Credentials:     store,
		CredentialAdmin: store,
		Scopes:          store,
		Fallback: func(context.Context) ([]buckets.VisibilityRow, error) {
			return nil, nil
		},
	}, nil)
	if err != nil {
		panic(err)
	}
	return service
}

func (f internalDRSTestFixture) GetObject(ctx context.Context, id, requiredMethod string) (*objects.Record, error) {
	return f.ObjectService.GetObject(ctx, id, requiredMethod)
}

func (f internalDRSTestFixture) RegisterObjects(ctx context.Context, records []objects.Record) error {
	_ = ctx
	f.objectStore.registerObjects(records)
	return nil
}

func (f internalDRSTestFixture) SaveS3Credential(ctx context.Context, credential *buckets.Credential) error {
	return f.bucketService.SaveS3Credential(ctx, credential)
}

func (f internalDRSTestFixture) CreateBucketScope(ctx context.Context, scope *buckets.Scope) error {
	return f.bucketService.CreateBucketScope(ctx, scope)
}
