package lfs

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/transfers"
)

type lfsPreparationObjectSpy struct {
	calls  []string
	object *drs.DrsObject
	getErr error
}

func (s *lfsPreparationObjectSpy) GetObject(_ context.Context, _, method string) (*drs.DrsObject, error) {
	s.calls = append(s.calls, "get:"+method)
	return s.object, s.getErr
}

func (s *lfsPreparationObjectSpy) RegisterObjects(context.Context, []drs.DrsObject) error {
	return nil
}

type lfsPreparationCredentialsSpy struct {
	credentials []buckets.Credential
	err         error
	calls       int
}

func (s *lfsPreparationCredentialsSpy) ListS3Credentials(context.Context) ([]buckets.Credential, error) {
	s.calls++
	return s.credentials, s.err
}

func (s *lfsPreparationCredentialsSpy) GetS3Credential(context.Context, string) (*buckets.Credential, error) {
	return nil, nil
}

func TestLFSPreparationWorkflowPreservesUploadPreflightAndSizeRules(t *testing.T) {
	objectsPort := &lfsPreparationObjectSpy{getErr: fmt.Errorf("%w: missing", errorapi.ErrNotFound)}
	credentials := &lfsPreparationCredentialsSpy{credentials: []buckets.Credential{{Bucket: "bucket"}}}
	service := NewService(transfers.NewService(transfers.Dependencies{}), objectsPort, credentials, nil, nil, nil)

	result, err := service.PrepareUpload(context.Background(), "oid", -3)
	if err != nil {
		t.Fatalf("PrepareUpload() error = %v", err)
	}
	if result.Existing || result.Size != 0 {
		t.Fatalf("PrepareUpload() result = %+v", result)
	}
	if !reflect.DeepEqual(objectsPort.calls, []string{"get:read"}) {
		t.Fatalf("preflight calls = %v", objectsPort.calls)
	}
	if credentials.calls != 1 {
		t.Fatalf("credential calls = %d", credentials.calls)
	}
}
