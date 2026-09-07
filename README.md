<div align="center">
  <img src="docs/images/syfon-logo.png" alt="syfon logo" width="520" />
  <br><br>
  <a href="https://pkg.go.dev/github.com/calypr/syfon"><img src="https://pkg.go.dev/badge/github.com/calypr/syfon.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/calypr/syfon"><img src="https://goreportcard.com/badge/github.com/calypr/syfon" alt="Go Report Card"></a>
  <a href="https://github.com/calypr/syfon/actions/workflows/ci.yaml"><img src="https://github.com/calypr/syfon/actions/workflows/ci.yaml/badge.svg" alt="CI"></a>
  <a href="https://app.codecov.io/gh/calypr/syfon/tree/development"><img src="https://codecov.io/gh/calypr/syfon/branch/development/graph/badge.svg?flag=root" alt="Server Coverage"></a>
  <a href="https://app.codecov.io/gh/calypr/syfon/tree/development"><img src="https://codecov.io/gh/calypr/syfon/branch/development/graph/badge.svg?flag=client" alt="Client Coverage"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
  <a href="CONTRIBUTING.md"><img src="https://img.shields.io/badge/PRs-welcome-brightgreen.svg" alt="PRs Welcome"></a>
  <a href="https://github.com/calypr/syfon/releases"><img src="https://img.shields.io/github/v/release/calypr/syfon" alt="Latest Release"></a>
  <br><br>
  <p align="center">A lightweight, production-grade implementation of a GA4GH Data Repository Service (DRS) server in Go.</p>
</div>

# Quickstart

## 1. Install Syfon

```bash
curl -sSL https://calypr.org/syfon/install.sh | bash
```

## 2. Start S3 (e.g. MinIO)

```sh
docker run --name minio --rm -p 9000:9000 \
  -e MINIO_ROOT_USER=example-user \
  -e MINIO_ROOT_PASSWORD=example-pass \
  quay.io/minio/minio server /tmp
```

## 3. Start Syfon Server

<details><summary><code>local.yaml</code></summary>

```yaml
port: 8080
auth:
  mode: local
  basic:
    username: "drs-user"
    password: "drs-pass"
database:
  sqlite:
    file: "drs_local.db"
s3_credentials:
  - bucket: "local-bucket"
    region: "us-east-1"
    access_key: "example-user"
    secret_key: "example-pass"
    endpoint: "http://localhost:9000"
```

</details>

```bash
syfon serve --config local.yaml
```

Smoke test:

```bash
curl -s http://localhost:8080/healthz
```

Notes:
- `auth.mode` is required and must be `local` or `gen3`.
- Local development should run `auth.mode: local` with SQLite only.
- In `local` mode, set `auth.basic.username/password` (or env `DRS_BASIC_AUTH_USER` / `DRS_BASIC_AUTH_PASSWORD`) to enforce HTTP basic auth. Unauthenticated local mode now requires the explicit development-only opt-in `auth.allow_unauthenticated: true`.
- `gen3` mode is for deployed environments and requires PostgreSQL.
- API route groups are enabled by default. Set `routes.*: false` or the matching `DRS_ENABLE_*` env var only when you intentionally want a reduced surface.

Record scope note:
- Syfon supports both scoped records (with `authz`, such as `/programs/<org>/projects/<project>`) and unscoped records (empty `authz`).
- Unscoped `ls` (`GET /index` without `organization/project/authz` filters) returns all records, including unscoped ones.
- RBAC checks still apply for scoped records in `gen3` mode.
- Recommended production policy: require project/authz at write time so unscoped records are not created unintentionally.

### Local Gen3 Mock Auth (no redeploy loop)

For local integration testing of Gen3 authorization behavior without Fence/Arborist and without PostgreSQL, run with mock auth:

```bash
DRS_AUTH_MODE=gen3 \
DRS_AUTH_MOCK_ENABLED=true \
DRS_AUTH_MOCK_RESOURCES="/data_file,/programs/cbds/projects/end_to_end_test" \
DRS_AUTH_MOCK_METHODS="read,file_upload,create,update,delete" \
go run . serve --config local.yaml
```

Optional:
- `DRS_AUTH_MOCK_REQUIRE_AUTH_HEADER=true` requires an `Authorization` header before mock privileges are injected.
- If `DRS_AUTH_MOCK_RESOURCES` is omitted, default is `"/data_file"`.
- If `DRS_AUTH_MOCK_METHODS` is omitted, default is `"*"` (full method access).

## Purpose

The `syfon` provides a robust implementation of the [GA4GH DRS API](https://ga4gh.github.io/data-repository-service-schemas/). It is designed to manage metadata for data objects and provide secure access via signed URLs.

### Key Features
- **GA4GH DRS Compliance**: Implements the standard DRS API for describing and accessing data objects.
- **Database Flexibility**: Supports both **SQLite** and **PostgreSQL** backends with a modular driver architecture.
- **S3 Integration**: Native support for Amazon S3 (and compatible storage like MinIO) with signed URL generation for downloads and multipart uploads.

## Configuration

The server is configured via a YAML or JSON file. Use the following structure to set up your environment:

```yaml
port: 8080
auth:
  mode: "local" # required: "local" or "gen3"
  basic:
    username: "user"
    password: "pass"

database:
  sqlite:
    file: "drs.db"
  # Or use PostgreSQL:
  # postgres:
  #   host: "localhost"
  #   port: 5432
  #   user: "user"
  #   password: "password"
  #   database: "drs"
  #   sslmode: "disable"

s3_credentials:
  - bucket: "my-test-bucket"
    region: "us-east-1"
    access_key: "AKIAXXXXXXXXXXXXXXXX"
    secret_key: "SECRETKEY"
    endpoint: "s3.amazonaws.com" # Optional: set for MinIO or custom backends
```

In `gen3` mode, PostgreSQL is required unless `DRS_AUTH_MOCK_ENABLED=true` is set for local mock-auth testing.
In `local` mode, Basic Auth or local CSV auth is required unless `auth.allow_unauthenticated: true` is explicitly set for development/testing.

Detailed configuration reference (including env overrides): [docs/configuration.md](docs/configuration.md)

## Gen3/PostgreSQL Schema Initialization

The Postgres backend bootstraps its object tables and required foreign keys on startup.
The Helm chart still ships an init job for deployments that want pre-provisioned schema, but it is no longer required for local startup.

## Local Development Workflow

```bash
go test ./... -count=1
./db/scripts/init_sqlite_db.sh drs_local.db
go run . serve --config local.yaml
```

## Minio Starter Kit

Start up a docker container with MinIO for testing:

```bash
docker run -p 9000:9000 -p 9001:9001 \
  -e "MINIO_ROOT_USER=minio-user" \
  -e "MINIO_ROOT_PASSWORD=minio-pass" \
  -v ./data:/data \
  minio/minio server /data --console-address ":9001"
```

The `minio/minio server` command starts with no buckets, so create the
bucket referenced in `local.yaml`. Run this once after the server is up
(the `-p` flag makes it idempotent):

```bash
docker run --rm --network host --entrypoint sh minio/mc -c '
  mc alias set local http://localhost:9000 minio-user minio-pass &&
  mc mb -p local/local-bucket
'
```

Create a config file called `local.yaml`

```yaml
port: 8080

auth:
  mode: local
  basic:
    username: "drs-user"
    password: "drs-pass"

database:
  sqlite:
    file: "drs_local.db"

s3_credentials:
  - bucket: "local-bucket"
    region: "us-east-1"
    access_key: "minio-user"
    secret_key: "minio-pass"
    endpoint: "http://localhost:9000"
    # Map an org/project scope to this bucket so uploads can resolve a target.
    # Omit `projects:` to match every project under the organization.
    resources:
      - organization: "example"
        projects:
          - project: "example"
```

Start the syfon server
```
syfon serve --config local.yaml
```

Upload a file. Because `local.yaml` enables basic auth, the CLI needs
matching credentials — pass them via `--username/--password` or the
`SYFON_USERNAME`/`SYFON_PASSWORD` environment variables:

```bash
export SYFON_USERNAME=drs-user
export SYFON_PASSWORD=drs-pass
syfon upload --file README.md --org example --project example
```

When `--did` is omitted, Syfon deterministically mints the object ID from the
file's SHA256 plus the canonical project scope
(`/organization/<org>/project/<project>`). Because of that, `--project` is
required whenever `--did` is omitted. The same file uploaded into the same
project gets the same DID across Syfon/Gen3 instances; the same file in a
different project gets a different DID.

List records
```
syfon ls
```

Download a record by DID (use a DID from `syfon ls`):
```
syfon download --did <did> --out /tmp/README.md
```

# Architecture

The project follows a modular structure to ensure maintainability:
- `db/core`: Core interfaces and models.
- `db/sqlite`, `db/postgres`: Database implementation drivers.
- `internal/api`: Subpackages for different API contexts (Core, internal compatibility, LFS, metrics, docs, middleware).
- `service`: High-level business logic implementing the DRS service.
- `urlmanager`: Logic for interacting with cloud storage providers.

See DB table details and relationships in [db/README.md](db/README.md).

## Go Client SDK (Multi-Module)

This repository now includes a separate Go client module at `./client`:

- Module path: `github.com/calypr/syfon/client`
- Purpose: reusable HTTP client for Syfon APIs (used by the Syfon CLI and external tools)

Example import:

```go
import syclient "github.com/calypr/syfon/client"
```

The root module (`github.com/calypr/syfon`) uses a local `replace` during development:

```go
replace github.com/calypr/syfon/client => ./client
```

# Development

The project uses a Makefile for common tasks:
- `make gen`: Generates the DRS server stubs from the official GA4GH OpenAPI spec (Git submodule) and refreshes the shared `apigen/*` OpenAPI outputs.
- `make test`: Runs all unit and integration tests.
- `make test-unit`: Runs unit tests only (excludes integration packages).
- `make coverage`: Runs coverage for core production packages (db/service/middleware/url signing) and writes `coverage/coverage.out`, `coverage/coverage.txt`, and `coverage/coverage.html`.
- `make coverage-full`: Runs broader compatibility-layer coverage (includes internal compatibility and LFS packages).
- `make serve ARGS="--config /path/to/config.yaml"`: Starts the DRS server.

## OpenAPI Codegen

Syfon currently uses `oapi-codegen` for both server-side and client-facing API artifacts:

- `make gen` bundles the GA4GH DRS spec into `apigen/openapi/openapi.yaml` and refreshes all generated packages under `apigen/client/*` and `apigen/server/*`.
- The runtime HTTP wiring, middleware, and compatibility behavior still live in handwritten code under `cmd/server` and `internal/api/*`.
- The client module itself is handwritten, but its request and response shapes come from the same generated OpenAPI contracts, so client and server stay aligned.

This split is intentional: generated code keeps the schema surface in sync, while handwritten runtime code preserves control over routing, auth, and compatibility behavior.

### Quick rules of thumb

- If you change the upstream GA4GH DRS schema or the Syfon overlay, run `make gen`.
- If you change `apigen/openapi/lfs.openapi.yaml`, `apigen/openapi/bucket.openapi.yaml`, `apigen/openapi/metrics.openapi.yaml`, or `apigen/openapi/internal.openapi.yaml`, run `make gen`.
- If you only change runtime wiring, auth, handlers, or business logic, you usually do not need codegen.
- If a PR touches OpenAPI files, include the regenerated `apigen/*` output in the same commit.

### What to expect after regeneration

- `apigen/server/drs` is overwritten with generated server code and generated DRS models.
- The relevant `apigen/client/*` and `apigen/server/*` packages are overwritten with generated Go types and helper files.
- `apigen/openapi/*.yaml` files are the bundled spec inputs that the runtime docs endpoint serves.
- `go test ./...` or the affected package tests should still pass after the regen.

## Running Integration Tests

You can run integration tests using your own config file:

```bash
go test ./cmd/server -v -count=1 -testConfig=example.yaml
```

Docker-backed MinIO upload and download coverage is available behind an opt-in flag:

```bash
SYFON_E2E_DOCKER=1 go test ./cmd -run TestSyfonDockerMinIOE2E -v -count=1
```

Docker-backed cloud emulator E2E suites are also available behind the same opt-in flag:

```bash
SYFON_E2E_DOCKER=1 go test ./cmd -run '^TestSyfonDockerFakeGCSE2E$' -v -count=1
SYFON_E2E_DOCKER=1 go test ./cmd -run '^TestSyfonDockerAzuriteE2E$' -v -count=1
```

To run all Docker-backed CLI E2E suites together (MinIO + fake-gcs-server + Azurite):

```bash
SYFON_E2E_DOCKER=1 go test ./cmd -run '^TestSyfonDocker(MinIOE2E|MultipartUpload|FakeGCSE2E|AzuriteE2E)$' -v -count=1
```

These suites are executed in CI with Docker; locally they will skip unless `SYFON_E2E_DOCKER=1` is set.

# License

This project is licensed under the MIT License. See [LICENSE](LICENSE).
