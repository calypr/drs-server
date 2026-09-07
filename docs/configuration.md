# Server Configuration

This page documents the actual `syfon serve --config <file>` server config model.

The current config schema lives in `syfon/internal/config/config.go`. If the code and the docs ever disagree, the code wins.

This page documents raw Syfon server config only. If you are deploying with the Helm chart, see [Kubernetes Deployment](kubernetes-deployment.md) for the chart-specific values layout and how it maps into this server config.

## Example: Direct Syfon Config

```yaml
port: 8080

auth:
  mode: gen3

routes:
  docs: true
  ga4gh: true
  internal: true
  lfs: true
  metrics: true

database:
  postgres:
    host: postgres-svc
    port: 5432
    user: syfon
    password: REDACTED
    database: syfon
    sslmode: require

credential_encryption:
  master_key: REDACTED

s3_credentials:
  - bucket: cbds
    provider: s3
    endpoint: https://object.example.org
    region: us-east-1
    access_key: REDACTED
    secret_key: REDACTED
    resources:
      - organization: cbds
        org_path: organizations/cbds
        projects:
          - project_id: training
            project_path: projects/training
          - project_id: release_1
            project_path: projects/release_1

lfs:
  max_batch_objects: 1000
  max_batch_body_bytes: 10485760
  request_limit_per_minute: 1200
  bandwidth_limit_bytes_per_minute: 0

signing:
  default_expiry_seconds: 900
```

## Quick Reference

If you only need the shape of the config, this is the short version:

| Key | Type | Required | Purpose |
| --- | --- | --- | --- |
| `port` | integer | no | HTTP listen port for the Syfon server |
| `database` | object | yes | Exactly one metadata backend: `sqlite` or `postgres` |
| `auth` | object | yes | Authentication mode and auth-specific settings |
| `routes` | object | no | Turn route groups on or off |
| `credential_encryption` | object | recommended | Controls how stored bucket credentials are encrypted at rest |
| `buckets` | array | no | Current bucket credential config field |
| `s3_credentials` | array | no | Legacy alias for `buckets`; do not set both |
| `bucket_scopes` | array | no | Explicit organization/project to bucket/path mappings |
| `lfs` | object | no | Git LFS request sizing and rate limits |
| `signing` | object | no | Signed URL expiry defaults |

Most production configs need:

- `database.postgres`
- `auth.mode: gen3`
- `credential_encryption`
- `buckets` or legacy `s3_credentials`
- either `resources` under each bucket entry or explicit `bucket_scopes`

Most local configs need:

- `database.sqlite`
- `auth.mode: local`
- one simple bucket entry

Important constraints:

- `buckets` is the current field.
- `s3_credentials` is still accepted as a legacy alias.
- You may specify only one of `buckets` or `s3_credentials`.
- Internally, legacy `s3_credentials` entries are normalized into `buckets`.
- `database` must contain exactly one backend.
- `auth.mode` must be exactly `local` or `gen3`.

## Detailed Reference

The rest of this page is the detailed field-by-field reference with accepted formats, defaults, validation rules, and provider-specific behavior.

## `port`

```yaml
port: 8080
```

- Type: integer
- Default: `8080`
- Environment override: `DRS_PORT`
- Meaning: TCP port the Fiber HTTP server listens on inside the container or process

## `database`

Exactly one database backend must be configured.

### SQLite

```yaml
database:
  sqlite:
    file: ./drs.db
```

- Type: string path
- Good for local development and simple deployments.
- Environment override: `DRS_DB_SQLITE_FILE`
- Meaning: path to the SQLite database file used for object metadata, bucket credentials, and server state

### PostgreSQL

```yaml
database:
  postgres:
    host: postgres-svc
    port: 5432
    user: syfon
    password: REDACTED
    database: syfon
    sslmode: require
```

| Field | Type | Notes |
| --- | --- | --- |
| `host` | string hostname or service DNS name | PostgreSQL server host |
| `port` | integer | PostgreSQL server port |
| `user` | string | PostgreSQL username |
| `password` | string | PostgreSQL password |
| `database` | string | Database name to open |
| `sslmode` | string | Typical values: `disable`, `require`, `verify-ca`, `verify-full`; env-driven default is `require` |

- Required for normal `auth.mode: gen3` deployments unless Gen3 mock auth is enabled for testing.
- Environment overrides: `DRS_DB_HOST`, `DRS_DB_PORT`, `DRS_DB_USER`, `DRS_DB_PASSWORD`, `DRS_DB_DATABASE`, `DRS_DB_SSLMODE`

## `auth`

Supported auth modes:

- `local`
- `gen3`

### Local

```yaml
auth:
  mode: local
  basic:
    username: drs-user
    password: drs-pass
```

| Field | Type | Notes |
| --- | --- | --- |
| `auth.basic.username` | string | Username for built-in Basic Auth in local mode |
| `auth.basic.password` | string | Password for built-in Basic Auth in local mode |
| `auth.allow_unauthenticated` | boolean | Default `false`; development-only bypass when you do not want Basic Auth configured |

Environment overrides: `DRS_AUTH_MODE`, `DRS_BASIC_AUTH_USER`, `DRS_BASIC_AUTH_PASSWORD`, `DRS_ALLOW_UNAUTHENTICATED_LOCAL`

### Gen3

```yaml
auth:
  mode: gen3
  fence_url: https://fence.example.org
  cache:
    enabled: true
    ttl_seconds: 60
    negative_ttl_seconds: 15
    max_entries: 10000
    cleanup_seconds: 60
```

| Field | Type | Notes |
| --- | --- | --- |
| `auth.fence_url` | string URL | Expected form: single `https://...` URL; trusted Fence issuer URL for JWT validation |
| `auth.plugin_paths.authn` | string path | Copied into `SYFON_AUTHN_PLUGIN_PATH` for external authentication plugin execution |
| `auth.plugin_paths.authz` | string path | Copied into `SYFON_AUTHZ_PLUGIN_PATH` for external authorization plugin execution |

Cache fields:

| Field | Type | Notes |
| --- | --- | --- |
| `auth.cache.enabled` | boolean | Default at request time: `true`; turns the Gen3 auth decision cache on or off |
| `auth.cache.ttl_seconds` | integer seconds | Default at request time: `45`; positive cache TTL for successful authz/authn results |
| `auth.cache.negative_ttl_seconds` | integer seconds | Default at request time: `8`; negative cache TTL for denied or missing results |
| `auth.cache.max_entries` | integer | Default at request time: `20000`; max number of cached auth results kept in memory |
| `auth.cache.cleanup_seconds` | integer seconds | Default at request time: `60`; how often expired cache entries are swept |

Mock auth fields:

| Field | Type | Notes |
| --- | --- | --- |
| `auth.mock.enabled` | boolean | Default `false`; enables the built-in fake Gen3 auth mode for local integration testing |
| `auth.mock.require_auth_header` | boolean | Default `false`; if true, mock auth still requires an `Authorization` header |
| `auth.mock.resources` | array of strings | Default at request time: `["/data_file"]`; Arborist-style resources granted by the mock authorizer |
| `auth.mock.methods` | array of strings | Default at request time: `["*"]`; methods granted by the mock authorizer |

Validation and behavior:

- `auth.mode` is required and must be exactly `local` or `gen3`
- `auth.mode: gen3` requires PostgreSQL unless mock auth is enabled
- `auth.mode: local` requires `auth.basic.username` plus `auth.basic.password`, unless `auth.allow_unauthenticated: true`
- `auth.basic.username` and `auth.basic.password` must be set together
- mock auth is only allowed in `gen3` mode

Environment overrides: `DRS_FENCE_URL`, `DRS_AUTH_MOCK_ENABLED`, `DRS_AUTH_MOCK_RESOURCES`, `DRS_AUTH_MOCK_METHODS`, `DRS_AUTH_MOCK_REQUIRE_AUTH_HEADER`, `DRS_AUTH_CACHE_ENABLED`, `DRS_AUTH_CACHE_TTL_SECONDS`, `DRS_AUTH_CACHE_NEGATIVE_TTL_SECONDS`, `DRS_AUTH_CACHE_MAX_ENTRIES`, `DRS_AUTH_CACHE_CLEANUP_SECONDS`

## `routes`

```yaml
routes:
  docs: true
  ga4gh: true
  internal: true
  lfs: true
  metrics: true
```

All of these default to `true`.

| Field | Type | Notes |
| --- | --- | --- |
| `routes.docs` | boolean | Swagger/OpenAPI docs routes |
| `routes.ga4gh` | boolean | GA4GH DRS routes under `/ga4gh/drs/v1/...` |
| `routes.internal` | boolean | Internal upload/download/index/bucket routes under `/data` and `/index` |
| `routes.lfs` | boolean | Git LFS-compatible routes |
| `routes.metrics` | boolean | Transfer and file-usage metrics routes |

Environment overrides: `DRS_ENABLE_DOCS`, `DRS_ENABLE_GA4GH`, `DRS_ENABLE_INTERNAL`, `DRS_ENABLE_LFS`, `DRS_ENABLE_METRICS`

## `credential_encryption`

```yaml
credential_encryption:
  master_key: REDACTED
  local_key_file: /var/lib/syfon/.syfon-credential-kek
```

| Field | Type | Notes |
| --- | --- | --- |
| `credential_encryption.master_key` | string | KEK material for encrypting persisted bucket credentials; accepted formats: 32-character raw string, 64-character hex string, or base64-encoded 32-byte key; not an access token or password |
| `credential_encryption.local_key_file` | string path | Path where Syfon loads or persists the server-local KEK; fallback order: `DRS_CREDENTIAL_LOCAL_KEY_FILE`, then next to the SQLite DB as `.syfon-credential-kek`, then `/app/.syfon-credential-kek` |

Environment overrides: `DRS_CREDENTIAL_MASTER_KEY`, `DRS_CREDENTIAL_LOCAL_KEY_FILE`

Use this to control how persisted bucket credentials are encrypted at rest. See also [Encryption](encryption.md).

Operational note:

- if `master_key` is omitted, Syfon can still run in local key-file mode and manage a KEK on disk
- if bucket credentials are being persisted, encryption must still be valid at runtime

## `buckets` and `s3_credentials`

These entries define stored signing credentials.

```yaml
s3_credentials:
  - bucket: cbds
    provider: s3
    endpoint: https://object.example.org
    region: us-east-1
    access_key: REDACTED
    secret_key: REDACTED
```

| Field | Type | Notes |
| --- | --- | --- |
| `bucket` | string | Storage bucket or container name used for signing |
| `provider` | string | Storage provider selector |
| `region` | string | Cloud region; required for `provider: s3` |
| `access_key` | string | Provider credential identifier |
| `secret_key` | string | Provider credential secret |
| `endpoint` | string URL | Custom endpoint for S3-compatible systems or local object stores |
| `resources` | array | Organization/project mapping rules used to derive bucket scopes automatically |

What this entry actually does:

- it tells Syfon which credential set should sign URLs for a given bucket
- it optionally tells Syfon which Gen3 organizations and projects belong in that bucket
- if `resources` is present, Syfon derives `bucket_scopes` entries automatically from it during config load

### Supported providers

Accepted provider names:

- `s3`
- `gcs`
- `gs`
- `azure`
- `azblob`
- `file`

Normalization:

- `gs` becomes `gcs`
- `azblob` becomes `azure`

### Required fields by provider

For `provider: s3`:

- `bucket` is required
- `region` is required
- `access_key` is required
- `secret_key` is required
- `endpoint` is optional and commonly used for S3-compatible systems

Bucket-name validation is provider-specific. For `s3`, Syfon uses default AWS-style validation with lowercase letters, numbers, and hyphens in the usual 3-63 character range; if `endpoint` is set, it allows a looser S3-compatible bucket pattern. For `gcs`, Syfon allows lowercase letters, numbers, hyphens, underscores, and dots, but rejects `goog...` names and IP-address-like bucket names. For `azure`, Syfon allows lowercase letters, numbers, and hyphens, and rejects consecutive hyphens. For `file`, Syfon does not enforce bucket-name validation.

For `gcs`, `azure`, and `file`, the validation rules are looser in config loading, but `bucket` and provider-appropriate runtime behavior still matter.

## `resources`

This is the part that usually does the heavy lifting for organization/project mapping.

Example:

```yaml
s3_credentials:
  - bucket: cbds
    provider: s3
    region: us-east-1
    access_key: REDACTED
    secret_key: REDACTED
    resources:
      - organization: cbds
        org_path: organizations/cbds
        projects:
          - project_id: release_1
            project_path: projects/release_1
          - project_id: release_2
            project_path: projects/release_2
      - organization: root_only
        org_path: roots/root_only
```

| Resource field | Type | Notes |
| --- | --- | --- |
| `organization` | string | Gen3 program / organization name |
| `org_path` | string path fragment | Storage prefix to use for the organization root inside the bucket |
| `projects` | array | Nested project-specific mappings under the organization |

| Project field | Type | Notes |
| --- | --- | --- |
| `project_id` | string | Gen3 project identifier |
| `project` | string | Alternate project field name accepted by config normalization |
| `project_path` | string path fragment | Project-specific suffix joined under `org_path` |
| `path` | string | Direct fully qualified storage path override such as `s3://bucket/prefix` |
| `path_prefix` | string | Normalized prefix override inside the bucket |

How this works:

Each bucket credential can declare one or more organizations. Each organization can optionally declare nested project mappings. Syfon converts these mappings into `bucket_scopes` internally. `org_path` and `project_path` are joined into the final storage prefix unless a project declares `path` or `path_prefix`, in which case that explicit project mapping wins. If an organization has no `projects`, Syfon derives an organization-wide scope rooted at `org_path`.

So a config like:

```yaml
resources:
  - organization: cbds
    org_path: organizations/cbds
    projects:
      - project_id: release_1
        project_path: projects/release_1
```

derives a scope roughly equivalent to:

```yaml
bucket_scopes:
  - organization: cbds
    project_id: release_1
    bucket: cbds
    path_prefix: organizations/cbds/projects/release_1
```

## `bucket_scopes`

You can also declare explicit scopes directly instead of deriving them from `resources`.

```yaml
bucket_scopes:
  - organization: cbds
    project_id: release_1
    bucket: cbds
    path_prefix: organizations/cbds/projects/release_1
```

| Field | Type | Notes |
| --- | --- | --- |
| `organization` | string | Gen3 program / organization name |
| `project_id` | string | Gen3 project id |
| `bucket` | string | Bucket or container to use for this scope |
| `path` | string URL-like storage path | Expected form: `s3://bucket/prefix` |
| `path_prefix` | string path fragment | Normalized prefix under the bucket |
| `organization_sub_path` | string path fragment | Organization-level composed-path fragment |
| `project_sub_path` | string path fragment | Project-level composed-path fragment |

Rules worth knowing:

`organization` must be a Gen3 program name, not a storage path, and `project_id` must be a Gen3 project identifier, not a storage path. `path` may include a fully qualified storage path like `s3://bucket/prefix`, and `path_prefix` is the normalized prefix under the bucket and should not start with `/`. `organization_sub_path` and `project_sub_path` are a composed-path convenience mode. `path` cannot be combined with `organization_sub_path` or `project_sub_path`, and `path_prefix` cannot be combined with them either. When `path` is present, Syfon parses the provider from the URL scheme, extracts the bucket from the URL host, and normalizes the path into `path_prefix`. When `organization_sub_path` or `project_sub_path` is used, `bucket` becomes required. Syfon joins `organization_sub_path` and `project_sub_path` with normalized path cleaning, and every final scope must resolve to a bucket, either from `bucket` directly or from the bucket embedded in `path`.

## `lfs`

```yaml
lfs:
  max_batch_objects: 1000
  max_batch_body_bytes: 10485760
  request_limit_per_minute: 1200
  bandwidth_limit_bytes_per_minute: 0
```

| Field | Type | Notes |
| --- | --- | --- |
| `lfs.max_batch_objects` | integer | Default `1000`; max object count accepted in a single LFS batch request |
| `lfs.max_batch_body_bytes` | integer bytes | Default `10485760` (10 MiB); max request body size accepted by the LFS batch endpoint |
| `lfs.request_limit_per_minute` | integer | Default `1200`; per-client request-rate limit for the LFS middleware |
| `lfs.bandwidth_limit_bytes_per_minute` | integer bytes | Default `0`; per-client bandwidth cap for LFS routes; `0` disables the cap |

Environment overrides: `DRS_LFS_MAX_BATCH_OBJECTS`, `DRS_LFS_MAX_BATCH_BODY_BYTES`, `DRS_LFS_REQUEST_LIMIT_PER_MINUTE`, `DRS_LFS_BANDWIDTH_LIMIT_BYTES_PER_MINUTE`

## `signing`

```yaml
signing:
  default_expiry_seconds: 900
```

| Field | Type | Notes |
| --- | --- | --- |
| `signing.default_expiry_seconds` | integer seconds | Default `900`; default lifetime for signed URLs when a caller does not request a custom expiry |

Environment override: `DRS_SIGNING_DEFAULT_EXPIRY_SECONDS`

## Practical Guidance

For most production deployments:

- use `auth.mode: gen3`
- use PostgreSQL
- enable all normal route groups unless you have a reason to reduce surface area
- store credential encryption material outside the image
- use `s3_credentials` or `buckets` plus `resources` when your bucket layout follows organization/project conventions
- use explicit `bucket_scopes` when you need exact path control

For local development:

- use `auth.mode: local`
- use SQLite
- use a small local config file with one bucket credential

## Related Pages

- [Deployment](deployment.md)
- [Encryption](encryption.md)
- [Troubleshooting](troubleshooting.md)
