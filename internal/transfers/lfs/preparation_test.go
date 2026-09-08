package lfs

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/transfers"
)

type lfsPreparationObjectSpy struct {
	calls      []string
	object     *objects.Record
	getErr     error
	requireErr error
}

func (s *lfsPreparationObjectSpy) GetObject(_ context.Context, _, method string) (*objects.Record, error) {
	s.calls = append(s.calls, "get:"+method)
	return s.object, s.getErr
}

func (s *lfsPreparationObjectSpy) RequireObjectResources(_ context.Context, method string, resources []string) error {
	s.calls = append(s.calls, "require:"+method+":"+strings.Join(resources, ","))
	return s.requireErr
}
func (s *lfsPreparationObjectSpy) RegisterObjects(context.Context, []objects.Record) error {
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
	if !reflect.DeepEqual(objectsPort.calls, []string{"get:read", "require:create:/data_file"}) {
		t.Fatalf("preflight calls = %v", objectsPort.calls)
	}
	if credentials.calls != 1 {
		t.Fatalf("credential calls = %d", credentials.calls)
	}
}
