package core
import (
 "context"
 "testing"
 "github.com/calypr/syfon/apigen/server/drs"
 "github.com/calypr/syfon/internal/models"
 "github.com/calypr/syfon/internal/testutils"
 "github.com/calypr/syfon/internal/urlmanager"
)
func TestMergedContentPreservesReplicaLocation(t *testing.T) {
 db := &coreTestDB{MockDatabase: &testutils.MockDatabase{BucketScopes: map[string]models.BucketScope{
  "org|a": {Organization:"org", ProjectID:"a", Bucket:"bucket-a", PathPrefix:"a"},
  "org|b": {Organization:"org", ProjectID:"b", Bucket:"bucket-b", PathPrefix:"b"},
 }}}
 om := NewObjectManager(db, &capturingURLManager{})
 objs := []models.InternalObject{
  {DrsObject: drs.DrsObject{Id:"uuid-a", Checksums: []drs.Checksum{{Type:"sha256", Checksum:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, ControlledAccess: &[]string{"/organization/org/project/a"}}},
  {DrsObject: drs.DrsObject{Id:"uuid-b", Checksums: []drs.Checksum{{Type:"sha256", Checksum:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, ControlledAccess: &[]string{"/organization/org/project/b"}}},
 }
 original := "s3://bucket-a/a/file"
 single, err := om.SignObjectURL(context.Background(), &objs[0], original, urlmanager.SignOptions{})
 if err != nil {t.Fatal(err)}
 if single != "signed:"+original {t.Fatalf("single project changed replica: %s", single)}
 merged := canonicalizeContentObjects(objs)[0]
 signed, err := om.SignObjectURL(context.Background(), &merged, original, urlmanager.SignOptions{})
 if err != nil {t.Fatal(err)}
 if signed != "signed:"+original {t.Fatalf("merged read changed replica: got %s, want signed:%s", signed, original)}
}
