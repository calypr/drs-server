# Local Deployment

Local Syfon deployments should normally use SQLite plus `auth.mode: local`. You do not need PostgreSQL for a local run.

If you only want the fastest first run, use [Quick Start](quickstart.md). If you need the raw config field reference, use [Server Configuration](configuration.md).

## Recommended Local Shape

For local development:

- use `database.sqlite`
- use `auth.mode: local`
- use `auth.basic.*`
- point bucket credentials at a local or dev S3-compatible endpoint such as MinIO

## Example Local Config

```yaml
port: 8080

auth:
  mode: local
  basic:
    username: drs-user
    password: drs-pass

database:
  sqlite:
    file: ./data/drs_local.db

credential_encryption:
  local_key_file: ./data/.syfon-credential-kek

buckets:
  - bucket: local-bucket
    provider: s3
    region: us-east-1
    endpoint: http://localhost:9000
    access_key: minio-user
    secret_key: minio-pass
```

Why this shape works:

- SQLite keeps the metadata store self-contained
- `auth.mode: local` avoids the Gen3, Fence, and PostgreSQL requirements
- `local_key_file` gives the local credential KEK an explicit stable path next to the SQLite DB

## Run From Source

```bash
go run . serve --config config.local.yaml
```

## Run With Docker

```bash
docker run \
  -p 8080:8080 \
  -v $(pwd)/config.local.yaml:/config.yaml \
  -v $(pwd)/data:/data \
  quay.io/ohsu-comp-bio/syfon:development serve --config /config.yaml
```

Use the published development image from Quay rather than building locally. Image tags are published at [Quay](https://quay.io/repository/ohsu-comp-bio/funnel?tab=tags).

## Smoke Test

```bash
curl -u drs-user:drs-pass http://localhost:8080/healthz
```

## Local Auth

For now, the documented local path is `auth.basic.username` plus `auth.basic.password`.

## Notes

- `auth.mode: gen3` is not the normal local path because it requires PostgreSQL unless you are explicitly using mock auth for integration testing.
- If your bucket credentials are non-empty, Syfon still needs valid credential encryption at runtime. `credential_encryption.local_key_file` is the simplest local option.
