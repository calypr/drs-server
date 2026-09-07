package common

import "testing"

func TestNormalizeProvider(t *testing.T) {
	tests := []struct {
		in       string
		fallback string
		want     string
	}{
		{"s3", "", S3Provider},
		{"GS", "", GCSProvider},
		{"azblob", "", AzureProvider},
		{"", "gcs", GCSProvider},
		{"unknown", "", S3Provider},
	}

	for _, tt := range tests {
		got := NormalizeProvider(tt.in, tt.fallback)
		if got != tt.want {
			t.Fatalf("NormalizeProvider(%q,%q)=%q want %q", tt.in, tt.fallback, got, tt.want)
		}
	}
}

func TestSchemeMappings(t *testing.T) {
	if ProviderFromScheme("s3") != S3Provider ||
		ProviderFromScheme("gs") != GCSProvider ||
		ProviderFromScheme("gcs") != GCSProvider ||
		ProviderFromScheme("azblob") != AzureProvider ||
		ProviderFromScheme("az") != AzureProvider {
		t.Fatalf("unexpected ProviderFromScheme mapping")
	}
	if ProviderFromScheme("http") != "" {
		t.Fatalf("expected empty mapping for unknown scheme")
	}

	if ProviderToScheme(S3Provider) != "s3" || ProviderToScheme(GCSProvider) != "gs" || ProviderToScheme(AzureProvider) != "azblob" {
		t.Fatalf("unexpected ProviderToScheme mapping")
	}
	if ProviderToScheme("unknown") != "s3" {
		t.Fatalf("unknown provider should default to s3 scheme")
	}
}

func TestValidateBucketName(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		bucket   string
		wantErr  bool
	}{
		{name: "s3 valid", provider: S3Provider, bucket: "my-bucket", wantErr: false},
		{name: "s3 invalid dot", provider: S3Provider, bucket: "my.bucket", wantErr: true},
		{name: "gcs valid dotted", provider: GCSProvider, bucket: "my.bucket.example", wantErr: false},
		{name: "gcs valid underscore", provider: GCSProvider, bucket: "my_bucket", wantErr: false},
		{name: "gcs invalid ip", provider: GCSProvider, bucket: "192.168.1.1", wantErr: true},
		{name: "azure valid", provider: AzureProvider, bucket: "my-azure-bucket", wantErr: false},
		{name: "azure invalid dot", provider: AzureProvider, bucket: "my.azure.bucket", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBucketNameWithEndpoint(tt.provider, tt.bucket, "")
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for provider=%q bucket=%q", tt.provider, tt.bucket)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error for provider=%q bucket=%q: %v", tt.provider, tt.bucket, err)
			}
		})
	}
}

func TestValidateBucketNameWithEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		bucket   string
		endpoint string
		wantErr  bool
	}{
		{name: "aws s3 still strict", provider: S3Provider, bucket: "EllrottLab", wantErr: true},
		{name: "custom endpoint s3 compatible accepts uppercase", provider: S3Provider, bucket: "EllrottLab", endpoint: "https://rgw.ohsu.edu"},
		{name: "custom endpoint still rejects slashes", provider: S3Provider, bucket: "bad/name", endpoint: "https://rgw.ohsu.edu", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBucketNameWithEndpoint(tt.provider, tt.bucket, tt.endpoint)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for provider=%q bucket=%q endpoint=%q", tt.provider, tt.bucket, tt.endpoint)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error for provider=%q bucket=%q endpoint=%q: %v", tt.provider, tt.bucket, tt.endpoint, err)
			}
		})
	}
}

func TestParseBucketProvider(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{in: "", want: S3Provider, ok: true},
		{in: "s3", want: S3Provider, ok: true},
		{in: "gs", want: GCSProvider, ok: true},
		{in: "azure", want: AzureProvider, ok: true},
		{in: "file", want: FileProvider, ok: true},
		{in: "unsupported", ok: false},
	}

	for _, tt := range tests {
		got, err := ParseBucketProvider(tt.in)
		if tt.ok {
			if err != nil {
				t.Fatalf("ParseBucketProvider(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("ParseBucketProvider(%q)=%q want %q", tt.in, got, tt.want)
			}
			continue
		}
		if err == nil {
			t.Fatalf("ParseBucketProvider(%q) expected error", tt.in)
		}
	}
}

func TestDeriveCredentialID(t *testing.T) {
	first := DeriveCredentialID("shared-bucket", "s3", "us-east-1", "https://s3.example.org/", "access-a")
	second := DeriveCredentialID("shared-bucket", "s3", "us-east-1", "https://s3.example.org", "access-a")
	if first == "" {
		t.Fatalf("expected derived credential id")
	}
	if first != second {
		t.Fatalf("expected endpoint normalization to keep derived id stable: %q != %q", first, second)
	}

	rotatedSecret := DeriveCredentialID("shared-bucket", "s3", "us-east-1", "https://s3.example.org", "access-a")
	if first != rotatedSecret {
		t.Fatalf("secret rotation must not affect derived id")
	}

	differentAccessKey := DeriveCredentialID("shared-bucket", "s3", "us-east-1", "https://s3.example.org", "access-b")
	if first == differentAccessKey {
		t.Fatalf("different access keys for the same bucket must produce distinct internal ids")
	}
}
