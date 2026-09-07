# Chosen internal architecture

## What changes

The main problem is ownership, not file size. `internal/core/service.go` gives `ObjectManager` a full database and URL manager, then receiver files add object lifecycle, bucket catalog, storage I/O, LFS state, service metadata, and accounting across 116 declared methods. `internal/db/interface.go` repeats that coupling in 18 interfaces. `internal/common` and `internal/models` let unrelated callers share representations instead of contracts. The target gives each behavior one owner and makes composition explicit in `cmd/server`.

The result is simpler to read. A record lookup starts in `httpapi/records`, enters `objects.Service`, and reaches one object repository port. A signed transfer starts in `httpapi/transfers`, enters `transfers.Service`, and calls bucket lookup, storage access, and usage recording through named ports. SQL hydration stays in one dialect adapter. Provider SDK calls stay in one provider child. No reader must inspect `ObjectManager` to discover which subsystem owns a call.

## Priority findings

1. `ObjectManager` is a facade over several domains. Renaming it would keep the broad constructor in `internal/core/service.go` and the route-wide dependency. Split callers and delete it.
2. A persisted physical record and a merged same-checksum response are different values. `internal/models/object.go` embeds a generated DRS value, while `internal/models/object_codec.go` gives it wire behavior. Introduce a stable `RecordID`, a checksum-derived `ContentID`, a physical `Record`, and a prepared `CanonicalContent` result.
3. HTTP-generated, Fiber, SQL, and cloud SDK types have crossed their owners. The generated signatures in `internal/db/interface.go` make the leak explicit. Generated types belong in HTTP adapters, SQL rows in persistence adapters, and SDK clients in provider packages.
4. The interface-only database parent makes every consumer understand unrelated capabilities. `internal/api/metrics/server.go` demonstrates the cost by constructing a second object manager from the aggregate. Concrete backends may implement many methods, but services receive only consumer-owned ports.
5. Bucket credential state has one owner. `buckets` owns values, CRUD, scope cache, visibility, and invalidation policy. `storage` may import bucket value types and declare a narrow lookup port. `buckets` never imports the storage parent or objects.
6. Structural movement must preserve surprising behavior. `internal/api/attribution/attribution.go` contains current scope selection, and `internal/repair/types.go` plus `internal/api/internaldrs/repair_cleanup.go` expose current repair/report behavior. Plugin failure policy, signer expiry and fallback, range handling, multipart ordering, cache closing, repair counts, and attribution selection remain separate decisions.

## Target package tree

```text
internal/
  access/                       session.go, policy.go
    authentication/            local, Gen3, JWT/JWKS, authn/authz process adapters
  buckets/                      credentials, scopes, cache, visibility, audit
  credentialcipher/             credential envelope and key-manager mechanisms
  faults/                        stable cross-layer sentinels only
  httpapi/
    mount.go
    apidocs/ drs/ records/ buckets/ transfers/ maintenance/
    lfs/ metrics/ middleware/ response/
  maintenance/
    scoperepair/                scope/reference audit and repair orchestration
    projectstorage/             project inspection, inventory comparison, cleanup
  objects/                      records, identities, aliases, canonical views, service
  persistence/
    sqlite/ postgres/           dialect SQL, hydration, transactions, schema setup
  requestmeta/                  request ID context value only
  storage/
    address/                    provider, scheme, bucket/key parsing leaf
    s3/ gcs/ azure/ file/       SDK calls and provider caches
    access.go multipart.go inspect.go inventory.go delete.go
  transfers/                    target/access issuance, multipart, pending LFS
  usage/                        event/grant identity, accounting, queries, reports
  testsupport/sqlite/           real reusable SQLite fixture, tests only
  config/ version/              retained focused packages
```

`internal/httpapi/mount.go` only orders route registration. It owns no business rules. `internal/httpapi/lfs` keeps the request-local base URL because no other production flow uses it. `internal/storage` owns provider-neutral I/O dispatch and a private complete backend registration contract. Its parent does not import provider children. `internal/maintenance/projectstorage` combines object records, bucket visibility, and raw storage operations for project inspection, inventory comparison, `BulkDeleteObjectsWithStorage`, and cleanup. `internal/transfers` coordinates signed access and multipart operations but does not absorb accounting reports. `internal/usage` owns event/grant values, pure identity functions, accounting, and report use cases because metrics, LFS, DRS, and provider ingestion share them.

## Allowed dependency graph

```text
cmd/server
  -> config, credentialcipher, persistence adapters, storage providers
  -> httpapi mount and constructed domain services

httpapi/* -> access, objects, buckets, transfers, usage, maintenance, generated API
maintenance/{scoperepair,projectstorage} -> objects, buckets, storage
transfers -> objects, buckets, storage and usage values through a transfers-owned recorder port
usage -> access and consumer-owned object/query ports
objects -> access, faults, requestmeta and object repository ports
storage -> storage/address, buckets value types, faults and lookup ports
buckets -> access, storage/address, faults and bucket repository ports
persistence/{sqlite,postgres} -> domain value packages, access, credentialcipher
storage/{s3,gcs,azure,file} -> storage, storage/address, buckets values, provider SDK
```

Forbidden edges are part of the design:

- No domain package imports Fiber, generated API packages, SQL drivers, or cloud SDKs.
- No persistence package imports generated API types, HTTP packages, or constructed domain services. It may import domain packages for their owned value types.
- No `buckets -> storage` parent or `buckets -> objects` edge. Composition supplies visibility fallbacks and cache invalidation.
- No `storage -> buckets.Catalog`, `storage -> objects`, or storage-parent-to-provider-child edge.
- No parent package imports either maintenance child; no `objects -> maintenance` edge.
- No production package imports `internal/testsupport`.
- No replacement `core`, `common`, `models`, `db`, generic repository, or central interface package.

## Object and wire contracts

The proposed object values are `type RecordID string`, `type ContentID string`, `type Record struct`, and `type CanonicalContent struct`. `RecordID` identifies one persisted physical record and remains stable across canonical preparation. `ContentID` identifies checksum content. `CanonicalContent` names the merged result returned by checksum-aware reads. Repository methods state which form they return, so a raw-record maintenance query cannot accidentally replace today's prepared listing semantics.

`Record` retains access methods, checksums, aliases, controlled-access state, nested contents, and opaque extension metadata. Proposed extension storage is `map[string]json.RawMessage`, but the type has no `MarshalJSON` or generated embedding. HTTP codecs preserve current `id` and `did`, `checksums` and legacy `hashes`, retired-field omission, alias normalization, and unknown-property round trips. Persistence must preserve existing stored fields and must not claim that transient unknown properties survive a database reload when they do not today.

## Common and model destinations

Every production file in the two catch-all packages has an assigned owner. Migrations delete the source file in the same package that moves its last caller. They do not leave aliases or duplicate domain types.

| Current file | Destination | Work package |
| --- | --- | --- |
| `common/checksum.go`, object portions of `common/identity.go`, `common/util.go` | Object checksums, record/content identity, and name normalization in `objects` | WP04 |
| Credential portion of `common/identity.go`, `common/audit.go` | Credential identity and access audit in `buckets` | WP05 |
| `common/provider.go`, `common/storage_url.go` | Provider/scheme/bucket-key values and parsing in `storage/address` | WP05 |
| `common/download_name.go` | Provider-safe filename and content-disposition functions in `storage` | WP09 |
| `common/errors.go`, `common/resource_scope.go` | Four stable sentinels in `faults`; authorization error and scope policy in `access`; private presentation interface in HTTP response | WP02, then WP13 |
| `common/request_id.go` | Request ID context in `requestmeta`; header behavior stays in HTTP middleware | WP02 |
| `common/context.go` | Context key type used by request ID in `requestmeta` | WP02 |
| `common/routes.go` | Constants beside each `httpapi` route owner | WP13 |
| `common/ptr.go` | Local boundary helpers as each consumer migrates; preserve `DerefStringSlice` copy and nil semantics | WP04/WP10 and other consumer moves; final residue in WP14 |
| `models/object.go`, `models/object_codec.go` | Plain record and canonical values in `objects`; JSON codec in DRS/record HTTP adapters | WP04 |
| `models/bucket_credentials.go` | Credential and scope values in `buckets` | WP05 |
| `models/storage.go` | Pending LFS values in `transfers`; visibility values in `buckets`; SQL-only row projections private to each adapter | WP05, WP07 |
| `models/transfer_metrics.go` | Event/grant and report values in `usage`; public result DTOs in `client/services` | WP06 |

The LFS-only `WithBaseURL` and `GetBaseURL` helpers are currently in `internal/core/service.go`, not `internal/common/context.go`. WP11 moves them into `httpapi/lfs`. This inventory concerns `internal/common`; the separate public root `common` package and its compatibility contracts remain in place.

## Complete current-package disposition

| Current package | Decision | Destination or reason |
| --- | --- | --- |
| `internal/api/apiutil` | Move | `internal/httpapi/response`; keep current mapping, separate from LFS errors. |
| `internal/api/attribution` | Move | Access projection to `internal/transfers`; event/grant values, identity, and accounting writes belong to `usage`. |
| `internal/api/docs` | Move | `internal/httpapi/apidocs`; preserve embedded-first and filesystem fallback behavior. |
| `internal/api/drsapi` | Move | `internal/httpapi/drs`; retain manual registration and aliases. |
| `internal/api/internaldrs` | Split/delete | `httpapi/records`, `transfers`, `buckets`, and `maintenance`, mounted explicitly. |
| `internal/api/lfs` | Split/delete | Protocol adapter to `httpapi/lfs`; workflow and pending state to `transfers`. |
| `internal/api/metrics` | Split/delete | Generated adapter to `httpapi/metrics`; use cases to `usage`. |
| `internal/api/middleware` | Split/delete | Fiber wiring to `httpapi/middleware`; mechanisms to `access/authentication`. |
| `internal/api/routeutil` | Delete | Use direct Fiber routes and handlers after parity characterization. |
| `internal/auth` | Merge/delete | `internal/access/session.go`. |
| `internal/authz` | Merge/delete | `internal/access/policy.go`; caller-local bucket conversion. |
| `internal/common` | Split/delete | `objects`, `buckets`, `storage/address`, `access`, `faults`, `requestmeta`, and HTTP owners. |
| `internal/config` | Keep/refocus | Pure resolution plus a characterized compatibility bridge for process and plugin environment. |
| `internal/core` | Split/delete | `objects`, `buckets`, provider I/O in `storage`, project inspection/cleanup in `maintenance/projectstorage`, `transfers`, `usage`, and HTTP converters. |
| `internal/crypto` | Move/delete | `internal/credentialcipher`; preserve dynamic per-operation key selection first. |
| `internal/db` | Delete | Consumer-owned ports; no interface-only replacement parent. |
| `internal/db/postgres` | Move | `internal/persistence/postgres`; retain dialect SQL and transactions. |
| `internal/db/sqlite` | Move | `internal/persistence/sqlite`; retain single-connection and upgrade behavior. |
| `internal/models` | Split/delete | Owned domain values and adapter-only generated conversions. |
| `internal/repair` | Move/delete | `internal/maintenance/scoperepair`; preserve current prepared listing semantics. |
| `internal/signer` | Move/delete | Storage values/helpers and a private full provider contract. |
| `internal/signer/azure` | Move | `internal/storage/azure`. |
| `internal/signer/file` | Move | `internal/storage/file`; keep raw-path behavior during the move. |
| `internal/signer/gcs` | Move | `internal/storage/gcs`. |
| `internal/signer/s3` | Move | `internal/storage/s3`. |
| `internal/testutils` | Delete | `internal/testsupport/sqlite` plus test-local fakes. |
| `internal/urlmanager` | Move/delete | Provider-neutral `internal/storage` facade and consumer-owned ports. |
| `internal/version` | Keep | Cohesive linker-owned build metadata; moving it would break `-X` paths. |

This plan rejects several tempting splits. `objects/names` and `objects/identity` do not become packages unless a future cycle proves the need. A one-function namespace adds navigation without ownership. The storage address leaf is justified because both buckets and storage need provider/address values while buckets cannot import the storage parent.
