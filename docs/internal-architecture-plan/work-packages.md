# Work packages

These packages implement the target in verifiable steps. Proposed names are not current APIs. Preserve behavior unless the package names an approved contract change. Every worker hands back its commit or patch, changed-file list, exact commands and results, skipped tests, remaining old imports, and a short contract note. The Sol integrator owns shared files and accepts a handoff only after reviewing those items.

## Dependency order

```text
WP01 -> WP02
WP02 -> {WP03, WP04, WP05}
WP04 -> WP06
{WP02, WP04, WP05, WP06} -> WP07
{WP02, WP05, WP07} -> WP08
{WP02, WP04, WP07} -> WP10
{WP05, WP07, WP08} -> WP09
{WP06, WP08, WP09, WP10} -> WP11
{WP08, WP09, WP10} -> WP12
{WP03, WP10, WP11, WP12} -> WP13
{WP02..WP13} -> WP14
```

The critical path is `WP01 -> WP02 -> WP04 -> WP06 -> WP07 -> WP08 -> WP09 -> WP11 -> WP13 -> WP14`. WP10 and WP12 join before the HTTP migration. WP03 and WP05 can proceed in parallel with WP04, but their shared HTTP, common, and server files must integrate in order.

## WP01: Pin behavior and dependency checks

Purpose: make later moves measurable. Own route snapshots, object codec characterization, storage alias tests, plugin environment tests, pending-LFS row fixtures, and a small import-policy script under a repository-approved tooling location. Do not change runtime behavior.

Capture all methods and paths mounted by `cmd/server/options.go`, including root routes, POST and PUT aliases, repair, inspection, bucket, docs, LFS, and metrics. Add proposed tests `TestObjectWireCompatibility`, `TestPendingMetaLegacyJSON`, `TestAuthEnvironmentCompatibility`, and `TestMountedRouteParity` if equivalent coverage does not already exist. Record current authorization any/all behavior, same-SHA preparation, provider selection, and maintenance prepared-list behavior.

Run `go test -count=1 -timeout=120s ./cmd/server ./internal/api/... ./internal/core ./internal/db/sqlite ./internal/db/postgres ./internal/config`. Handoff includes the route inventory and import-check command. Exit when the tests pass or retain the already documented external skip. This package is prerequisite for every move.

## WP02: Consolidate access policy and session

Prerequisite: WP01.

Purpose: establish the small shared foundations, then replace the artificial `auth` and `authz` split. Move `ErrNotFound`, `ErrUnauthorized`, `ErrConflict`, and `ErrInvalidInput`, plus the `IsNotFoundError` classifier, from `internal/common/errors.go` to `internal/faults`. Move `AuthorizationError` and resource-scope policy to access. Make the `PublicError` check private to the current API error consumer until WP13 moves it to response. Migrate every sentinel caller without changing `errors.Is` behavior.

Move `internal/auth/session.go` and `internal/authz/authz.go` into proposed `internal/access/session.go` and `policy.go`. Move request ID context from `internal/common` to `internal/requestmeta`; move `AuthzContextKey` with the request metadata migration because `RequestIDKey` currently uses it. Remove the shared `HasScopedBucketAccess` wrapper after its sole internal bucket caller performs the same scope-to-resource conversion locally.

Migrate core, both SQL adapters, API packages, audit code, middleware, and direct-import tests. Preserve `CheckAccess`, wildcard matching, all-resource method checks, any-resource object checks, header/enforcement status behavior, and current session cloning. Do not normalize methods or deep-copy nested claims in this package.

Run `go test -count=1 -timeout=120s ./internal/access ./internal/api/middleware ./internal/core ./internal/db/sqlite ./internal/db/postgres`. Structural exit: `rg 'internal/(auth|authz)' --glob '*.go'` returns no imports, and `go list -deps ./internal/access` contains no Fiber, API, core, database, object, or bucket package. Delete both old directories in this package. Handoff maps each moved policy and context symbol to its new file.

## WP03: Separate authentication mechanisms from Fiber

Prerequisites: WP01 and WP02.

Purpose: keep `internal/httpapi/middleware` as request wiring. Move local/Gen3 authentication, CSV parsing, token/JWKS code, and authn/authz subprocess adapters to `internal/access/authentication`. Use the public `plugin.AuthenticationPlugin`, `plugin.AuthorizationPlugin`, and `plugin.Handshake` contracts; remove the duplicate middleware interfaces and handshake type. Proposed `Authenticator` and `Authorizer` ports may be private to the middleware consumer.

Migrate `cmd/server/server.go`, `cmd/server/options.go`, plugin integration tests, middleware tests, and documentation references. Preserve environment names, plugin keys, RPC method names, cookie/protocol values, public metadata bypass, request-ID order, and current status results. Keep startup error swallowing, nil-output behavior, and process lifetime unchanged.

Run `go test -count=1 -timeout=120s ./internal/access/... ./internal/httpapi/middleware ./cmd/server`. Exit when middleware has only Fiber orchestration and request context installation, while authentication code has no Fiber imports. Handoff identifies every preserved environment variable.

## WP04: Introduce object records and canonical content

Prerequisites: WP01 and WP02.

Purpose: replace `models.InternalObject` with values that state identity and query semantics. Add proposed `objects.RecordID`, `ContentID`, `Checksum`, `AccessMethod`, `AccessURL`, `Record`, and `CanonicalContent`. `Record` keeps nested contents and opaque extension metadata. Move checksum, object/access identity, name normalization, and alias functions from `internal/common` into `objects` files, not one-function subpackages.

Move generated mapping and custom JSON behavior to `internal/httpapi/drs` and `internal/httpapi/records`. Preserve `id`/`did`, `checksums`/`hashes`, retired-field omission, controlled-access derivation, aliases, and unknown JSON properties. Keep physical `RecordID` distinct from checksum-derived `ContentID`. Name raw and prepared return types explicitly. Maintenance listing continues to consume the prepared/canonical form where it does today.

This package migrates every object-value caller, including `internal/db/interface.go`, both backends' object hydration and mutation files, core object code, DRS and record adapters, LFS candidates, attribution, metrics, repair adapters, `cmd/upload`, and tests. Preserve SQL column types and stored TEXT/JSON encodings. Land values and HTTP codecs first, then change old-path database signatures/hydration, then migrate remaining callers. Delete `models/object.go` and `object_codec.go` in this package. Do not keep aliases or an unused parallel type system.

Localize pointer/value helpers as their consumers migrate; new object code must not depend on the remaining `internal/common` package. Run `go test -count=1 -timeout=120s ./internal/objects ./internal/models ./internal/core ./internal/db/sqlite ./internal/db/postgres ./internal/api/drsapi ./internal/api/internaldrs ./internal/api/lfs ./internal/api/metrics ./internal/httpapi/drs ./internal/httpapi/records`. Exit when object values have no generated, Fiber, SQL, cloud SDK, buckets, storage, `internal/common`, or `internal/models` imports and no production reference to `InternalObject` remains. Handoff documents exact field, SQL, and wire mappings and any unsupported bundle behavior without changing it.

## WP05: Establish bucket values and the address leaf

Prerequisites: WP01 and WP02.

Purpose: give credentials, scopes, visibility, and provider address syntax stable owners. Move `models.S3Credential`, `BucketScope`, and `BucketVisibilityRow` to proposed `buckets.Credential`, `Scope`, and `VisibilityRow`. Move `DeriveCredentialID` and credential audit to buckets. Move provider constants, scheme aliases, bucket/key parsing, `NormalizeStoragePath`, and legacy S3 URL parsing to `storage/address`.

Migrate config validation, server bootstrap, signer/provider inputs, both backends, bucket handlers, and tests. Preserve `s3`, `gs`/`gcs`, `az`/`azblob`, and `file` strings; custom endpoint validation; credential ID derivation; secret exclusion; audit fields; and current permissive legacy parsing at compatibility boundaries. `buckets` may import only the address leaf, access, faults, and repository value contracts. It does not import storage parent or objects.

Run `go test -count=1 -timeout=120s ./internal/buckets ./internal/storage/address ./internal/config ./internal/db/sqlite ./internal/db/postgres`. Exit when provider/address helpers have left common and address imports only the standard library. Handoff includes an alias matrix and exact credential field mapping.

## WP06: Establish usage values and client DTOs

Prerequisites: WP01 and WP04.

Purpose: prevent a transfers-to-usage type cycle. Add proposed usage-owned `Event`, `Grant`, `ProviderEvent`, file usage, filters, summaries, freshness, and breakdown values. Move pure event/grant identity into usage. Transfers later assembles the access projection and calls its own narrow recorder port over usage values; usage never imports transfers.

Move server-side values from `internal/models/transfer_metrics.go` without changing fields. Add client-owned public transfer result DTOs in `client/services` with the same JSON fields and explicit generated-client mapping. Migrate `cmd/metrics`. This is a named Go API change, even though HTTP JSON stays stable. Do not add a permanent alias to internal models.

Run `go test -count=1 -timeout=120s ./internal/usage ./internal/api/metrics ./client/services ./cmd/metrics`, then `GOWORK=off go test ./...` from `client` against release-intended dependencies when they are available. Exit when client and `cmd/metrics` have no `internal/models` imports. Handoff includes the named-type compatibility note and proposed client/root release order. Do not publish a module.

## WP07: Move SQL adapters and replace database aggregates

Prerequisites: WP02, WP04, WP05, and WP06.

Purpose: relocate the now object-decoupled adapters, remove the remaining database aggregates, and remove generated pending/service types from persistence. Move dialect code unchanged to `internal/persistence/sqlite` and `internal/persistence/postgres`. Define remaining object ports in `objects`, bucket ports in `buckets`, transfer pending ports in `transfers`, and accounting/query ports in `usage`. Let adapters import those packages for owned values, but never HTTP code or constructed services.

Move `internal/crypto` to `internal/credentialcipher` in this package because both persistence adapters are its production consumers. Move `CredentialKeyManager`, envelope versions, key managers, and credential field transforms. Update server and adapter imports. Preserve plaintext, V1, V2, purpose string, audit, and current per-operation environment/key-manager selection. Do not introduce an injected startup snapshot in this structural package. Run the existing crypto tests at their destination and both adapters' credential suites.

Delete `ServiceInfoStore`, `ObjectsAPIServiceDatabase`, `SHA256ValidityStore`, `LFSStore`, `MetricsStore`, and `DatabaseInterface`. Delete the unused checksum-page interface and methods after a final call search. Split `ObjectStore`, `CredentialStore`, and `UsageStore` into consumer ports. Preserve the production-used optional object, visibility, file-usage, and transfer-query optimizations and their fallbacks. Replace pending generated candidates with plain transfer values and an old-row-compatible JSON codec. Static service info moves to server/DRS composition.

Caller families are `core`, metrics, URL/signers, server bootstrap, both backends, and test wrappers. Run `go test -count=1 -timeout=120s ./internal/persistence/... ./internal/credentialcipher ./internal/objects ./internal/buckets ./internal/usage ./cmd/server`. Exit when `rg 'internal/db|apigen/server/drs' internal/persistence --glob '*.go'` returns no imports. Live PostgreSQL remains an explicit gate when `SYFON_TEST_POSTGRES_DSN` is set. Handoff lists every consumer port implemented by each adapter.

## WP08: Build the bucket catalog service

Prerequisites: WP02, WP05, and WP07.

Purpose: move credential/scope CRUD, normalization, visibility, cache, and invalidation from `core/bucket_catalog.go` and `bucket_scope_cache.go` to `internal/buckets`. Proposed ports are `CredentialReader`, `CredentialAdmin`, `ScopeStore`, `VisibilityQuery`, and a private `cacheInvalidator`.

The visibility fast path stays optional. Composition supplies the object-scan fallback through a callback or adapter, so buckets does not import objects. Storage can consume bucket-owned values through its own lookup port, but buckets never imports storage parent. Preserve cache hit/miss behavior, invalidation after credential mutation, scope selection, authorization, audit, and config-loaded credential/scope ordering.

Migrate internal bucket routes, server config loading, storage credential adapter, both SQL adapters, and tests. Run `go test -count=1 -timeout=120s ./internal/buckets ./internal/api/internaldrs ./cmd/server ./internal/persistence/...`. Exit when core has no bucket catalog files, the forbidden imports are absent, and both optimized and fallback visibility tests pass. Handoff names the composition adapter for object fallback.

## WP09: Move storage facade and provider I/O

Prerequisites: WP05, WP07, and WP08.

Purpose: combine provider-neutral signing, multipart, probe, inventory, and exact-delete dispatch under `internal/storage`; move SDK implementations to `storage/{s3,gcs,azure,file}`. Move signing options, multipart parts, sorting, download filename/content-disposition functions, and the manager from `signer`, `common`, and `urlmanager`. Keep a private complete backend interface for registration. `storage.CredentialLookup` returns bucket-owned values.

Move current core SDK-level delete, head, list, limiter, retry, and cache mechanisms to storage and provider children. Keep project-level object/visibility comparison out of storage. Proposed raw capabilities are `Access`, `Multipart`, `Prober`, `Inventory`, and `Deleter`. Providers keep their current client factories and caches. The parent imports no child; server registers all four.

Preserve provider resolution and error fallback, URL aliases, default methods, expiry behavior, S3 remote multipart IDs/ETags, current part ordering, GCS endpoint/native modes, Azure block IDs, file raw paths/root behavior, exact stored-URL deletion, and S3-only inventory. Run `go test -count=1 -timeout=120s ./internal/storage/... ./internal/buckets ./cmd/server`. Exit when old signer/urlmanager imports are zero and no storage package imports objects, HTTP, or core. Imports of bucket-owned values are allowed, but dependencies on the concrete bucket service are not. Handoff lists provider capability differences rather than hiding them behind uniform claims.

## WP10: Build the object service and remove object-manager calls

Prerequisites: WP02, WP04, and WP07.

Purpose: move object registration, aliases, reads, authorization, canonicalization, bulk overwrite, and access mutation from core to `objects.Service`. The constructor receives separate reader, writer, alias, and optional query ports. It does not receive a database aggregate, bucket catalog, storage manager, usage service, or generated value.

Migrate DRS/record handlers, transfer and usage object readers, maintenance readers/writers, LFS registration, `cmd/upload`, and tests. Preserve deterministic record IDs, checksum validation, same-SHA merge, replica URLs, public-read state, metadata tie-breaking, pagination, authorization filtering, transaction boundaries, and bulk overwrite. Keep storage target and accounting orchestration outside objects.

Run `go test -count=1 -timeout=120s ./internal/objects ./internal/api/drsapi ./internal/api/internaldrs ./internal/persistence/...`. Structural exit: migrated DRS and record route responsibilities no longer accept `*core.ObjectManager`; object package dependencies meet the graph; raw `Record` and prepared `CanonicalContent` tests prove distinct semantics. Transfer and maintenance routes may retain the facade until WP11 and WP12. Handoff maps every former object receiver method to its owner.

## WP11: Split transfer workflow, LFS, and usage services

Prerequisites: WP06, WP08, WP09, and WP10.

Purpose: move signed access/target selection, multipart, pending LFS staging, and attribution projection into `internal/transfers`. Move accounting ingestion and report queries into `internal/usage`. `transfers` constructs a usage-owned event and calls its own narrow recorder port. Centralize event/grant identity in usage so usage does not import transfers.

Replace `RecordAccessIssued(*core.ObjectManager, generated object, ...)` with proposed plain `transfers.AccessRequest` plus a transfers-owned `EventRecorder` that accepts `usage.Event`. Move DRS, internal transfer, and LFS callers; update both adapters' grant hash call sites. Keep SQL transactions and provider reconciliation in persistence. Split `internal/api/lfs`: `httpapi/lfs` keeps generated strict responses, media types, middleware, base URL context, and provider PUT adapter; transfers owns workflow and pending metadata. `httpapi/metrics` keeps generated conversion while usage owns aggregation and scoped optimization ports.

Move `WithBaseURL` and `GetBaseURL` from `internal/core/service.go` into the LFS adapter. Preserve issuance paths, scope selection, event fields, idempotence, provider reconciliation, pending JSON, pop timing, default bucket choice, process-global LFS limiter behavior, status codes, and report freshness output. Run `go test -count=1 -timeout=120s ./internal/transfers ./internal/usage ./internal/api/drsapi ./internal/api/internaldrs ./internal/httpapi/lfs ./internal/httpapi/metrics ./internal/persistence/...`. Exit when attribution and metrics no longer construct or accept ObjectManager and generated types stay in HTTP adapters. Handoff lists each issuance path, event field mapping, pending-row codec, and usage query port.

## WP12: Move project maintenance workflows

Prerequisites: WP08, WP09, and WP10.

Purpose: distinguish provider I/O from cross-domain maintenance. Move `internal/repair` to `internal/maintenance/scoperepair`. Move project storage inspection, object-versus-inventory comparison, cleanup orchestration, and `BulkDeleteObjectsWithStorage` from core/API glue to `internal/maintenance/projectstorage`. Both packages consume object, bucket, and storage ports; none of those parents imports maintenance.

Replace `repair.IndexAPI` and `BucketsAPI` generated contracts with proposed plain readers/writers. Replace `StorageInspector` with a maintenance-owned port returning storage probe results. Keep HTTP mapping in `httpapi/maintenance`. Preserve prepared/canonical listing, duplicate collapse, current audit/apply ordering, report counts, skipped update handling, S3-only scope repair, probe statuses, deletion order, retry/terminal replay, and exact physical URLs. These include known oddities but are not silently fixed here.

Run `go test -count=1 -timeout=120s ./internal/maintenance/... ./internal/api/internaldrs ./internal/storage ./internal/objects ./internal/buckets`. Exit when maintenance has no generated imports and core owns no inspection/cleanup orchestration. Handoff maps each former repair/core operation to either provider I/O or project orchestration.

## WP13: Split HTTP ownership and retire route helpers

Prerequisites: WP03, WP10, WP11, and WP12.

Purpose: replace `internal/api` with the target HTTP tree. Move DRS, records, bucket, transfer, maintenance, LFS, metrics, docs, middleware, and response adapters to their named packages. Keep a tiny `httpapi.RegisterRoutes` owner for order and shared mounting. Bucket mounting is no longer a side effect of transfer mounting.

Delete `routeutil`: use Fiber `:param` paths and direct handlers, and remove the unread test path-parameter context. Move route constants from common/config into their route owners. Preserve manual DRS registration, explicit aliases, unsupported-operation responses, generated LFS and metrics strict handling, exact route order, status/header/body behavior, and docs embedded-first filesystem fallback. Do not adopt generated DRS/internal handlers in this package.

Update `cmd/server/options.go`, server integration, CLI E2E, test route helpers, and all handler imports. Run `go test -count=1 -timeout=120s ./internal/httpapi/... ./cmd/server` plus the route parity test from WP01. Exit when `internal/api` and routeutil imports are zero, the full baseline runtime method/path/alias inventory is unchanged, and the existing documented-to-mounted check still passes. Private runtime routes do not have to appear in the public specification. Handoff includes the final method/path table.

## WP14: Delete facades and test monolith, then integrate

Prerequisites: WP02 through WP13.

Purpose: remove migration residue. Replace `internal/testutils.MockDatabase` and `MockUrlManager` with package-local fakes that implement consumer ports. Move only the real database fixture to `internal/testsupport/sqlite`. Migrate provider tests first, then objects/buckets/storage/usage/transfers, HTTP packages, and command integration tests. Do not recreate a broad mock builder.

Localize remaining `Ptr`, `TimeVal`, `StringVal`, `Int64Val`, and dereference helpers at API or persistence boundaries; domain consumers must already have stopped importing them during their own migrations. Preserve `DerefStringSlice` copy and nil behavior. Verify that WP02 moved shared sentinels to `faults` and request ID plus its context key to `requestmeta`, then delete residual `internal/common` files. Delete any remaining retired core, models, db, API, auth/authz, crypto, repair, signer, URL manager, and testutils paths after all callers migrate. The public root `common` package and `internal/version` stay unchanged.

The Sol integrator alone edits `cmd/server`, shared module files, the dependency checker, and final deletion commits. Run focused direct-importer tests, then once run `go test -count=1 -timeout=120s ./... ./client/... ./apigen/...`. Run `GOWORK=off` checks for affected modules and PostgreSQL, provider, and KMS tests in configured disposable test environments. Report unavailable gates. Exit only when `rg` finds zero production imports of retired packages, `go list ./...` is acyclic, domain packages have no forbidden dependencies, no production graph reaches testsupport, and route/backend behavior gates pass. Handoff records external skips and release order; it does not publish or deploy.

## Parallel execution schedule

Use isolated branches or worktrees for concurrent workers. Never let two workers edit the same checkout files.

| Wave | Parallel assignments | Integration owner and shared files |
| --- | --- | --- |
| 0 | WP01 only | Sol accepts characterization before runtime moves. |
| 1 | WP02 foundations and access | One worker moves faults, request metadata, session, policy, and every shared sentinel caller. |
| 2 | WP03 authentication, WP04 object values, and WP05 bucket/address values | Workers use isolated branches. Sol serializes shared API, common, model, and server edits. |
| 3 | WP06 usage values and client DTOs | One worker completes the value layer needed by persistence. |
| 4 | WP07 persistence and credentialcipher | One worker owns both adapter moves and consumer-port integration. Sol owns `cmd/server` changes. |
| 5 | WP08 buckets and WP10 objects | Disjoint domain trees. Sol integrates shared persistence adapter calls. |
| 6 | WP09 storage | Provider children may use four disjoint workers. One storage worker owns the parent contracts; Sol owns server registration. |
| 7 | WP11 transfers/usage and WP12 maintenance | Disjoint owners, but Sol serializes object, bucket, storage port changes and persistence event files. |
| 8 | WP13 HTTP | One worker owns all HTTP moves to avoid route-file conflicts. Sol owns `cmd/server/options.go`. |
| 9 | WP14 cleanup and full verification | One cleanup worker may migrate disjoint test families, but Sol alone deletes old packages and edits shared module/composition files. |

## Behavior-change backlog

Do not use these issues to make a structural package pass. Triage each as a separate change with a product decision and a failing regression test:

| Issue | Evidence | Discriminating test |
| --- | --- | --- |
| Attribution chooses the first scope instead of the matched access method | `internal/api/attribution/attribution.go:190-205` | Two access methods with different scopes select the issued method's scope. |
| Plugin construction errors are ignored; Gen3 accepts weak built-in truth | `internal/api/middleware/middleware.go:64-87`, `authn_builtin.go:38-55` | Configured startup failure and invalid token policy are decided explicitly. |
| Session claims clone shallowly; plugin nil/lifecycle behavior is undefined | `internal/auth/session.go:112-120`, `internal/api/middleware/plugin_clients.go` | Nested mutation isolation, nil output, deadline, and shutdown cases. |
| Signing expiry, method coercion, ranges, credential-error fallback, S3 ordering, GCS close, and file roots differ | `internal/urlmanager/manager.go`, `internal/signer/*` | Provider matrix over non-default expiry, range, ordering, error kind, and local containment. |
| Metrics freshness is a placeholder | `internal/api/metrics/transfer_reports.go:175-184` | Known watermark and missing bucket produce explicit stale output. |
| Repair count, audit/apply snapshot, and skipped writes are misleading | `internal/repair/types.go:104-120`, `service.go:27-51` | Multiple findings, duplicate collapse, and write failure preserve a chosen report contract. |
| PostgreSQL pending LFS not-found differs from SQLite | `internal/db/postgres/lfs.go:60-73,101-123` | Both adapters map absent and expired rows to the same domain error. |
| Config and credential cipher use ambient process state | `internal/config/config_auth_environment.go`, `internal/crypto/credential_envelope.go` | Successive configs, plugin child inheritance, two simultaneous key sets, and legacy decrypt. Preserve dynamic per-operation key selection until this decision lands. |
| Database close and schema migration lifecycle are implicit | backend constructors and schema files | Constructor failure cleanup, shutdown close, idempotent upgrades, and live PostgreSQL migration. |
| LFS TTL, pop-before-register, global limits, and abort behavior need contracts | `internal/api/lfs/handlers.go:93-295`, `middleware.go:63-105` | Failure-then-retry, TTL, per-server isolation, and multipart abort capability. |

Final structural gates include zero retired imports, no generated/Fiber/SQL/cloud SDK imports in domain packages, no test helpers in production dependency graphs, an acyclic allowed graph, exact route parity, object identity and canonicalization parity, both SQL dialect suites, provider-specific contract tests, and isolated module checks. Syntax or compilation alone does not satisfy a package.
