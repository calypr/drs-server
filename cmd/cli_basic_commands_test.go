package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/bucketapi"
	"github.com/calypr/syfon/apigen/internalapi"
	syclient "github.com/calypr/syfon/client"
)

func TestSyfonListAndRemoveCommands(t *testing.T) {
	server := newSyfonTestServer(t)
	defer server.Close()

	c, err := syclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	did := "11111111-1111-1111-1111-111111111111"
	storagePath := filepath.Join(server.StorageDir, "README.md")
	if err := os.WriteFile(storagePath, []byte("rm single scope"), 0o644); err != nil {
		t.Fatalf("seed storage object: %v", err)
	}
	if err := c.Index().Upsert(context.Background(), did, "s3://syfon-bucket/README.md", "README.md", 123, "", map[string][]string{"syfon": {"e2e"}}); err != nil {
		t.Fatalf("seed record: %v", err)
	}

	out, err := executeRootCommand(t, "--server", server.URL, "ls")
	if err != nil {
		t.Fatalf("ls failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, did) || !strings.Contains(out, "README.md") {
		t.Fatalf("ls output missing expected record: %s", out)
	}

	out, err = executeRootCommand(t, "--server", server.URL, "rm", "--did", did)
	if err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatalf("expected explicit storage purge conflict, got err=%v output=%s", err, out)
	}

	out, err = executeRootCommand(t, "--server", server.URL, "ls")
	if err != nil {
		t.Fatalf("ls after rm failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, did) {
		t.Fatalf("rejected deletion removed metadata: %s", out)
	}
	if _, err := os.Stat(storagePath); err != nil {
		t.Fatalf("rejected deletion modified backing storage: %v", err)
	}
}

func TestSyfonRemoveScopedControlledAccessOnly(t *testing.T) {
	server := newSyfonTestServer(t)
	defer server.Close()

	c, err := syclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	did := "11111111-1111-1111-1111-222222222222"
	storagePath := filepath.Join(server.StorageDir, "scoped.txt")
	if err := os.WriteFile(storagePath, []byte("rm scoped"), 0o644); err != nil {
		t.Fatalf("seed storage object: %v", err)
	}
	if err := c.Index().Upsert(context.Background(), did, "s3://syfon-bucket/scoped.txt", "scoped.txt", 99, "", map[string][]string{
		"syfon": {"e2e"},
		"other": {"x"},
	}); err != nil {
		t.Fatalf("seed multi-scope record: %v", err)
	}

	out, err := executeRootCommand(t, "--server", server.URL, "rm", "--did", did, "--organization", "syfon", "--project", "e2e")
	if err != nil {
		t.Fatalf("rm failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "removed scoped access /organization/syfon/project/e2e from "+did) {
		t.Fatalf("unexpected rm output: %s", out)
	}

	rec, err := c.Index().Get(context.Background(), did)
	if err != nil {
		t.Fatalf("expected record to remain: %v", err)
	}
	controlled := derefCLIStringSlice(rec.ControlledAccess)
	if len(controlled) != 1 || controlled[0] != "/organization/other/project/x" {
		t.Fatalf("unexpected controlled access after scoped remove: %+v", controlled)
	}
	if _, err := os.Stat(storagePath); err != nil {
		t.Fatalf("expected backing storage to remain, stat err=%v", err)
	}
}

func TestSyfonDownloadDefaultsToRecordFilename(t *testing.T) {
	server := newSyfonTestServer(t)
	defer server.Close()

	tmp := t.TempDir()
	t.Chdir(tmp)

	srcPath := filepath.Join(server.StorageDir, "source.txt")
	srcData := []byte("download default filename test")
	if err := os.WriteFile(srcPath, srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	c, err := syclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	did := "22222222-2222-2222-2222-222222222222"
	recordName := "nested/path/README.md"
	// Store record with explicit filename and a storage-root URL so download can resolve locally.
	if err := c.Index().Upsert(context.Background(), did, "s3://syfon-bucket/source.txt", recordName, int64(len(srcData)), "", map[string][]string{"syfon": {"e2e"}}); err != nil {
		t.Fatalf("seed record with file url: %v", err)
	}

	out, err := executeRootCommand(t, "--server", server.URL, "download", "--did", did)
	if err != nil {
		t.Fatalf("download failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "downloaded "+did+" -> README.md") {
		t.Fatalf("unexpected download output: %s", out)
	}

	gotPath := filepath.Join(tmp, "README.md")
	got, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatalf("expected README.md to be created: %v", err)
	}
	if string(got) != string(srcData) {
		t.Fatalf("downloaded data mismatch")
	}
	if _, err := os.Stat(filepath.Join(tmp, "nested")); !os.IsNotExist(err) {
		t.Fatalf("expected nested path prefix to be ignored, stat err=%v", err)
	}
}

func TestSyfonBucketListAndRemoveCommands(t *testing.T) {
	server := newSyfonTestServer(t)
	defer server.Close()

	c, err := syclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Buckets().Put(context.Background(), bucketapi.PutBucketRequest{
		Bucket:       "test-bucket-cli",
		Provider:     stringPtr("file"),
		Region:       stringPtr("us-east-1"),
		AccessKey:    stringPtr("ak"),
		SecretKey:    stringPtr("sk"),
		Organization: "cli-tests",
		ProjectId:    "bucket-list",
	}); err != nil {
		t.Fatalf("seed bucket: %v", err)
	}
	if err := c.Index().Upsert(context.Background(), "bucket-visible-object", "s3://test-bucket-cli/visible.txt", "visible.txt", 7, "", map[string][]string{"cli-tests": {"bucket-list"}}); err != nil {
		t.Fatalf("seed visible object: %v", err)
	}

	out, err := executeRootCommand(t, "--server", server.URL, "bucket", "list")
	if err != nil {
		t.Fatalf("bucket list failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "test-bucket-cli") {
		t.Fatalf("bucket list missing bucket: %s", out)
	}

	out, err = executeRootCommand(t, "--server", server.URL, "bucket", "remove", "test-bucket-cli")
	if err != nil {
		t.Fatalf("bucket remove failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "bucket removed: test-bucket-cli") {
		t.Fatalf("unexpected bucket remove output: %s", out)
	}

	out, err = executeRootCommand(t, "--server", server.URL, "bucket", "list")
	if err != nil {
		t.Fatalf("bucket list after remove failed: %v output=%s", err, out)
	}
	if strings.Contains(out, "test-bucket-cli") {
		t.Fatalf("expected bucket to be removed, output=%s", out)
	}
}

func TestSyfonBucketAddCredentialAndScopesCommands(t *testing.T) {
	server := newSyfonTestServer(t)
	defer server.Close()

	out, err := executeRootCommand(t,
		"--server", server.URL,
		"bucket", "add", "test-bucket-cli",
		"--provider", "file",
		"--region", "us-east-1",
	)
	if err != nil {
		t.Fatalf("bucket add failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "bucket credential configured: test-bucket-cli") {
		t.Fatalf("unexpected bucket add output: %s", out)
	}

	out, err = executeRootCommand(t,
		"--server", server.URL,
		"bucket", "list",
	)
	if err != nil {
		t.Fatalf("bucket list after add failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "test-bucket-cli") {
		t.Fatalf("expected bucket to be visible before scopes are added: %s", out)
	}

	out, err = executeRootCommand(t,
		"--server", server.URL,
		"bucket", "add-organization", "cli-tests",
		"--path", "gs://test-bucket-cli/program-root",
	)
	if err != nil {
		t.Fatalf("bucket add-organization failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "bucket organization scope configured: bucket=test-bucket-cli org=cli-tests") {
		t.Fatalf("unexpected bucket add-organization output: %s", out)
	}

	out, err = executeRootCommand(t,
		"--server", server.URL,
		"bucket", "add-project", "cli-tests", "bucket-cli",
		"--path", "gs://test-bucket-cli/program-root/project-subpath",
	)
	if err != nil {
		t.Fatalf("bucket add-project failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "bucket project scope configured: bucket=test-bucket-cli org=cli-tests project=bucket-cli") {
		t.Fatalf("unexpected bucket add-project output: %s", out)
	}

	out, err = executeRootCommand(t,
		"--server", server.URL,
		"bucket", "list-scopes", "test-bucket-cli",
	)
	if err != nil {
		t.Fatalf("bucket list-scopes failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "cli-tests") || !strings.Contains(out, "bucket-cli") || !strings.Contains(out, "file://test-bucket-cli/program-root/project-subpath") {
		t.Fatalf("unexpected bucket list-scopes output: %s", out)
	}

	out, err = executeRootCommand(t,
		"--server", server.URL,
		"bucket", "remove-scope", "test-bucket-cli", "cli-tests", "file://test-bucket-cli/program-root/project-subpath", "bucket-cli",
	)
	if err != nil {
		t.Fatalf("bucket remove-scope project failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "bucket scope removed: bucket=test-bucket-cli org=cli-tests project=bucket-cli path=file://test-bucket-cli/program-root/project-subpath") {
		t.Fatalf("unexpected bucket remove-scope project output: %s", out)
	}

	out, err = executeRootCommand(t,
		"--server", server.URL,
		"bucket", "remove-scope", "test-bucket-cli", "cli-tests", "file://test-bucket-cli/program-root",
	)
	if err != nil {
		t.Fatalf("bucket remove-scope org failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "bucket scope removed: bucket=test-bucket-cli org=cli-tests path=file://test-bucket-cli/program-root") {
		t.Fatalf("unexpected bucket remove-scope org output: %s", out)
	}

	out, err = executeRootCommand(t,
		"--server", server.URL,
		"bucket", "list-scopes", "test-bucket-cli",
	)
	if err != nil {
		t.Fatalf("bucket list-scopes after removals failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "no mappings configured for bucket test-bucket-cli") {
		t.Fatalf("unexpected bucket list-scopes after removals output: %s", out)
	}
}

func TestSyfonDeleteProjectCommand(t *testing.T) {
	server := newSyfonTestServer(t)
	defer server.Close()

	c, err := syclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Buckets().Put(context.Background(), bucketapi.PutBucketRequest{
		Bucket:       "delete-project-bucket",
		Provider:     stringPtr("file"),
		Region:       stringPtr("us-east-1"),
		AccessKey:    stringPtr("ak"),
		SecretKey:    stringPtr("sk"),
		Organization: "cli-tests",
		ProjectId:    "cleanup-me",
	}); err != nil {
		t.Fatalf("seed bucket scope: %v", err)
	}
	if err := c.Index().Upsert(context.Background(), "delete-project-1", "s3://delete-project-bucket/one.txt", "one.txt", 1, "", map[string][]string{"cli-tests": {"cleanup-me"}}); err != nil {
		t.Fatalf("seed record one: %v", err)
	}
	if err := c.Index().Upsert(context.Background(), "delete-project-2", "s3://delete-project-bucket/two.txt", "two.txt", 2, "", map[string][]string{"cli-tests": {"cleanup-me"}}); err != nil {
		t.Fatalf("seed record two: %v", err)
	}
	if err := c.Index().Upsert(context.Background(), "delete-project-keep", "s3://delete-project-bucket/keep.txt", "keep.txt", 3, "", map[string][]string{"cli-tests": {"keep-me"}}); err != nil {
		t.Fatalf("seed keep record: %v", err)
	}

	out, err := executeRootCommand(t, "--server", server.URL, "delete-project", "cli-tests/cleanup-me")
	if err != nil {
		t.Fatalf("delete-project failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "deleted project cli-tests/cleanup-me: 2 records removed, 1 project bucket mappings removed") {
		t.Fatalf("unexpected delete-project output: %s", out)
	}

	listOut, err := executeRootCommand(t, "--server", server.URL, "ls")
	if err != nil {
		t.Fatalf("ls after delete-project failed: %v output=%s", err, listOut)
	}
	if strings.Contains(listOut, "delete-project-1") || strings.Contains(listOut, "delete-project-2") {
		t.Fatalf("expected project records to be removed, output=%s", listOut)
	}
	if !strings.Contains(listOut, "delete-project-keep") {
		t.Fatalf("expected unrelated record to remain, output=%s", listOut)
	}

	scopeOut, err := executeRootCommand(t, "--server", server.URL, "bucket", "list-scopes", "delete-project-bucket")
	if err != nil {
		t.Fatalf("bucket list-scopes after delete-project failed: %v output=%s", err, scopeOut)
	}
	if strings.Contains(scopeOut, "cleanup-me") {
		t.Fatalf("expected cleanup-me mapping to be removed, output=%s", scopeOut)
	}
}

func TestSyfonCopyProjectCommand(t *testing.T) {
	server := newSyfonTestServer(t)
	defer server.Close()

	// 1. Create a target storage directory
	targetDir := t.TempDir()

	// 2. Add the target bucket credentials to the server using the CLI
	out, err := executeRootCommand(t,
		"--server", server.URL,
		"bucket", "add", "target-bucket",
		"--provider", "file",
		"--endpoint", targetDir,
	)
	if err != nil {
		t.Fatalf("failed to add target bucket: %v output=%s", err, out)
	}

	// 3. Add organization scope for destination (target-bucket) to test auto-creation of project scope
	out, err = executeRootCommand(t,
		"--server", server.URL,
		"bucket", "add-organization", "syfon-copy",
		"--path", "s3://target-bucket/copied-root",
	)
	if err != nil {
		t.Fatalf("failed to add destination organization: %v output=%s", err, out)
	}

	// 4. Seed a source file in the source bucket
	did := "33333333-3333-3333-3333-333333333333"
	fileName := "test-copy.txt"
	content := []byte("hello copy project")
	sourceFilePath := filepath.Join(server.StorageDir, fileName)
	if err := os.WriteFile(sourceFilePath, content, 0o644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	hash := sha256.Sum256(content)
	checksum := hex.EncodeToString(hash[:])

	c, err := syclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Index().Upsert(context.Background(), did, "s3://syfon-bucket/"+fileName, fileName, int64(len(content)), checksum, map[string][]string{"syfon": {"e2e"}}); err != nil {
		t.Fatalf("failed to seed record: %v", err)
	}

	// 5. Run the copy-project command with source/dest format
	out, err = executeRootCommand(t,
		"--server", server.URL,
		"copy-project", "syfon/e2e", "syfon-copy/e2e",
	)
	if err != nil {
		t.Fatalf("copy-project command failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "Downloading "+did+" -> ") {
		t.Fatalf("expected download stage output, got: %s", out)
	}
	if !strings.Contains(out, "Uploading "+did+" -> ") {
		t.Fatalf("expected upload stage output, got: %s", out)
	}
	if !strings.Contains(out, "Successfully copied project syfon/e2e to syfon-copy/e2e") {
		t.Fatalf("unexpected copy-project output: %s", out)
	}

	// 6. Verify the file exists in the target storage directory
	rec, err := c.Index().Get(context.Background(), did)
	if err != nil {
		t.Fatalf("failed to get updated index record: %v", err)
	}
	if rec.AccessMethods == nil || len(*rec.AccessMethods) != 1 {
		t.Fatalf("expected exactly 1 access method, got: %+v", rec.AccessMethods)
	}
	targetURL := (*rec.AccessMethods)[0].AccessUrl.Url
	if !strings.Contains(targetURL, "://target-bucket/copied-root/e2e/") {
		t.Fatalf("expected access URL to be in target-bucket under copied-root/e2e, got: %s", targetURL)
	}

	parsed, err := url.Parse(targetURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("DEBUG: targetURL=%s", targetURL)
	t.Logf("DEBUG: parsed.Path=%s", parsed.Path)
	t.Logf("DEBUG: server.StorageDir=%s", server.StorageDir)
	t.Logf("DEBUG: targetDir=%s", targetDir)

	// Verify we can download the file using the client from the new target location.
	reader, err := c.Data().GetReader(context.Background(), did)
	if err != nil {
		t.Fatalf("failed to get reader: %v", err)
	}
	defer reader.Close()
	gotContent, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotContent) != string(content) {
		t.Fatalf("copied file content mismatch: expected %q, got %q", content, gotContent)
	}
}

func TestSyfonCopyProjectCommand_CreatesMissingDestinationScope(t *testing.T) {
	server := newSyfonTestServer(t)
	defer server.Close()

	content1 := []byte("hello copy missing destination 1")
	fileName1 := "test-copy-1.txt"
	sourceFilePath1 := filepath.Join(server.StorageDir, fileName1)
	if err := os.WriteFile(sourceFilePath1, content1, 0o644); err != nil {
		t.Fatalf("failed to write source file 1: %v", err)
	}
	hash1 := sha256.Sum256(content1)
	checksum1 := hex.EncodeToString(hash1[:])
	did1 := "44444444-4444-4444-4444-444444444444"

	content2 := []byte("hello copy missing destination 2")
	fileName2 := "test-copy-2.txt"
	sourceFilePath2 := filepath.Join(server.StorageDir, fileName2)
	if err := os.WriteFile(sourceFilePath2, content2, 0o644); err != nil {
		t.Fatalf("failed to write source file 2: %v", err)
	}
	hash2 := sha256.Sum256(content2)
	checksum2 := hex.EncodeToString(hash2[:])
	did2 := "55555555-5555-5555-5555-555555555555"

	c, err := syclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Index().Upsert(context.Background(), did1, "s3://syfon-bucket/"+fileName1, fileName1, int64(len(content1)), checksum1, map[string][]string{"syfon": {"e2e"}}); err != nil {
		t.Fatalf("failed to seed record 1: %v", err)
	}
	if err := c.Index().Upsert(context.Background(), did2, "s3://syfon-bucket/"+fileName2, fileName2, int64(len(content2)), checksum2, map[string][]string{"syfon": {"e2e"}}); err != nil {
		t.Fatalf("failed to seed record 2: %v", err)
	}

	out, err := executeRootCommand(t,
		"--server", server.URL,
		"copy-project", "syfon/e2e", "syfon-clone/e2e",
	)
	if err != nil {
		t.Fatalf("copy-project command failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "Creating organization scope mapping on bucket syfon-bucket: syfon-clone -> s3://syfon-bucket/organizations/syfon-clone") {
		t.Fatalf("expected organization scope creation output, got: %s", out)
	}
	if !strings.Contains(out, "Creating project scope mapping on bucket syfon-bucket: syfon-clone/e2e -> s3://syfon-bucket/organizations/syfon-clone/e2e") {
		t.Fatalf("expected project scope creation output, got: %s", out)
	}
	if !strings.Contains(out, "Successfully copied project syfon/e2e to syfon-clone/e2e (2 copied, 0 skipped, 2 total)") {
		t.Fatalf("unexpected copy-project output: %s", out)
	}

	scopes, err := c.Buckets().ListScopes(context.Background(), "syfon-bucket")
	if err != nil {
		t.Fatalf("failed to list scopes: %v", err)
	}
	foundOrg := false
	foundProject := false
	for _, scope := range scopes {
		pathValue := ""
		if scope.Path != nil {
			pathValue = *scope.Path
		}
		if scope.Organization == "syfon-clone" && scope.ProjectId == "" && strings.HasSuffix(pathValue, "/organizations/syfon-clone") {
			foundOrg = true
		}
		if scope.Organization == "syfon-clone" && scope.ProjectId == "e2e" && strings.HasSuffix(pathValue, "/organizations/syfon-clone/e2e") {
			foundProject = true
		}
	}
	if !foundOrg || !foundProject {
		t.Fatalf("expected destination scopes to be created, got: %+v", scopes)
	}

	rec1, err := c.Index().Get(context.Background(), did1)
	if err != nil {
		t.Fatalf("failed to get updated index record 1: %v", err)
	}
	if rec1.AccessMethods == nil || len(*rec1.AccessMethods) != 1 {
		t.Fatalf("expected exactly 1 access method for did1, got: %+v", rec1.AccessMethods)
	}
	targetURL1 := (*rec1.AccessMethods)[0].AccessUrl.Url
	if !strings.Contains(targetURL1, "://syfon-bucket/organizations/syfon-clone/e2e/") {
		t.Fatalf("expected did1 access URL to be rewritten into destination scope, got: %s", targetURL1)
	}

	reader1, err := c.Data().GetReader(context.Background(), did1)
	if err != nil {
		t.Fatalf("failed to get reader for did1: %v", err)
	}
	defer reader1.Close()
	gotContent1, err := io.ReadAll(reader1)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotContent1) != string(content1) {
		t.Fatalf("did1 content mismatch: expected %q, got %q", content1, gotContent1)
	}

	rec2, err := c.Index().Get(context.Background(), did2)
	if err != nil {
		t.Fatalf("failed to get index record 2: %v", err)
	}
	if rec2.AccessMethods == nil || len(*rec2.AccessMethods) != 1 {
		t.Fatalf("expected exactly 1 access method for did2, got: %+v", rec2.AccessMethods)
	}
	targetURL2 := (*rec2.AccessMethods)[0].AccessUrl.Url
	if !strings.Contains(targetURL2, "://syfon-bucket/organizations/syfon-clone/e2e/") {
		t.Fatalf("expected did2 access URL to be rewritten into destination scope, got: %s", targetURL2)
	}
}

func TestSyfonCopyProjectCommand_Individual(t *testing.T) {
	server := newSyfonTestServer(t)
	defer server.Close()

	targetDir := t.TempDir()

	out, err := executeRootCommand(t,
		"--server", server.URL,
		"bucket", "add", "target-bucket",
		"--provider", "file",
		"--endpoint", targetDir,
	)
	if err != nil {
		t.Fatalf("failed to add target bucket: %v output=%s", err, out)
	}

	out, err = executeRootCommand(t,
		"--server", server.URL,
		"bucket", "add-organization", "syfon-individual",
		"--path", "s3://target-bucket/individual-root",
	)
	if err != nil {
		t.Fatalf("failed to add destination organization: %v output=%s", err, out)
	}

	content1 := []byte("hello copy individual 1")
	fileName1 := "test-copy-1.txt"
	sourceFilePath1 := filepath.Join(server.StorageDir, fileName1)
	if err := os.WriteFile(sourceFilePath1, content1, 0o644); err != nil {
		t.Fatalf("failed to write source file 1: %v", err)
	}
	hash1 := sha256.Sum256(content1)
	checksum1 := hex.EncodeToString(hash1[:])
	did1 := "44444444-4444-4444-4444-444444444444"

	content2 := []byte("hello copy individual 2")
	fileName2 := "test-copy-2.txt"
	sourceFilePath2 := filepath.Join(server.StorageDir, fileName2)
	if err := os.WriteFile(sourceFilePath2, content2, 0o644); err != nil {
		t.Fatalf("failed to write source file 2: %v", err)
	}
	hash2 := sha256.Sum256(content2)
	checksum2 := hex.EncodeToString(hash2[:])
	did2 := "55555555-5555-5555-5555-555555555555"

	c, err := syclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Index().Upsert(context.Background(), did1, "s3://syfon-bucket/"+fileName1, fileName1, int64(len(content1)), checksum1, map[string][]string{"syfon": {"e2e"}}); err != nil {
		t.Fatalf("failed to seed record 1: %v", err)
	}
	if err := c.Index().Upsert(context.Background(), did2, "s3://syfon-bucket/"+fileName2, fileName2, int64(len(content2)), checksum2, map[string][]string{"syfon": {"e2e"}}); err != nil {
		t.Fatalf("failed to seed record 2: %v", err)
	}

	out, err = executeRootCommand(t,
		"--server", server.URL,
		"copy-project", "syfon/e2e", "syfon-individual/e2e",
		"-I", did1,
	)
	if err != nil {
		t.Fatalf("copy-project command failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "Retrieving individual record "+did1+" from project syfon/e2e") {
		t.Fatalf("expected individual retrieval output, got: %s", out)
	}
	if !strings.Contains(out, "Successfully copied project syfon/e2e to syfon-individual/e2e (1 copied, 0 skipped, 1 total)") {
		t.Fatalf("unexpected copy-project output: %s", out)
	}

	rec1, err := c.Index().Get(context.Background(), did1)
	if err != nil {
		t.Fatalf("failed to get updated index record 1: %v", err)
	}
	if rec1.AccessMethods == nil || len(*rec1.AccessMethods) != 1 {
		t.Fatalf("expected exactly 1 access method for did1, got: %+v", rec1.AccessMethods)
	}
	targetURL1 := (*rec1.AccessMethods)[0].AccessUrl.Url
	if !strings.Contains(targetURL1, "://target-bucket/individual-root/e2e/") {
		t.Fatalf("expected did1 access URL to be in target bucket destination scope, got: %s", targetURL1)
	}

	reader1, err := c.Data().GetReader(context.Background(), did1)
	if err != nil {
		t.Fatalf("failed to get reader for did1: %v", err)
	}
	defer reader1.Close()
	gotContent1, err := io.ReadAll(reader1)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotContent1) != string(content1) {
		t.Fatalf("did1 content mismatch: expected %q, got %q", content1, gotContent1)
	}

	rec2, err := c.Index().Get(context.Background(), did2)
	if err != nil {
		t.Fatalf("failed to get index record 2: %v", err)
	}
	if rec2.AccessMethods == nil || len(*rec2.AccessMethods) != 1 {
		t.Fatalf("expected exactly 1 access method for did2, got: %+v", rec2.AccessMethods)
	}
	targetURL2 := (*rec2.AccessMethods)[0].AccessUrl.Url
	if strings.Contains(targetURL2, "://target-bucket/individual-root/e2e/") {
		t.Fatalf("expected did2 to remain in the source scope, got: %s", targetURL2)
	}
}

func TestSyfonCopyProjectCommand_SkipsBrokenRecords(t *testing.T) {
	server := newSyfonTestServer(t)
	defer server.Close()

	goodContent := []byte("good copy record")
	goodName := "good-copy.txt"
	goodPath := filepath.Join(server.StorageDir, goodName)
	if err := os.WriteFile(goodPath, goodContent, 0o644); err != nil {
		t.Fatalf("failed to write good source file: %v", err)
	}
	goodHash := sha256.Sum256(goodContent)
	goodChecksum := hex.EncodeToString(goodHash[:])
	goodDID := "77777777-7777-7777-7777-777777777777"

	badContent := []byte("too short")
	badName := "bad-copy.txt"
	badPath := filepath.Join(server.StorageDir, badName)
	if err := os.WriteFile(badPath, badContent, 0o644); err != nil {
		t.Fatalf("failed to write bad source file: %v", err)
	}
	badHash := sha256.Sum256(badContent)
	badChecksum := hex.EncodeToString(badHash[:])
	badDID := "88888888-8888-8888-8888-888888888888"

	c, err := syclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Index().Upsert(context.Background(), goodDID, "s3://syfon-bucket/"+goodName, goodName, int64(len(goodContent)), goodChecksum, map[string][]string{"syfon": {"e2e"}}); err != nil {
		t.Fatalf("failed to seed good record: %v", err)
	}
	if err := c.Index().Upsert(context.Background(), badDID, "s3://syfon-bucket/"+badName, badName, int64(len(badContent))+1024, badChecksum, map[string][]string{"syfon": {"e2e"}}); err != nil {
		t.Fatalf("failed to seed bad record: %v", err)
	}

	out, err := executeRootCommand(t,
		"--server", server.URL,
		"copy-project", "syfon/e2e", "syfon-skip/e2e",
	)
	if err != nil {
		t.Fatalf("copy-project command failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "warning: skipping "+badDID) {
		t.Fatalf("expected skip warning for bad record, got: %s", out)
	}
	if !strings.Contains(out, "Successfully copied project syfon/e2e to syfon-skip/e2e (1 copied, 1 skipped, 2 total)") {
		t.Fatalf("unexpected copy-project output: %s", out)
	}

	goodRec, err := c.Index().Get(context.Background(), goodDID)
	if err != nil {
		t.Fatalf("failed to get updated good record: %v", err)
	}
	if goodRec.AccessMethods == nil || len(*goodRec.AccessMethods) != 1 {
		t.Fatalf("expected exactly 1 access method for good record, got: %+v", goodRec.AccessMethods)
	}
	goodTargetURL := (*goodRec.AccessMethods)[0].AccessUrl.Url
	if !strings.Contains(goodTargetURL, "://syfon-bucket/organizations/syfon-skip/e2e/") {
		t.Fatalf("expected good record to be rewritten into destination scope, got: %s", goodTargetURL)
	}

	reader, err := c.Data().GetReader(context.Background(), goodDID)
	if err != nil {
		t.Fatalf("failed to get reader for good record: %v", err)
	}
	defer reader.Close()
	gotGoodContent, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotGoodContent) != string(goodContent) {
		t.Fatalf("good record content mismatch: expected %q, got %q", goodContent, gotGoodContent)
	}

	badRec, err := c.Index().Get(context.Background(), badDID)
	if err != nil {
		t.Fatalf("failed to get bad record: %v", err)
	}
	if badRec.AccessMethods == nil || len(*badRec.AccessMethods) != 1 {
		t.Fatalf("expected exactly 1 access method for bad record, got: %+v", badRec.AccessMethods)
	}
	badTargetURL := (*badRec.AccessMethods)[0].AccessUrl.Url
	if strings.Contains(badTargetURL, "://syfon-bucket/organizations/syfon-skip/e2e/") {
		t.Fatalf("expected bad record to remain in source scope, got: %s", badTargetURL)
	}
}

func TestSyfonCopyProjectCommand_AcrossInstances(t *testing.T) {
	sourceServer := newSyfonTestServer(t)
	defer sourceServer.Close()
	targetServer := newSyfonTestServer(t)
	defer targetServer.Close()

	did := "99999999-9999-9999-9999-999999999999"
	fileName := "cross-instance.txt"
	content := []byte("copy project across syfon instances")
	sourceFilePath := filepath.Join(sourceServer.StorageDir, fileName)
	if err := os.WriteFile(sourceFilePath, content, 0o644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}
	hash := sha256.Sum256(content)
	checksum := hex.EncodeToString(hash[:])

	sourceClient, err := syclient.New(sourceServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceClient.Index().Upsert(context.Background(), did, "s3://syfon-bucket/"+fileName, fileName, int64(len(content)), checksum, map[string][]string{"syfon": {"e2e"}}); err != nil {
		t.Fatalf("failed to seed source record: %v", err)
	}

	out, err := executeRootCommand(t,
		"--server", targetServer.URL,
		"bucket", "add-organization", "syfon-copy",
		"--path", "s3://syfon-bucket/copied-root",
	)
	if err != nil {
		t.Fatalf("failed to configure target destination org scope: %v output=%s", err, out)
	}

	out, err = executeRootCommand(t,
		"--server", sourceServer.URL,
		"copy-project", "syfon/e2e", "syfon-copy/e2e",
		"--target-server", targetServer.URL,
	)
	if err != nil {
		t.Fatalf("copy-project across instances failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "Successfully copied project syfon/e2e to syfon-copy/e2e (1 copied, 0 skipped, 1 total)") {
		t.Fatalf("unexpected cross-instance copy output: %s", out)
	}

	targetClient, err := syclient.New(targetServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	targetRec, err := targetClient.Index().Get(context.Background(), did)
	if err != nil {
		t.Fatalf("failed to get copied target record: %v", err)
	}
	if targetRec.Name == nil || *targetRec.Name != fileName {
		t.Fatalf("unexpected target name: %+v", targetRec.Name)
	}
	if targetRec.Hashes == nil || (*targetRec.Hashes)["sha256"] != checksum {
		t.Fatalf("unexpected target hashes: %+v", targetRec.Hashes)
	}
	if targetRec.AccessMethods == nil || len(*targetRec.AccessMethods) != 1 || !strings.Contains((*targetRec.AccessMethods)[0].AccessUrl.Url, "://syfon-bucket/copied-root/e2e/") {
		t.Fatalf("unexpected target access methods: %+v", targetRec.AccessMethods)
	}

	reader, err := targetClient.Data().GetReader(context.Background(), did)
	if err != nil {
		t.Fatalf("failed to read copied target file: %v", err)
	}
	defer reader.Close()
	gotContent, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotContent) != string(content) {
		t.Fatalf("target content mismatch: expected %q, got %q", content, gotContent)
	}

	sourceRec, err := sourceClient.Index().Get(context.Background(), did)
	if err != nil {
		t.Fatalf("failed to get original source record: %v", err)
	}
	if sourceRec.AccessMethods == nil || len(*sourceRec.AccessMethods) != 1 || (*sourceRec.AccessMethods)[0].AccessUrl.Url != "s3://syfon-bucket/"+fileName {
		t.Fatalf("source record should remain unchanged, got: %+v", sourceRec.AccessMethods)
	}
}

func TestSyfonCopyProjectRefsCommand(t *testing.T) {
	sourceServer := newSyfonTestServer(t)
	defer sourceServer.Close()
	targetServer := newSyfonTestServer(t)
	defer targetServer.Close()

	c, err := syclient.New(sourceServer.URL)
	if err != nil {
		t.Fatal(err)
	}

	did := "66666666-6666-6666-6666-666666666666"
	fileName := "refs-only.txt"
	description := "metadata only copy"
	content := []byte("copy refs test")
	sourceFilePath := filepath.Join(sourceServer.StorageDir, fileName)
	if err := os.WriteFile(sourceFilePath, content, 0o644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}
	hash := sha256.Sum256(content)
	checksum := hex.EncodeToString(hash[:])

	if err := c.Index().Upsert(context.Background(), did, "s3://syfon-bucket/"+fileName, fileName, int64(len(content)), checksum, map[string][]string{"syfon": {"e2e"}}); err != nil {
		t.Fatalf("failed to seed record: %v", err)
	}
	sourceRec, err := c.Index().Get(context.Background(), did)
	if err != nil {
		t.Fatalf("failed to get seeded record: %v", err)
	}
	sourceUpdate := internalapi.InternalRecord{
		Did:              sourceRec.Did,
		AccessMethods:    sourceRec.AccessMethods,
		ControlledAccess: sourceRec.ControlledAccess,
		Description:      &description,
		Name:             sourceRec.Name,
		Hashes:           sourceRec.Hashes,
		Organization:     sourceRec.Organization,
		Project:          sourceRec.Project,
		Size:             sourceRec.Size,
	}
	if _, err := c.Index().Update(context.Background(), did, sourceUpdate); err != nil {
		t.Fatalf("failed to enrich seeded record: %v", err)
	}

	out, err := executeRootCommand(t,
		"--server", sourceServer.URL,
		"copy-project-refs", "syfon/e2e",
		"--target-server", targetServer.URL,
	)
	if err != nil {
		t.Fatalf("copy-project-refs command failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "Successfully copied 1 records for syfon/e2e") {
		t.Fatalf("unexpected copy-project-refs output: %s", out)
	}

	targetClient, err := syclient.New(targetServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	targetRec, err := targetClient.Index().Get(context.Background(), did)
	if err != nil {
		t.Fatalf("failed to get copied target record: %v", err)
	}
	if targetRec.Name == nil || *targetRec.Name != fileName {
		t.Fatalf("unexpected target name: %+v", targetRec.Name)
	}
	if targetRec.Description == nil || *targetRec.Description != description {
		t.Fatalf("unexpected target description: %+v", targetRec.Description)
	}
	if targetRec.Size == nil || *targetRec.Size != int64(len(content)) {
		t.Fatalf("unexpected target size: %+v", targetRec.Size)
	}
	if targetRec.Hashes == nil || (*targetRec.Hashes)["sha256"] != checksum {
		t.Fatalf("unexpected target hashes: %+v", targetRec.Hashes)
	}
	if targetRec.AccessMethods == nil || len(*targetRec.AccessMethods) != 1 {
		t.Fatalf("unexpected target access methods: %+v", targetRec.AccessMethods)
	}
	if got := (*targetRec.AccessMethods)[0].AccessUrl.Url; got != "s3://syfon-bucket/"+fileName {
		t.Fatalf("unexpected target access url: %s", got)
	}
	if targetRec.ControlledAccess == nil || len(*targetRec.ControlledAccess) != 1 || (*targetRec.ControlledAccess)[0] != "/organization/syfon/project/e2e" {
		t.Fatalf("unexpected target controlled access: %+v", targetRec.ControlledAccess)
	}
}

func stringPtr(v string) *string { return &v }

func derefCLIStringSlice(in *[]string) []string {
	if in == nil {
		return nil
	}
	return *in
}
