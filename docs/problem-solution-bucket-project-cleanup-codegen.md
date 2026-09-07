# Problem and Solution: Bucket Project Cleanup Endpoints and Client Response Naming

## Problem

This branch adds two bucket-management API operations to the local bucket OpenAPI surface:

- `DELETE /data/buckets/{bucket}/scopes`
- `DELETE /data/projects/{organization}/{project_id}`

The first removes one org/project scope from an existing bucket credential. The second deletes Syfon records for one project and then removes all bucket scopes attached to that same org/project pair.

Those API additions are real runtime behavior changes, so `make gen` now legitimately changes both:

- `apigen/server/bucketapi/bucket.gen.go`
- `apigen/client/bucketapi/bucket.gen.go`

The branch also exposed a code generation failure in the bucket client.

## Why `make gen` Started Failing

The new project cleanup operation returns a JSON schema named `DeleteProjectDataResponse`:

```yaml
responses:
  '200':
    description: Project Syfon records and bucket scopes removed
    content:
      application/json:
        schema:
          $ref: '#/components/schemas/DeleteProjectDataResponse'
```

At the same time, `oapi-codegen` generates `ClientWithResponses` wrapper types using the operation name plus `Response`.

That meant the bucket client tried to emit both:

```go
type DeleteProjectDataResponse struct { ... }     // schema model
type DeleteProjectDataResponse struct { ... }     // response wrapper
```

This is a pure code generation naming collision. It is not a handwritten code bug and not a runtime API bug.

## Runtime Behavior Added by This Branch

### Delete a single bucket scope

Route:

```text
DELETE /data/buckets/{bucket}/scopes?organization=<org>&project_id=<project>
```

Runtime path:

- Route constant: `internal/common/routes.go`
- Handler registration: `internal/api/internaldrs/buckets.go`
- Handler: `handleInternalDeleteBucketScopeFiber`

Behavior:

- requires `bucket` path param
- requires `organization` and `project_id` query params
- authorizes against the target org/project with bucket-scope write permissions
- deletes exactly one scope association for that bucket credential and project
- returns `204 No Content`

### Delete all Syfon project data plus bucket scopes

Route:

```text
DELETE /data/projects/{organization}/{project_id}
```

Runtime path:

- Route constant: `internal/common/routes.go`
- Handler registration: `internal/api/internaldrs/buckets.go`
- Handler: `handleInternalDeleteProjectFiber`

Behavior:

1. validates `organization` and `project_id`
2. requires auth
3. authorizes delete/update against that org/project scope
4. deletes Syfon objects for the project via `DeleteBulkByScope`
5. lists bucket scopes and removes every matching scope for that org/project
6. returns JSON with:
   - `organization`
   - `project_id`
   - `deleted_objects`
   - `deleted_bucket_scopes`

## Current Fix

The bucket client generation config now sets:

```yaml
output-options:
  response-type-suffix: Resp
```

in:

- `apigen/codegen/client-oapi-bucket.yaml`

That keeps the schema model name unchanged:

```go
type DeleteProjectDataResponse struct { ... }
```

while renaming the generated response wrapper to:

```go
type DeleteProjectDataResp struct { ... }
```

## Why This Fix Is Correct

This fix is generator-scoped and low-risk:

- no wire-format change
- no route change
- no schema rename
- no handwritten compat shim in generated packages

It only tells `oapi-codegen` to stop colliding wrapper names with schema model names in the bucket client.

## Review Expectations

For this branch, `make gen` should now produce a diff that matches the real OpenAPI additions:

- server-side bucket contract additions for delete-scope and delete-project-cleanup
- client-side bucket contract additions for the same operations
- response wrapper names ending in `Resp` for bucket client `WithResponse` helpers

If the generated bucket client shows duplicate `DeleteProjectDataResponse` declarations again, the bucket client config is not using `response-type-suffix: Resp`.
