# Composition backlog

This backlog records the remaining composition work after the first-principles cleanup. Each item describes a reproduced defect or a source-proven contract mismatch. File size alone does not qualify work for this list.

The following constraints apply to every item:

- Preserve the HTTP and DRS contracts unless an item identifies a current contract violation.
- Preserve the PostgreSQL schema and storage behavior.
- Do not add a database migration.
- Preserve the public Go client.
- Prefer one operation that owns an invariant over a sequence of loosely related calls.
- Keep generated protocol types at transport boundaries. Add a domain type only when it prevents an invalid state or carries a domain invariant.
- Test behavior through a public service, HTTP handler, or real database where practical.

## What adapter means in this document

Syfon still has necessary adapters at external boundaries:

- `internal/httpapi` implements the generated Fiber server interfaces and converts domain results into generated responses.
- `internal/persistence/sqlite` and `internal/persistence/postgres` adapt the shared store to each SQL dialect.
- `internal/storage/file`, `internal/storage/s3`, `internal/storage/gcs`, and `internal/storage/azure` adapt provider APIs to the storage contracts.
- `cmd/server` constructs those implementations and connects them to the application services.
- `cmd/validate` adapts Fiber requests to the OpenAPI validator.

These adapters have distinct external dependencies and should remain. The cleanup removed internal forwarding wrappers that added no policy or translation.

The remaining error problem is narrower. Some boundary translation paths discard a typed error before the HTTP layer can classify it. For example, `transfers.Service.IssueAccessBulk` converts every lookup, authorization, signing, and event-recording failure into an unresolved object ID. `httpapi.drsServer.GetBulkAccessURL` can then report only a 404 for that object. The backlog calls this problem "boundary translations collapse errors," not "adapters erase errors."

## Recurring issue classes

### Multi-step mutations lack one commit owner

Canonical duplicate repair and credential deletion compose one logical mutation from separately committed store calls. A failure can leave a state that no caller requested.

### Canonical identity is lost after persistence

Object registration and checksum handling can select a durable identity. Some callers continue to use the submitted ID or raw checksum for responses and usage accounting.

### Boundary translations collapse errors

Bulk access and startup wiring can convert a concrete failure into a missing result, successful startup, or generic response. The caller loses the information needed to choose the correct status or retry behavior.

### Shared provider contracts are incomplete

Multipart validation and ordering are not defined before provider dispatch. Providers therefore interpret the same request differently.

### Boundary inputs exceed downstream limits

Configuration decoding accepts ambiguous input, and some SQLite queries accept more parameters than SQLite can execute.

## Dependency order

| ID | Work package | Priority | Status | Depends on |
|---|---|---:|---|---|
| C01 | Stop canonical duplicate-repair panics | P0 | Complete | None |
| C02 | Return durable object identities and assign LFS accounting ownership | P0 | Complete | None |
| C03 | Commit canonical duplicate repair atomically | P0 | Complete | C01, with C02 reuse when practical |
| C04 | Preserve typed failures in bulk access | P1 | Complete | None |
| C05 | Commit credential and scope deletion atomically | P1 | Complete | Completed bucket configuration and cleanup fixes |
| C06 | Honor SQLite parameter limits | P1 | Ready | None |
| C07 | Enforce one multipart input contract | P1 | Ready | None |
| C08 | Reject ambiguous configuration | P1 | Ready | None |
| C09 | Remove the duplicate project-delete route and specification operation | P1 | Ready | Audit generated-client use |
| C10 | Fail startup when configured credentials cannot be used | P2 | Ready | Prefer C08 first |
| C11 | Use the canonical SHA rule for transfer attribution | P2 | Ready | Coordinate with C02 |
| C12 | Bound LFS limiter state and prevent quota-key collisions | P2 | Ready | None |

## C01. Stop canonical duplicate-repair panics

`collapseCanonicalGroup` in `internal/objects/canonical.go` writes to `publicRead` without requiring or initializing the map. `CollapseProjectChecksumDuplicates` calls that helper with a nil map. Project repair reaches this path through `internal/projects/storage/repair.go` and `internal/httpapi/maintenance_routes.go`.

Required result:

- Repairing a valid duplicate group does not panic.
- The repair preserves the existing merge and public-read rules.

Smallest change:

- Initialize the policy map in the repair path, or return the policy result instead of mutating optional caller state.
- Do not redesign the object service interface.

Verification:

- Create two same-project SQLite records with the same SHA.
- Run the public repair operation.
- Assert that the operation does not panic and leaves one canonical record with the expected alias.
- Run `go test ./internal/objects ./internal/projects/storage -count=1`.

## C02. Return durable object identities and assign LFS accounting ownership

Registration can canonicalize a submitted ID by checksum, but `objects.Service.RegisterScopedObjects` and LFS verification continue to use the submitted object. LFS download also replaces the accounting object ID with the requested OID. Two concurrent verification requests can both record usage after only one request consumes the pending-upload row.

Relevant code:

- `internal/objects/writes.go`
- `internal/persistence/store/object_writes.go`
- `internal/httpapi/records_routes.go`
- `internal/transfers/lfs/service.go`
- `internal/persistence/store/lfs.go`

Required result:

- Registration returns the durable stored records in request order.
- Create responses use the durable object ID.
- Upload and download usage use the durable object ID.
- Only the request that consumes pending upload metadata records upload usage.
- A losing verification retry still succeeds when the upload is already complete.

Smallest change:

- Return canonical records from registration, or resolve every prepared candidate after registration.
- Use those records for responses and LFS accounting.
- Remove the download accounting-ID override.
- Return an ownership boolean when pending metadata is consumed.

Verification:

- Register a second ID with the checksum of an existing UUID record.
- Assert that create, verify, and download accounting use the UUID.
- Start two verification calls at a barrier and assert one consumption and one usage event under the race detector.
- Run `go test ./internal/objects ./internal/httpapi ./internal/transfers/lfs ./internal/persistence/sqlite -count=1`.

## C03. Commit canonical duplicate repair atomically

`internal/objects/canonical.go` registers merged metadata, creates aliases, and deletes sibling records through separately committed store operations. An alias or deletion failure can leave a partially repaired project.

Required result:

- The merged object, aliases, and sibling deletions commit together.
- A failure preserves the original objects and aliases.
- Retrying a successful repair converges to the same state.

Smallest change:

- Add one purpose-specific persistence operation for canonical repair.
- Run the existing merge, alias, and deletion SQL in one content-write transaction.
- Keep authorization and audit decisions in `internal/objects`.
- Do not expose a generic transaction callback.

Dependencies:

- Complete C01 first so its panic does not hide transaction failures.
- Reuse the durable identity result from C02 if it fits the operation without coupling the changes.

Verification:

- Force alias insertion and sibling deletion failures in SQLite.
- Assert that the original objects and aliases remain unchanged.
- Assert that success followed by a retry has the same result.
- Run `go test ./internal/persistence/sqlite ./internal/objects ./internal/projects/storage -count=1`.

## C04. Preserve typed failures in bulk access

`transfers.Service.IssueAccessBulk` treats every error as an unresolved object. `httpapi.drsServer.GetBulkAccessURL` maps the unresolved list to a 404 error code inside the HTTP 200 batch response. A signer outage, an authorization denial, and a missing object therefore look identical.

Required result:

- Only missing objects and missing access URLs map to 404.
- Authorization and backend failures retain their existing error category.
- The HTTP 200 batch envelope remains unchanged.

Smallest change:

- Carry the existing typed error or error category for each failed item.
- Map each item through the shared HTTP error classifier.
- Do not create another API error hierarchy.

Verification:

- Exercise one request containing a missing object, a denied object, a signer failure, and an event-recorder failure.
- Assert the per-item codes and the absence of false successful URLs.
- Run `go test ./internal/transfers ./internal/httpapi -run 'Test.*BulkAccess' -count=1`.

## C05. Commit credential and scope deletion atomically

The bucket cleanup fix now reports lookup, listing, scope deletion, and credential deletion failures. The persistence store still deletes scopes and credentials with separate autocommit statements. A failed credential deletion can therefore leave the credential without its scopes.

Required result:

- Alias resolution, the last-scope decision, scope deletion, and credential deletion commit together.
- Other scopes preserve the credential.
- Cache invalidation occurs only after commit and includes the requested, canonical, and physical aliases.

Smallest change:

- Add one purpose-specific transactional delete operation to the store.
- Return the aliases that the service must invalidate after commit.
- Reuse the transaction helpers added for atomic bucket configuration writes.
- Do not add a generic transaction callback.

Verification:

- Use a SQLite trigger to fail credential deletion and assert that both the scope and credential survive.
- Cover last-scope success, sibling-scope retention, missing credentials, lookup errors, and both cache aliases.
- Add a conditional two-connection PostgreSQL behavior test.
- Run `go test ./internal/persistence/sqlite ./internal/buckets ./internal/storage -count=1`.

## C06. Honor SQLite parameter limits

Several queries in `internal/persistence/store` expand object IDs, checksums, or authorized resources without respecting `Dialect.MaxParameters`. SQLite sets that budget to 900.

Relevant code:

- `internal/persistence/store/objects.go`
- `internal/persistence/store/object_reads.go`
- `internal/persistence/store/usage.go`
- `internal/persistence/store/visibility.go`
- `internal/persistence/store/transfers.go`

Required result:

- Every SQLite query stays within the dialect parameter budget.
- Chunking preserves order, duplicate removal, pagination, aggregate values, and include-unscoped behavior.
- PostgreSQL behavior does not change.

Smallest change:

- Chunk ID and checksum hydration where results can be merged without changing semantics.
- For resource predicates that affect pagination or aggregation, restructure the query or enforce one documented service limit.
- Do not paginate each chunk independently and concatenate the results.

Verification:

- Exercise more than 900 IDs and more than 900 resources through object reads, DRS listing, bucket visibility, usage metrics, and transfer reports.
- Test through public store and service methods instead of SQL-string assertions.
- Run `go test ./internal/persistence/sqlite ./internal/objects ./internal/httpapi ./internal/usage -count=1`.

## C07. Enforce one multipart input contract

`internal/transfers/multipart.go` accepts non-positive, duplicate, and unsorted parts. Provider implementations do not handle those inputs consistently. `internal/storage/s3/multipart.go` also accepts a nil provider upload ID as an empty session ID.

The invalid part-number, duplicate-part, ordering, and empty-upload-ID defects are source-proven. Zero-byte upload behavior needs characterization before a policy change.

Required result:

- Upload IDs are non-empty.
- Part numbers are positive and unique.
- Completion order is deterministic before provider dispatch.
- Provider-specific ETags remain unchanged.

Smallest change:

- Validate and sort parts once in `internal/transfers`.
- Return the existing invalid-input error.
- Reject nil or empty S3 upload IDs.
- Characterize zero-byte LFS uploads before choosing a single-object upload or explicit rejection.

Verification:

- Cover zero, negative, duplicate, and unsorted part numbers at the route and service boundaries.
- Cover a nil S3 upload ID.
- Assert the same ordering contract for the supported local and S3 implementations.
- Run `go test ./internal/transfers ./internal/storage/s3 ./internal/storage/file -count=1`.

## C08. Reject ambiguous configuration

`internal/config/load.go` accepts unknown fields and trailing documents. Explicit `bucket_scopes` and scopes derived from `buckets[].resources` can silently replace each other. Validation trims identity fields, but bootstrap can persist their untrimmed values.

Required result:

- The loader accepts one document and rejects unknown fields.
- One normalized organization and project key maps to one scope.
- Identical duplicate scope definitions collapse to one value.
- Conflicting scope definitions fail with the field path in the error.
- Bucket, region, endpoint, and other identity fields use one normalized value.
- Access keys and secrets remain byte-exact.

Smallest change:

- Enable strict YAML and JSON decoding.
- Require end of file after the first document.
- Normalize only semantic identity fields before credential-ID derivation.
- Reject conflicting normalized scope keys.

Verification:

- Cover unknown top-level and nested fields.
- Cover valid and malformed trailing YAML and JSON.
- Cover identical and conflicting derived and explicit scopes.
- Cover whitespace through loading and credential seeding.
- Run `go test ./internal/config ./cmd/server -count=1`.

## C09. Remove the duplicate project-delete route and specification operation

The generated internal and bucket servers both register `DELETE /data/projects/:organization/:project_id`. Fiber runs the first registered handler. The merged OpenAPI document keeps the operation merged later. Runtime behavior, documentation, and generated clients can therefore describe different owners for the same method and path.

Relevant code:

- `internal/httpapi/routes.go`
- `apigen/openapi/internal.openapi.yaml`
- `apigen/openapi/bucket.openapi.yaml`
- `internal/httpapi/apidocs/specs.go`
- `cmd/server/options.go`

Required result:

- One generated interface owns the runtime route.
- One OpenAPI operation describes its response contract.
- Generated clients expose the canonical operation.

Smallest change:

- Audit public client call sites before choosing the canonical operation.
- Keep the internal maintenance operation unless compatibility evidence selects the bucket operation.
- Remove the duplicate source operation and route adapter.
- Regenerate the API and client code. Do not hand-edit generated Go files.

Verification:

- Assert that route registration produces one matching Fiber route.
- Exercise the canonical handler.
- Assert the merged operation ID, tags, and responses.
- Run `go test ./internal/httpapi ./internal/httpapi/apidocs ./cmd/server -count=1` and the public client suite.

## C10. Fail startup when configured credentials cannot be used

`credentialcipher.Cipher.Enabled` can validate a local key even when configuration selects AWS KMS. Server bootstrap also logs some configured credential save failures and continues to construct a runtime.

The defect is source-proven. The fix intentionally changes startup behavior for deployments that currently continue after a configuration failure.

Required result:

- Readiness validates the selected key manager.
- Runtime construction fails before serving if a configured credential cannot be persisted.
- The startup error identifies the affected configured bucket.

Smallest change:

- Make readiness specific to the selected key manager.
- Return or aggregate credential-seeding errors from runtime construction.
- Do not change the encrypted storage format.

Dependencies:

- Prefer C08 first so startup reports canonical configuration errors before persistence errors.

Verification:

- Select an invalid or unknown KMS while a valid local key exists and assert that runtime construction fails.
- Force credential save failures with and without dependent scopes.
- Assert that server routes are not constructed.
- Run `go test ./internal/persistence/credentialcipher ./cmd/server -count=1`.

## C11. Use the canonical SHA rule for transfer attribution

Transfer event construction stores the first trimmed raw SHA. Object identity uses `objects.CanonicalSHA256`, which removes the `sha256:` prefix, lowercases, validates, and deduplicates the checksum. Prefixed or uppercase values can therefore split usage across keys or disappear from filtered reports.

Required result:

- Object lookup, event persistence, event IDs, grant IDs, and SHA filters use one lowercase bare SHA.
- Existing lowercase values remain unchanged.

Smallest change:

- Call `objects.CanonicalSHA256` when constructing transfer events.
- Apply the same normalization to inbound SHA filters.
- Do not copy the checksum normalization logic.

Dependencies:

- Coordinate with C02 because both change usage identity, but neither implementation must block the other.

Verification:

- Use an object whose checksum contains an uppercase `sha256:` value.
- Issue access and download operations.
- Assert one canonical event and identical results for canonical and prefixed-uppercase filters.
- Run `go test ./internal/transfers ./internal/usage ./internal/persistence/sqlite -count=1`.

## C12. Bound LFS limiter state and prevent quota-key collisions

The package-level limiter maps in `internal/httpapi/lfs_routes.go` never evict client entries. The limiter also identifies an authorization value by its first 64 bytes. High-cardinality traffic grows the maps for the life of the process, and distinct long values with a shared prefix use one quota.

Required result:

- One server runtime owns its limiter state.
- Idle entries expire or the limiter enforces a fixed bound.
- The key uses the complete authorization value without storing the raw value.
- Existing limits and HTTP responses remain unchanged.

Smallest change:

- Construct an expiring limiter during route registration or runtime construction.
- Hash the complete authorization value for the map key.
- Add only the clock and cardinality seams required for deterministic tests.

Verification:

- Generate many unique keys, advance a test clock, and assert bounded cardinality.
- Use two long values with the same 64-byte prefix and assert separate quotas.
- Run the focused limiter tests under the race detector.

## Work that does not earn a place yet

Do not schedule these changes without new behavior evidence:

- Split `persistence/store` because its files are large.
- Split `storage.Manager` or replace its provider registration model.
- Add domain copies of generated maintenance types.
- Move transaction-time authorization out of persistence.
- Merge `projects/storage` into another package.
- Change local filesystem behavior or S3 URL fallback.
- Redesign commercial cloud providers before Syfon supports and tests them.
- Implement durable multipart sessions, abort, or expiry without a defined lifecycle contract.
- Implement the public `reconciliation_status` filter without an API-contract decision.

## Stopping rule

Stop composition work when all of the following statements are true:

- One operation owns each user-visible mutation and its commit boundary.
- Persisted canonical identities flow back to responses and accounting.
- Boundary code preserves typed failures until the HTTP or CLI serializer classifies them.
- Shared provider inputs have one validated contract before dispatch.
- Every backend receives inputs within its documented limits.
- A proposed change cannot name a reproduced behavior problem, a violated invariant, or a reader path that it removes.

Do not use production line count, test line count, file count, or file length as the stopping condition.
