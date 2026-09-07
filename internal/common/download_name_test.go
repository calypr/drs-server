package common

import "testing"

func TestDownloadFilename(t *testing.T) {
	t.Run("basename from path", func(t *testing.T) {
		in := "aced-evotypes/results/foo/bar/LP6008050-DNA_B01__pv.2.0o__rg.grch38__alleleFrequencies_chr17.txt"
		want := "LP6008050-DNA_B01__pv.2.0o__rg.grch38__alleleFrequencies_chr17.txt"
		if got := DownloadFilename(in); got != want {
			t.Fatalf("unexpected basename: got %q want %q", got, want)
		}
	})

	t.Run("windows path", func(t *testing.T) {
		in := `nested\dir\report.txt`
		if got := DownloadFilename(in); got != "report.txt" {
			t.Fatalf("unexpected basename from windows path: %q", got)
		}
	})
}

func TestContentDispositionAttachment(t *testing.T) {
	got := ContentDispositionAttachment("nested/report final.txt")
	if got == "" {
		t.Fatal("expected content disposition")
	}
	if want := `attachment; filename="report final.txt"; filename*=UTF-8''report%20final.txt`; got != want {
		t.Fatalf("unexpected content disposition:\n got: %s\nwant: %s", got, want)
	}
}
