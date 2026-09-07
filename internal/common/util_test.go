package common

import (
	"testing"
)



func TestSchemeFromURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"HTTP", "http://example.com", "http"},
		{"HTTPS", "HTTPS://example.com", "https"},
		{"S3", "s3://my-bucket", "s3"},
		{"No Scheme", "example.com", ""},
		{"Empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SchemeFromURL(tt.input); got != tt.expected {
				t.Errorf("SchemeFromURL() = %v, want %v", got, tt.expected)
			}
		})
	}
}



func TestBucketToURL(t *testing.T) {
	tests := []struct {
		name     string
		bucket   string
		key      string
		expected string
	}{
		{"Normal", "my-bucket", "my-key", "s3://my-bucket/my-key"},
		{"Bucket With Scheme", "s3://my-bucket", "my-key", "s3://my-bucket/my-key"},
		{"Key With Leading Slash", "my-bucket", "/my-key", "s3://my-bucket/my-key"},
		{"Both With Extras", "s3://my-bucket", "/my-key", "s3://my-bucket/my-key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BucketToURL(tt.bucket, tt.key); got != tt.expected {
				t.Errorf("BucketToURL() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCleanToBasename(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Empty", "", ""},
		{"Spaces", "   ", ""},
		{"Unix path", "/foo/bar/baz.txt", "baz.txt"},
		{"Windows path", `C:\foo\bar\baz.txt`, "baz.txt"},
		{"Relative path", "foo/bar.txt", "bar.txt"},
		{"No path", "baz.txt", "baz.txt"},
		{"Slash end", "foo/bar/", "bar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CleanToBasename(tt.input); got != tt.expected {
				t.Errorf("CleanToBasename(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}
