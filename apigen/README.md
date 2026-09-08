# Syfon API Generation

`apigen` is the committed OpenAPI generation output for Syfon.

## Overview
Syfon uses `oapi-codegen` with Fiber v3 templates, not the stock Gin runtime.
The generated packages under `apigen/*` are committed to the repository so the
runtime boundary is visible in code review.

Each API has one generated package containing its models, client bindings, and
Fiber v3 server bindings. The generated packages are `drs`, `lfsapi`,
`bucketapi`, `metricsapi`, and `internalapi`. Their shared error envelope is
defined once in `openapi/error.openapi.yaml` and generated into `errorapi`.

## Package paths

The combined package is the supported import path for generated APIs. Migrate
imports as follows:

| Old import | Current import |
| --- | --- |
| `github.com/calypr/syfon/apigen/client/drs` | `github.com/calypr/syfon/apigen/drs` |
| `github.com/calypr/syfon/apigen/server/drs` | `github.com/calypr/syfon/apigen/drs` |
| `github.com/calypr/syfon/apigen/client/<pkg>` | `github.com/calypr/syfon/apigen/<pkg>` |
| `github.com/calypr/syfon/apigen/server/<pkg>` | `github.com/calypr/syfon/apigen/<pkg>` |

The DRS package is `apigen/drs`, not `apigen/drsapi`. Each current package
contains the generated models, client methods, and Fiber server methods for
that API. The `apigen/errorapi` package owns the shared error definitions.

The `N200ServiceInfo...` names in `apigen/drs/compat.go` are compatibility
aliases for generated DRS field names. They do not create another package path.

## Module release order

Release the `apigen` module before releasing a client version that requires it.
Tag `apigen/vX.Y.Z`, update the client requirement to that version, and then
run the client checks with `GOWORK=off`. The release check must resolve the
published `github.com/calypr/syfon/apigen` module without a local `replace`
directive. The repository `go.work` file is for local development only.

The generator configs live in `apigen/codegen/`:

- `oapi-drs.yaml`
- `oapi-internal.yaml`
- `oapi-lfs.yaml`
- `oapi-metrics.yaml`
- `oapi-bucket.yaml`

Each config points at local user templates in `apigen/templates/`:

### Fiber templates

- `templates/fiber/fiber-interface.tmpl`
  - Generates the server interface that receives `fiber.Ctx` plus typed params.
  - This is the main Fiber-specific replacement for the default interface template.
- `templates/fiber/fiber-middleware.tmpl`
  - Generates the Fiber route wrapper that extracts path/query/header/cookie values.
  - It turns raw Fiber request data into typed operation parameters.

### Strict templates

- `templates/strict/strict-fiber.tmpl`
  - Generates strict request objects.
  - Builds the request body wrapper used by `NewStrictHandler(...)`.
  - Handles Fiber body parsing, multipart readers, and response visiting.
- `templates/strict/strict-fiber-interface.tmpl`
  - Generates strict server interfaces and response object types.
  - This is the layer the service implementations satisfy.

### Regeneration

Use `make gen` from the repo root when changing:

- the OpenAPI specs in `apigen/openapi/*.openapi.yaml`
- the Fiber templates in `apigen/templates/*`
- the generator configs in `apigen/codegen/*`

The generated code is then consumed by:

- `cmd/server`
- `internal/httpapi/buckets`
- `internal/httpapi/drs`
- `internal/httpapi/lfs`
- `internal/httpapi/metrics`
- `internal/httpapi/records`
- `internal/httpapi/transfers`

### Upstream reference

The upstream generator docs are here:

[oapi-codegen](https://github.com/oapi-codegen/oapi-codegen)
