package sdkerror

import (
	"errors"
	"fmt"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
)

func TestLocalErrorsUseCanonicalIdentity(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "no records", err: ErrNoRecordsForHash},
		{name: "profile", err: ErrProfileNotFound},
		{name: "range", err: ErrRangeIgnored},
	} {
		t.Run(test.name, func(t *testing.T) {
			wrapped := fmt.Errorf("wrapped: %w", test.err)
			if !errors.Is(wrapped, test.err) {
				t.Fatal("wrapped local error did not preserve canonical identity")
			}
			if errors.Is(wrapped, errorapi.ErrNotFound) || errors.Is(wrapped, errorapi.ErrUnavailable) {
				t.Fatal("local SDK error matched an API sentinel")
			}
		})
	}
}
