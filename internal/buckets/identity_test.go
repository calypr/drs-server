package buckets

import "testing"

func TestDeriveCredentialIDExcludesSecretsAndNormalizesEndpoint(t *testing.T) {
	first := DeriveCredentialID("shared-bucket", "s3", "us-east-1", "https://s3.example.org/", "access-a")
	second := DeriveCredentialID("shared-bucket", "s3", "us-east-1", "https://s3.example.org", "access-a")
	if first == "" || first != second {
		t.Fatalf("endpoint normalization changed credential id: %q != %q", first, second)
	}
	if first == DeriveCredentialID("shared-bucket", "s3", "us-east-1", "https://s3.example.org", "access-b") {
		t.Fatal("different access keys should produce distinct credential ids")
	}
}

func TestCredentialSecretRotationDoesNotChangeIdentity(t *testing.T) {
	first := Credential{
		Bucket: "shared-bucket", Provider: "s3", Region: "us-east-1",
		Endpoint: "https://s3.example.org", AccessKey: "access-a", SecretKey: "secret-a",
	}
	rotated := first
	rotated.SecretKey = "secret-b"
	if first.SecretKey == rotated.SecretKey {
		t.Fatal("test credentials must exercise different secrets")
	}
	credentialID := func(credential Credential) string {
		return DeriveCredentialID(credential.Bucket, credential.Provider, credential.Region, credential.Endpoint, credential.AccessKey)
	}
	if got, want := credentialID(first), credentialID(rotated); got != want {
		t.Fatalf("secret rotation changed credential identity: %q != %q", got, want)
	}
}
