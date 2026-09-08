package records

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/client/apierror"
	"github.com/calypr/syfon/internal/objects"
)

func TestBulkOverwriteResponseKeepsExactConflictCode(t *testing.T) {
	body := `{"organization":"test","project":"p1","records":[{"did":"duplicate"},{"did":"duplicate"}]}`
	req := httptest.NewRequest("PUT", "/index/bulk/overwrite", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	store := &internalRecordStore{Objects: map[string]*objects.Record{}}
	response := doInternalDRSTestRequest(req, newInternalDRSObjectManager(store))
	decoded := apierror.FromResponse(response.Result(), response.Body.Bytes())
	if response.Code != 409 || !errors.Is(decoded, errorapi.ErrBulkOverwriteConflict) {
		t.Fatalf("bulk overwrite conflict code was lost: status=%d body=%s code=%q", response.Code, response.Body.String(), decoded.Code)
	}
}
