package cloud

import "testing"

func TestParseObjectLocation_S3Scheme(t *testing.T) {
	loc, err := parseObjectLocation("s3://my-bucket/path/to/file.bam", "", ObjectParameters{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if loc.bucket != "my-bucket" || loc.key != "path/to/file.bam" || loc.bucketURL != "s3://my-bucket" {
		t.Fatalf("unexpected location: %+v", loc)
	}
}

func TestParseObjectLocation_GCSAndAzure(t *testing.T) {
	cases := []struct {
		raw       string
		bucket    string
		key       string
		bucketURL string
	}{
		{"gs://my-gcs-bucket/path/to/file.bam", "my-gcs-bucket", "path/to/file.bam", "gs://my-gcs-bucket"},
		{"https://myacct.blob.core.windows.net/mycontainer/path/to/blob.bam", "mycontainer", "path/to/blob.bam", "azblob://mycontainer?account_name=myacct"},
	}
	for _, tc := range cases {
		loc, err := parseObjectLocation(tc.raw, "", ObjectParameters{})
		if err != nil {
			t.Fatalf("parseObjectLocation(%q): %v", tc.raw, err)
		}
		if loc.bucket != tc.bucket || loc.key != tc.key || loc.bucketURL != tc.bucketURL {
			t.Fatalf("unexpected location for %q: %+v", tc.raw, loc)
		}
	}
}

func TestParseObjectLocation_S3UsesPassedRegionAndEndpoint(t *testing.T) {
	loc, err := parseObjectLocation("s3://cbds/path/to/file.bin", "", ObjectParameters{
		S3Region:   "us-east-1",
		S3Endpoint: "https://aced-storage.ohsu.edu/",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if loc.bucketURL == "" {
		t.Fatal("expected bucketURL")
	}
}

func TestNormalizeAndExtractSHA256(t *testing.T) {
	hex := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if got := normalizeSHA256("sha256:" + hex); got != hex {
		t.Fatalf("normalizeSHA256 mismatch: %q", got)
	}
	got := extractSHA256FromMetadata(map[string]string{"checksum-sha256": "sha256:" + hex})
	if got != hex {
		t.Fatalf("extractSHA256FromMetadata mismatch: %q", got)
	}
}
