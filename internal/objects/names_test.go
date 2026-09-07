package objects

import "testing"

func TestNameNormalizationPreservesTrailingSlashCompatibility(t *testing.T) {
	if got := CleanToBasename("foo/bar/"); got != "bar" {
		t.Fatalf("trailing slash basename = %q", got)
	}
	got := NormalizeNameAliases("/primary/primary.txt", []string{"\\other\\z.txt", "/primary/primary.txt", "z.txt", ""})
	if len(got) != 1 || got[0] != "z.txt" {
		t.Fatalf("normalized aliases = %#v", got)
	}
}
