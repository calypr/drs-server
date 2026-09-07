# Interface decisions

Interfaces belong with the code that calls them. SQLite and PostgreSQL can implement many capabilities without importing each consumer package because Go checks method sets structurally. `cmd/server` supplies compile-time assertions and composition. This avoids replacing `internal/db/interface.go` with the same interface collection under a new path.

Proposed ports use domain-owned values. They do not use generated API, Fiber, SQL row, or cloud SDK types. A concrete backend can still have a broad method set. No service receives that concrete set as one dependency.

## HTTP, access, and provider seams

| Current interface | Decision | Evidence and future owner |
| --- | --- | --- |
| `metricsObjectReader` | Move and narrow | Metrics uses two authorized object reads in `internal/api/metrics/server.go:27-30`. `internal/usage` owns an `ObjectReader` port; composition injects the existing object service rather than constructing another manager. |
| `authenticationPluginManagerInterface` | Delete | Its one method duplicates the public plugin authentication contract. `internal/access/authentication` stores the public interface or an adapter-local minimum. |
| `pluginManagerInterface` | Delete | Its one method duplicates the public authorization plugin contract. The access authentication integration uses the public wire owner unchanged. |
| `PublicError` | Move and make private | Only `internal/api/apiutil/errors.go:33` consumes it. `internal/httpapi/response` owns `publicError`; stable sentinel identity stays in `faults`. |
| `s3ObjectDeleter` | Retain and move | This two-method AWS SDK seam in `core/object_storage_delete.go:36` supports the S3 delete consumer. Move it beside `internal/storage/s3` delete code. Do not widen it to the full SDK client. |
| `s3HeadObjectClient` | Retain and move | This one-method inspection seam in `core/object_storage_inspect.go:114` moves beside the S3 prober. |
| `s3ListObjectsV2Client` | Retain and move | This one-method inventory seam in `core/object_storage_inventory.go:171` moves beside S3 inventory. Project-level comparison remains in `maintenance/projectstorage`. |
| `CredentialKeyManager` | Move and retain | Its three operations are specific to credential envelopes. Move it to `credentialcipher`; preserve current per-operation manager and environment selection until a separate explicit-protector change passes compatibility tests. |
| `bucketInvalidatingSigner` | Retain and move | The private one-method seam lets the storage manager invalidate a resolved provider cache. Move it beside `storage.Manager`; it remains distinct from the bucket catalog's consumer-owned invalidator. |

These SDK interfaces are good testing seams. Their small method sets match real consumers. Reducing interface counts is not a reason to replace them with concrete clients.

## Current `internal/db` interfaces

| Current interface | Decision | Evidence and future owner |
| --- | --- | --- |
| `ServiceInfoStore` | Delete | Both backends manufacture generated `drs.Service` values without SQL. Server composition supplies a plain service-info value to `httpapi/drs`. |
| `ObjectStore` | Delete and split | Its 18 methods mix reads, writes, aliases, access mutation, and bulk operations. `objects` owns proposed `RecordReader`, `RecordWriter`, `AliasStore`, and query ports with `Record` values. |
| `ObjectIDResourceLister` | Move and narrow | Core uses the optional authorization fast path. `objects` owns the optional query interface and preserves its fallback. |
| `ObjectIDPageLister` | Move and narrow | Core uses both scope and resource page paths. `objects` owns it as an optional query optimization. |
| `ObjectChecksumPageLister` | Delete | No production caller uses it. Remove both adapter methods and its SQLite-only direct test after a final repository search. Do not silently wire it into behavior. |
| `ObjectURLPageLister` | Move and narrow | Core uses the URL page fast path. `objects` owns it with the current fallback and ordering contract. |
| `ObjectAuthorizedLister` | Move and narrow | Core uses its two bulk authorization paths. `objects` owns the optional capability. |
| `FileUsageScopedLister` | Move and retain | Metrics uses the optimized scoped queries and fallback aggregation. `usage` owns this optional query port. |
| `TransferAttributionScopedStore` | Move and retain | Metrics uses both scoped summary and breakdown queries. `usage` owns this optional query port. |
| `BucketVisibilityLister` | Move and retain | Bucket catalog uses it as an optimization and has an object-scan fallback. `buckets` owns the port; composition supplies the fallback without a `buckets -> objects` import. |
| `CredentialStore` | Delete and split | Catalog and bootstrap use CRUD and scopes, while storage providers only read one credential. `buckets` owns `CredentialReader`, `CredentialAdmin`, and `ScopeStore`. `storage` owns a narrow lookup port that returns bucket-owned values. |
| `ObjectsAPIServiceDatabase` | Delete | No production caller uses this composite. |
| `PendingLFSMetaStore` | Move and narrow | `transfers` owns proposed `PendingStore` with plain pending candidate values. Preserve stored JSON names, expiry, and atomic pop behavior. |
| `UsageStore` | Delete and split | Upload/download counts, transfer events, provider events, and reports serve different calls. `transfers` owns the access-event recorder port over usage-owned values. `usage` owns provider ingestion and report query ports, plus event/grant values and identity. |
| `SHA256ValidityStore` | Delete | No production caller uses this composite. The checksum endpoint consumes an object query port. |
| `MetricsStore` | Delete and split | The HTTP metrics server currently stores it and creates a second object manager. `usage` owns focused write/read ports and an object-reader port. |
| `LFSStore` | Delete | No production caller uses this composite. `httpapi/lfs` calls `transfers` and `objects` ports. |
| `DatabaseInterface` | Delete | Server keeps concrete adapter and lifecycle handles, then passes narrow ports. A server-local composition struct is allowed; a shared bootstrap interface is not. |

Proposed object ports distinguish physical and prepared data. `RecordReader.GetRecord` returns one physical `Record`; `ContentReader.GetCanonicalContent` returns a prepared same-content view. Page and maintenance methods state whether they return record IDs, records, or canonical content. This prevents maintenance from changing duplicate and report behavior by switching to raw SQL rows.

## SQL scanner, maintenance, and storage seams

| Current interface | Decision | Evidence and future owner |
| --- | --- | --- |
| `transferRows` in PostgreSQL | Retain locally | The three-method scanner seam in `postgres/transfer_reports.go:231` supports report scanning and tests. Move unchanged to `persistence/postgres`. |
| `transferRows` in SQLite | Retain locally | The same dialect-local seam in `sqlite/transfer_reports.go:240` stays in `persistence/sqlite`. Do not centralize dialect scan behavior. |
| `IndexAPI` | Delete and replace | `repair` currently consumes generated client responses. `maintenance/scoperepair` owns plain `PreparedRecordReader` and `ReferenceWriter` ports. |
| `BucketsAPI` | Delete and replace | `repair` currently consumes generated bucket responses. `maintenance/scoperepair` owns a narrow scope reader using bucket-owned values. |
| `StorageInspector` | Move and narrow | The seam is valid but URL and sentinel shaped. `maintenance/scoperepair` owns a probe port returning storage-owned result/status values. |
| `Signer` | Delete as exported contract | Only the manager and four providers need the five-method composite. `storage` keeps an unexported complete backend contract so registration cannot omit a capability. Consumers own narrower access and multipart ports. |
| `SignedURLManager` | Delete and split | Move value types to storage. `transfers` owns the signing port used by access workflows; maintenance owns any raw-operation port it needs. Objects does not call storage. Remove unused `SignUploadURL` after test doubles migrate. |
| `MultipartManager` | Delete and split | `transfers` owns a narrow multipart port; storage implements it. Preserve provider-specific upload IDs, part fields, completion, and cleanup behavior. |
| `UrlManager` | Delete | It is a convenience composite used by `ObjectManager`. Composition passes `*storage.Manager`; consumers receive their narrow ports. |
| `BucketCacheInvalidator` | Move and make private | Only the bucket catalog type-asserts it. `buckets` owns a private one-method invalidator that `storage.Manager` satisfies structurally. |

## Injection and composition

`cmd/server/server.go` constructs config, the credential cipher, one SQL adapter, bucket and object services, the storage manager and provider backends, transfer and usage services, maintenance services, authentication integrations, and HTTP adapters. Provider constructors receive a one-method credential lookup. The storage parent receives registered provider implementations but never imports their packages. Database `Close` support remains behavior/lifecycle backlog until its constructor and shutdown contracts are designed and tested.

The HTTP mount receives ready services. It does not build a second object service, as `internal/api/metrics/server.go:32-37` does today. Tests construct the same narrow ports with package-local fakes. Tests that need SQL behavior use `internal/testsupport/sqlite`; production imports of that package fail the structural gate.
