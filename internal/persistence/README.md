# Persistence adapters

`internal/persistence` contains Syfon's SQL adapters. SQLite and PostgreSQL remain separate because their SQL, connection, locking, schema-upgrade, and parameter-limit behavior differs.

## Package layout

- `sqlite/` implements the contracts with SQLite. `sqlite.NewSqliteDB` initializes and upgrades the runtime schema before it returns.
- `postgres/` implements the contracts with PostgreSQL. `postgres.NewPostgresDB` initializes and upgrades the runtime schema before it returns.
- `postgres/object_schema.sql` contains the object tables and indexes embedded by the PostgreSQL implementation.
- `sqlite/scripts/` contains the manual SQLite bootstrap helper.
- `postgres/scripts/` contains PostgreSQL maintenance SQL.

Consumer packages own their narrow ports in `objects`, `buckets`, `transfers`, and `usage`. Both adapters implement those ports, and `cmd/server` explicitly composes the selected concrete backend. There is no shared database aggregate interface.

## Object and access tables

Both runtime backends store the following object data. SQLite creates these tables in `internal/persistence/sqlite/sqlite.go:initSchema`. PostgreSQL creates the object tables from `internal/persistence/postgres/object_schema.sql`.

### `drs_object`

One row stores the metadata for a canonical object record.

- `id` is the text primary key.
- `size`, `created_time`, `updated_time`, `name`, `version`, and `description` store object metadata.

Core uses the canonical SHA-256 checksum to group and resolve content. The record ID remains the persisted primary key. `drs_object_alias` maps an alias ID, such as an older UUID, to that canonical record.

### `drs_object_checksum`

This table stores one row for each typed checksum value.

- `object_id` references `drs_object.id`.
- `type` stores values such as `sha256` or `md5`.
- `checksum` stores the corresponding value.

`objects.CanonicalSHA256` normalizes SHA-256 values for identity and lookup. The runtime schema includes indexes for ordinary checksum lookup and the normalized SHA-256 identity expression.

### `drs_object_access_method`

This table stores the provider locations for an object.

- `object_id` references `drs_object.id`.
- `url` stores a location such as `s3://bucket/key`.
- `type` stores the provider type, such as `s3`.

Organization and project columns do not belong to this table. Scoped authorization uses `drs_object_controlled_access`.

### `drs_object_controlled_access`

Each row associates an object with an Arborist-compatible resource path in `resource`. Core builds a path from an organization and project with `common.ResourcePath`. API and authz code use the stored resource values when they evaluate access.

### `drs_object_read_policy`

This table stores the per-object `public_read` flag. The policy row is internal state and is not part of the DRS response.

### `drs_object_alias`

Each `alias_id` points to one canonical `object_id`. Object lookup resolves this mapping before loading the canonical row. Deletes and updates apply the current alias rules in each backend.

### `drs_object_name_alias`

This table stores prior or alternate object names. The `(object_id, name_alias)` pair is unique, and reads return normalized aliases with the object.

## Credentials, scopes, and transfer records

### `s3_credential`

The `credential_id` text column is the primary key. The row also stores `bucket`, `provider`, `region`, `access_key`, `secret_key`, and an optional `endpoint`.

Runtime initialization enforces one credential identity per physical bucket. SQLite uses triggers. PostgreSQL uses a trigger function. A bucket may still have multiple logical project scopes through `bucket_scope`.

### `bucket_scope`

The `(organization, project_id)` pair is the primary key. Each row maps a project to a `credential_id`, physical `bucket`, and optional `path_prefix`.

### `lfs_pending_metadata`

This table stores pending LFS metadata by `oid`, with creation and expiry timestamps. The LFS implementation consumes entries atomically when it verifies an upload.

### `object_usage` and `object_usage_event`

`object_usage` stores upload and download counters and their last-event timestamps. `object_usage_event` stores the event history used by usage reporting.

### `transfer_attribution_event`, `access_grant`, and `provider_transfer_event`

These tables store issued-access records, access grant aggregates, and provider transfer records. They include object, checksum, scope, actor, request, range, and reconciliation fields used by the metrics API.

## Runtime schema initialization

The application initializes its schema when it creates a database:

- `sqlite.NewSqliteDB` opens the configured SQLite file, enables its connection settings, calls `initSchema`, and runs compatibility upgrades before returning.
- `postgres.NewPostgresDB` opens and pings PostgreSQL, loads `object_schema.sql`, then runs the credential, bucket-scope, LFS, usage, and transfer schema initializers.

The SQLite runtime schema includes `drs_object`, the access and policy tables, aliases, credentials and scopes, LFS pending metadata, usage tables, transfer attribution tables, access grants, provider transfer events, indexes, and credential uniqueness triggers. Runtime initialization also handles older databases. It adds missing columns, migrates old credential identity shape, removes retired object columns, removes the retired browse index, and backfills access grants.

The PostgreSQL runtime schema has the same logical table groups. Its object DDL lives in `postgres/object_schema.sql`. The remaining DDL and compatibility statements live in `postgres/postgres.go`.

## Standalone SQLite script

`internal/persistence/sqlite/scripts/init_sqlite.sql` is a manual bootstrap helper used by `init_sqlite_db.sh`. The application does not read this file during normal startup. The script creates a basic object, access, checksum, credential, bucket-scope, and access-grant schema.

The script is intentionally smaller than the runtime schema. It omits `drs_object_read_policy`, `drs_object_alias`, `drs_object_name_alias`, `lfs_pending_metadata`, `object_usage`, `object_usage_event`, `transfer_attribution_event`, and `provider_transfer_event`, along with their runtime indexes and the SQLite credential uniqueness triggers. It also does not run the runtime compatibility upgrades or the access-grant backfill.

Starting Syfon against a database created by the script still runs `sqlite.NewSqliteDB`, which creates the missing runtime tables and applies its upgrades. The difference between the script and runtime DDL does not show that normal server startup fails. Do not treat the standalone script as the complete runtime schema or as a migration engine. Keep its behavior unchanged when reorganizing files.

## Add a persistence operation

1. Add the smallest suitable interface to the consuming domain package, or extend an existing consumer-owned port when the methods are cohesive.
2. Implement the port in each backend that supports it.
3. Wire the port explicitly at the server or test composition boundary.
4. Add backend-specific tests for SQL, transactions, missing rows, and error mapping.
5. Update a test double only when the test exercises the capability.

Keep SQLite and PostgreSQL SQL separate. Use the existing backend tests and the in-memory SQLite constructor for behavior that must match. Run a live PostgreSQL test when a change affects PostgreSQL persistence.

Do not add organization or project columns to `drs_object_access_method`; scoped access remains represented by controlled-access resources.
