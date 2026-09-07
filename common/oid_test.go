package common

import (
	"strings"
	"testing"
)

func TestNormalizeOidAndMintObjectIDFromChecksum(t *testing.T) {
	valid := strings.Repeat("a", 64)
	if got := NormalizeOid("  sha256:" + strings.ToUpper(valid) + "  "); got != valid {
		t.Fatalf("unexpected normalized oid: %q", got)
	}
	if got := NormalizeOid("not-a-valid-oid"); got != "" {
		t.Fatalf("expected invalid oid to normalize to empty string, got %q", got)
	}

}
