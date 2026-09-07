# Syfon architecture and reorganization

This page describes the current Syfon layout at the 2026-09-04 audit baseline. It replaces the old directory-move proposal that this file used to contain. The dependency map and accepted worklist below are self-contained.

## Current dependency map

The server is built from these ownership boundaries:

| Responsibility | Current owner | Dependency direction |
| --- | --- | --- |
| Process and server construction | `main.go`, `cmd/root.go`, `cmd/server/server.go`, `cmd/server/options.go` | Loads `internal/config`, creates a database, creates `internal/core.ObjectManager`, and mounts the route packages. |
| Generated protocol types | `apigen/server/*` and `apigen/client/*` | Separate `github.com/calypr/syfon/apigen` module. Server handlers and client services use the generated bindings. |
| DRS, internal, LFS, and metrics protocols | `internal/api/drsapi`, `internal/api/internaldrs`, `internal/api/lfs`, `internal/api/metrics` | Registers HTTP routes and translates requests and responses. The handlers call core services and the database contracts. |
| Object and storage workflows | `internal/core` | `ObjectManager` coordinates object identity, policy, bucket scope, storage inspection, deletion, repair support, and usage recording. It depends on `internal/db`, `internal/models`, `internal/common`, and `internal/urlmanager`. |
| Persistence contracts | `internal/db/interface.go` | Defines capability interfaces such as `ObjectStore`, `CredentialStore`, `UsageStore`, and `DatabaseInterface`. It uses `internal/models` and generated DRS types. |
| Persistence implementations | `internal/db/sqlite`, `internal/db/postgres` | Implements the database contracts with SQLite and PostgreSQL SQL and transaction behavior kept separate. |
| Provider URL signing | `internal/urlmanager`, `internal/signer/s3`, `internal/signer/gcs`, `internal/signer/azure`, `internal/signer/file` | `urlmanager` dispatches by provider and the signer packages resolve credentials and create provider clients. Core inspection and deletion also contain provider SDK operations. |
| Authentication and authorization | `internal/httpapi/middleware`, `internal/access`, `internal/access/authentication` | Fiber middleware carries request identity and access context. Access policy and authentication mechanisms stay independent of Fiber. Core and API code supply object policy data. |
| Repair | `internal/repair`, `internal/api/internaldrs/repair_cleanup.go` | Repair coordinates storage checks and cleanup. Its current in-process adapter calls core through a transport-shaped request. Task 3 replaces that adapter with a direct interface while keeping `/data/inspect`. |
| Public client and transfers | `client/*` | Separate `github.com/calypr/syfon/client` module for client services, transfer engines, and provider backends. The root module and published client module paths stay unchanged. |

The normal server path is:

1. `main.go` executes `cmd.RootCmd`.
2. `cmd/server/server.go` loads configuration, creates either `sqlite.NewSqliteDB` or `postgres.NewPostgresDB`, creates `urlmanager.Manager`, and creates `core.ObjectManager`.
3. `cmd/server/options.go` mounts the DRS, internal, LFS, metrics, and documentation routes that configuration enables.
4. A route handler calls `ObjectManager` or a narrower database capability.
5. Core resolves object identity and access policy, then reads or writes through the database contract. URL signing goes through `urlmanager`. Storage inspection and deletion use their existing provider-specific core paths.

Keep the DRS, internal API, LFS, and metrics protocol boundaries. Keep both database backends, provider-specific implementations, authentication and session separation, repair, URL management, and the public `client` and `apigen` module paths. Generated bindings remain under `apigen`; they do not move into `internal`.

The audit found no basis for deleting or merging a whole tracked package.

## Accepted implementation worklist

The audit accepted the following work in order of priority. These items describe bounded ownership and file splits. They do not authorize a whole-tree move or a new generic repository layer. The source paths and compatibility requirements below are the tracked reference for each item.

1. Move the database test helper out of `internal/db` and into test utilities. Keep database contracts free of backend imports and add compile-time backend assertions.
2. Narrow database dependencies at startup and in metrics. Use `db.CredentialStore` for bucket-scope loading and pass metrics its actual store and object reader requirements.
3. Replace repair's in-process HTTP imitation with a consumer-owned `StorageInspector` interface. Preserve `/data/inspect` and current missing-object and error mapping.
4. Give bucket credentials and scope caching one owner inside `internal/core`. Preserve positive and negative cache entries, invalidation, and visibility fallback behavior.
5. Split storage policy from provider SDK mechanics. First split inventory files inside `internal/core`, then add operation-specific provider ports only where existing seams prove useful.
6. Split SQLite and PostgreSQL implementations by operation. Characterize both backends before changing object identity, aliases, authorization, LFS, metrics, pagination, or transaction behavior.
7. Make internal route ownership explicit and separate metrics queries from HTTP adapters. Preserve method and path pairs, registration order, pagination, visibility, and fallback behavior.
8. Expose effective configuration and authentication inputs without changing precedence or fallback decisions. Keep environment export and plugin behavior intact until characterized.
9. Group `internal/common`, `internal/models`, and `internal/crypto` by cohesion inside their current packages. Preserve public `common`, sentinel errors, model codecs, and encryption compatibility.
10. Remove inert private state from `client/services` and split its mixed implementation inside the existing package. Keep public client types, signatures, transfer checkpoints, and retry behavior.
11. Split command and copy-project state into focused files. Keep exported command compatibility values, aliases, output, authentication behavior, and copy result semantics.
12. Keep this architecture reference current, document database schema initialization, and anchor the root `syfon` binary ignore rule to `/syfon`.

Each work item needs focused package and direct-importer checks. Run the full three-module suite after the integrated wave. Preserve HTTP behavior, authorization, canonical identity, database transactions, provider requests, retry and cancellation behavior, environment precedence, CLI output, and published imports.

## Historical proposal (superseded)

The earlier proposal came from the 2026-04-15 Fiber and OpenAPI migration period. It suggested moving `service/` to `internal/service/`, moving `db/` and `urlmanager/` below `internal/infra/`, consolidating handlers below `internal/handler/`, moving `apigen/` to `internal/gen/`, moving `client/` to `pkg/client/`, and deleting `api/types/`.

Those paths are historical proposal targets. The current source uses `internal/core`, `internal/db`, `internal/urlmanager`, `internal/api`, `apigen`, and `client`. Do not use the earlier layout or its phase instructions for new changes. The accepted worklist above supersedes it.
