# Operator Guide: OpenAPI Schema Loading, Code Generation, and Swagger Serving

This guide explains where Syfon loads OpenAPI schemas, how `make gen` works, how specs are served at Swagger endpoints, and how to update the GA4GH schema version.

## 1) Schema sources and default inputs

### GA4GH DRS canonical schema

Syfon vendors the upstream GA4GH DRS schemas as a Git submodule:

- Submodule path: `ga4gh/data-repository-service-schemas`
- Default DRS OpenAPI input: `ga4gh/data-repository-service-schemas/openapi/data_repository_service.openapi.yaml`

The `Makefile` defaults are:

```make
OPENAPI ?= ga4gh/data-repository-service-schemas/openapi/data_repository_service.openapi.yaml
SCHEMAS_SUBMODULE ?= ga4gh/data-repository-service-schemas
OPENAPI_DIR ?= apigen/openapi
CODEGEN_CONFIG_DIR ?= apigen/codegen
```

### Additional local OpenAPI inputs

Syfon also maintains local OpenAPI inputs for additional generated packages:

- `apigen/openapi/lfs.openapi.yaml`
- `apigen/openapi/bucket.openapi.yaml`
- `apigen/openapi/metrics.openapi.yaml`
- `apigen/openapi/internal.openapi.yaml`

## 2) OpenAPI code generation tasks in this repository

All OpenAPI generation is centralized in `make gen`.

| Target | Inputs | Outputs | Generator mode |
|---|---|---|---|
| `make gen` | GA4GH DRS spec plus local OpenAPI YAML files under `apigen/openapi/` | `apigen/openapi/*.yaml`, `apigen/server/*`, `apigen/client/*` | `oapi-codegen` with custom Fiber templates |

### Notes on `make gen`

`make gen` performs this flow:

1. Validate input and optionally initialize the GA4GH schema submodule.
2. Bundle the upstream GA4GH DRS schema with Redocly into `.tmp/drs.base.yaml`.
3. Copy the bundled DRS spec into `apigen/openapi/openapi.yaml`.
4. Apply the local `x-go-name` fixup for the checksum parameter.
5. Generate DRS server bindings into `apigen/server/drs/drs.gen.go`.
6. Generate LFS, bucket, metrics, and internal server bindings into `apigen/server/*`.
7. Generate DRS, LFS, bucket, metrics, and internal client bindings into `apigen/client/*`.
8. Apply the post-codegen DRS `service-info` type fix so generated output does not require handwritten compat shims.

### How generated code is used

The generated surface is split deliberately:

- `apigen/server/drs` contains the generated DRS server interfaces, routers, models, and helper types.
- `apigen/server/lfsapi`, `apigen/server/bucketapi`, `apigen/server/metricsapi`, and `apigen/server/internalapi` contain generated server-side contract packages for the local OpenAPI specs.
- `apigen/client/*` contains the generated client-side contract packages consumed across `syfon`, `git-drs`, and `data-client`.
- The `client` module itself is handwritten, but it consumes generated `apigen` request and response types so client and server stay aligned.

### Common change recipes

| Change you are making | Edit these files first | Regenerate with | Why |
|---|---|---|---|
| Add or remove a DRS endpoint | `ga4gh/data-repository-service-schemas` submodule | `make gen` | Keeps the bundled DRS spec and generated DRS bindings aligned. |
| Add or change a DRS field or model | Upstream spec or Syfon DRS overlay | `make gen` | Refreshes generated DRS client and server shapes. |
| Change LFS, bucket, metrics, or internal request/response shapes | `apigen/openapi/*.openapi.yaml` | `make gen` | Refreshes the generated package that owns that contract. |
| Add a local schema whose name collides with a `ClientWithResponses` wrapper | `apigen/codegen/client-oapi-*.yaml` plus the owning `apigen/openapi/*.openapi.yaml` | `make gen` | `oapi-codegen` uses `<OperationId>Response` wrapper names by default, so client configs may need `output-options.response-type-suffix` to avoid duplicate Go type names. |
| Change route behavior, auth, middleware, or validation logic only | `cmd/server`, `internal/api/*`, `internal/httpapi/middleware/*`, `internal/access/authentication/*` | No regen needed | These are handwritten runtime concerns, not generated contracts. |
| Update docs served at `/index/openapi.yaml` | `apigen/openapi/*.yaml` and embed/load code | `make gen` plus tests | The runtime docs endpoint reads the bundled spec files. |

### Service-info type fix

If `oapi-codegen` emits `N200ServiceInfo...` or `N200ServiceInfoJSONResponse...` references in DRS output, the repository keeps small handwritten `compat.go` alias files in `apigen/client/drs` and `apigen/server/drs`.

Those aliases exist because the `/service-info` response is a composed schema and `oapi-codegen` currently emits references to response-scoped enum names that it does not define.

### Client response wrapper naming collisions

`oapi-codegen` client generation has another failure mode that matters for local specs like `bucket.openapi.yaml`:

- schema model names live in the same Go package as generated `ClientWithResponses` wrapper types
- wrapper types default to `<OperationId>Response`
- if a schema model also uses that same name, the generated client becomes invalid Go

When that happens, prefer fixing the client config with:

```yaml
output-options:
  response-type-suffix: Resp
```

instead of hand-editing generated output.

The bucket client now uses that pattern so schema models like `DeleteProjectDataResponse` can coexist with response wrappers like `DeleteProjectDataResp`.

### What usually changes in git

- DRS changes usually touch the submodule pointer, `apigen/openapi/openapi.yaml`, and `apigen/server/drs` plus `apigen/client/drs`.
- LFS changes usually touch `apigen/openapi/lfs.openapi.yaml` and the generated `lfsapi` packages.
- Bucket, metrics, and internal schema changes follow the same pattern in their corresponding `apigen/openapi/*.openapi.yaml` file and generated package directories.
- Pure runtime changes usually do not touch `apigen/` at all.

## 3) Runtime serving path to Swagger

Swagger/OpenAPI serving flow:

1. Server startup calls `docs.RegisterSwaggerRoutes(api)`.
2. `GET /index/swagger` returns the Swagger UI page.
3. The UI loads `GET /index/openapi.yaml`.
4. `GET /index/openapi.yaml` is built by merging runtime spec documents:
   - required: `openapi.yaml` (DRS), `lfs.openapi.yaml`
   - optional: `bucket.openapi.yaml`, `metrics.openapi.yaml`, `internal.openapi.yaml`, `compat.openapi.yaml`
5. Spec files are read from embedded assets first via `apigen/openapi/specs_embed.go` (`ReadSpec`), with filesystem fallback for local dev.

### Sanity checks

- `make gen` should complete without recreating `.openapi-generator` metadata or a removed `apigen/model` package.
- `go test ./cmd/server ./tests/endpoints/...` should pass for DRS route and coverage changes.
- Route/spec changes should also be validated against:
  - `/index/swagger`
  - `/index/openapi.yaml`
  - `/index/lfs.openapi.yaml`
  - `/index/bucket.openapi.yaml`
  - `/index/internal.openapi.yaml`

## 4) Confirming currently pinned GA4GH schema version

Run from repo root:

```bash
# authoritative submodule pointer committed in superproject
git ls-tree HEAD ga4gh/data-repository-service-schemas

# branch hint configured for the submodule
cat .gitmodules

# initialize or refresh local submodule checkout
make init-schemas

# actual checked-out commit in local submodule worktree
git -C ga4gh/data-repository-service-schemas rev-parse HEAD
```
