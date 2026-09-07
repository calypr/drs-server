package transfers

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
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
	objectsPort := &lfsPreparationObjectSpy{getErr: fmt.Errorf("%w: missing", faults.ErrNotFound)}
	credentials := &lfsPreparationCredentialsSpy{credentials: []buckets.Credential{{Bucket: "bucket"}}}
	workflow := NewLFSPreparationWorkflow(NewService(Dependencies{}), objectsPort, credentials, nil)

	result, err := workflow.PrepareUpload(context.Background(), "oid", -3)
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
