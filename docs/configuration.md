# Server configuration

Syfon loads a YAML or JSON file passed to `syfon serve --config <file>`. Environment variables override selected fields after the file is decoded. The server validates the merged config before it starts.

If a field in this page differs from the code, the types in `internal/config/types.go` and validation in `internal/config/validation.go` define the behavior.

## Minimal configurations

Local development needs one database and one auth mode:

```yaml
port: 8080

auth:
  mode: local
  basic:
    username: drs-user
    password: drs-pass

database:
  sqlite:
    file: ./data/drs.db
```

Production Gen3 deployments normally use PostgreSQL:

```yaml
port: 8080

auth:
  mode: gen3
  fence_url: https://fence.example.org

database:
  postgres:
    host: postgres
    port: 5432
    user: syfon
    password: REDACTED
    database: syfon
    sslmode: require
```

Both modes need a storage entry when the server handles object transfers. Use `buckets` for new configs:

```yaml
buckets:
  - bucket: data
    provider: s3
    region: us-east-1
    access_key: REDACTED
    secret_key: REDACTED
    endpoint: https://object.example.org
    resources:
      - organization: example
        projects:
          - project_id: research
```

## Top-level fields

| Field | Default | Description |
| --- | --- | --- |
| `port` | `8080` | HTTP listen port. |
| `database` | none | Exactly one `sqlite` or `postgres` backend. |
| `auth` | none | Authentication mode and integrations. |
| `buckets` | empty | Current bucket credential list. |
| `s3_credentials` | empty | Legacy alias for `buckets`. Do not set both. |
| `bucket_scopes` | empty | Explicit organization/project to storage mappings. |
| `credential_encryption` | key-file defaults | Key material for stored bucket credentials. |
| `routes` | all `true` | Route-group switches. |
| `lfs` | see below | Git LFS limits. |
| `signing` | 900 seconds | Signed URL defaults. |

## `database`

Configure exactly one backend.

### SQLite

```yaml
database:
  sqlite:
    file: ./data/drs.db
```

`file` is the SQLite database path. The `DRS_DB_SQLITE_FILE` environment variable overrides it.

### PostgreSQL

```yaml
database:
  postgres:
    host: postgres
    port: 5432
    user: syfon
    password: REDACTED
    database: syfon
    sslmode: require
```

The fields map to `DRS_DB_HOST`, `DRS_DB_PORT`, `DRS_DB_USER`, `DRS_DB_PASSWORD`, `DRS_DB_DATABASE`, and `DRS_DB_SSLMODE`. Supplying `DRS_DB_HOST` or `DRS_DB_DATABASE` selects PostgreSQL when the file does not already define it. `auth.mode: gen3` requires PostgreSQL unless Gen3 mock auth is enabled.

## `auth`

`auth.mode` is required and must be `local` or `gen3`.

### Local mode

```yaml
auth:
  mode: local
  basic:
    username: drs-user
    password: drs-pass
  local_authz_csv: ./authz.csv
```

`basic.username` and `basic.password` must be set together. Local mode requires those credentials or `local_authz_csv`, unless `allow_unauthenticated: true` is set for development or tests. The environment variables are `DRS_AUTH_MODE`, `DRS_BASIC_AUTH_USER`, `DRS_BASIC_AUTH_PASSWORD`, `DRS_LOCAL_AUTHZ_CSV`, and `DRS_ALLOW_UNAUTHENTICATED_LOCAL`.

The CSV can map usernames to passwords and scoped privileges. Keep the file readable only by the server user.

### Gen3 mode

```yaml
auth:
  mode: gen3
  fence_url: https://fence.example.org
  plugin_paths:
    authn: /opt/syfon/plugin/gen3_auth
    authz: /opt/syfon/plugin/authorization
```

`fence_url` sets the trusted issuer used for token authorization lookups. Syfon exports it as `DRS_FENCE_URL`.

`plugin_paths.authn` and `plugin_paths.authz` name executable server-side plugin binaries. Syfon exports them as `SYFON_AUTHN_PLUGIN_PATH` and `SYFON_AUTHZ_PLUGIN_PATH` during startup. See [Plugin integration](plugins.md) for the RPC contracts.

### Gen3 mock auth

Use mock auth for local integration tests that need Gen3 authorization behavior without Fence or PostgreSQL. Set `DRS_AUTH_MOCK_ENABLED=true` in the process environment before startup so config validation permits SQLite:

```yaml
auth:
  mode: gen3

database:
  sqlite:
    file: ./data/drs.db
```

```bash
DRS_AUTH_MOCK_ENABLED=true \
DRS_AUTH_MOCK_REQUIRE_AUTH_HEADER=true \
DRS_AUTH_MOCK_RESOURCES=/data_file \
DRS_AUTH_MOCK_METHODS=read,file_upload,create,update,delete \
bin/syfon serve --config config.gen3-mock.yaml
```

Mock auth is only allowed in Gen3 mode. The `auth.mock` config fields also export to `DRS_AUTH_MOCK_ENABLED`, `DRS_AUTH_MOCK_REQUIRE_AUTH_HEADER`, `DRS_AUTH_MOCK_RESOURCES`, and `DRS_AUTH_MOCK_METHODS` after config validation. Set the enabled environment variable explicitly when a SQLite config must pass Gen3 validation.

## `buckets` and `s3_credentials`

Each bucket entry stores credentials and an optional provider-specific endpoint:

```yaml
buckets:
  - bucket: data
    provider: s3
    region: us-east-1
    access_key: REDACTED
    secret_key: REDACTED
    endpoint: https://object.example.org
```

Supported providers are `s3`, `gcs`, `azure`, and `file`. The aliases `gs` and `azblob` are accepted. For S3, `bucket`, `region`, `access_key`, and `secret_key` are required. GCS uses service-account material in the credential fields. Azure uses the credential fields required by its shared-key signer. The file provider uses `endpoint` as its local storage root.

`s3_credentials` remains accepted for older configs. Syfon copies it into `buckets` during validation. Set one field or the other, never both.

### Map storage to projects

Use `resources` when a bucket follows an organization and project layout:

```yaml
buckets:
  - bucket: data
    provider: s3
    region: us-east-1
    access_key: REDACTED
    secret_key: REDACTED
    resources:
      - organization: example
        org_path: organizations/example
        projects:
          - project_id: research
            project_path: projects/research
```

Syfon derives a `bucket_scopes` entry with the joined path `organizations/example/projects/research`. If an organization has no `projects`, the mapping applies to the organization. Use `path` or `path_prefix` for an explicit storage location.

Declare exact mappings with `bucket_scopes` when the layout does not follow the bucket `resources` shape:

```yaml
bucket_scopes:
  - organization: example
    project_id: research
    path: s3://data/organizations/example/projects/research
```

`organization` and `project_id` are logical Gen3 names. `path` must include a supported provider scheme and bucket. `path_prefix` is the normalized prefix within the bucket. `path` cannot be combined with `path_prefix`, `organization_sub_path`, or `project_sub_path`.

## `credential_encryption`

```yaml
credential_encryption:
  local_key_file: ./data/.syfon-credential-kek
  master_key: REDACTED
```

`local_key_file` selects the server-local key file. `DRS_CREDENTIAL_LOCAL_KEY_FILE` overrides it. `master_key` supplies key material for the configured credential cipher and is redacted when config is logged. `DRS_CREDENTIAL_MASTER_KEY` is read directly by the credential-encryption package. See [Credential Encryption](encryption.md) for key handling and deployment guidance.

## `routes`

All route groups default to `true`:

```yaml
routes:
  docs: true
  ga4gh: true
  internal: true
  lfs: true
  metrics: true
```

The groups are `docs`, `ga4gh`, `internal`, `lfs`, and `metrics`. Environment overrides are `DRS_ENABLE_DOCS`, `DRS_ENABLE_GA4GH`, `DRS_ENABLE_INTERNAL`, `DRS_ENABLE_LFS`, and `DRS_ENABLE_METRICS`.

## `lfs`

```yaml
lfs:
  max_batch_objects: 1000
  max_batch_body_bytes: 10485760
  request_limit_per_minute: 1200
  bandwidth_limit_bytes_per_minute: 0
```

The defaults are 1,000 objects, 10 MiB, 1,200 requests per minute, and no bandwidth cap. Environment overrides are `DRS_LFS_MAX_BATCH_OBJECTS`, `DRS_LFS_MAX_BATCH_BODY_BYTES`, `DRS_LFS_REQUEST_LIMIT_PER_MINUTE`, and `DRS_LFS_BANDWIDTH_LIMIT_BYTES_PER_MINUTE`.

## `signing`

```yaml
signing:
  default_expiry_seconds: 900
```

`default_expiry_seconds` controls the default lifetime of signed URLs. Override it with `DRS_SIGNING_DEFAULT_EXPIRY_SECONDS`.

## Global environment overrides

`DRS_PORT` overrides `port`. `DRS_AUTH_MODE` overrides `auth.mode`. Database, auth, route, LFS, and signing overrides are listed in the sections above. File values take precedence only when an environment variable is absent.

Related pages: [Local Deployment](local-deployment.md), [Kubernetes Deployment](kubernetes-deployment.md), [Credential Encryption](encryption.md), and [Troubleshooting](troubleshooting.md).
