# Syfon API Generation

`apigen` is the committed OpenAPI generation output for Syfon.

## Overview
Syfon uses `oapi-codegen` with Fiber v3 templates, not the stock Gin runtime.
The generated packages under `apigen/*` are committed to the repository so the
runtime boundary is visible in code review.

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
- `internal/api/internaldrs`
- `internal/api/lfs`
- `internal/api/metrics`
- `internal/api/routeutil`

### Upstream reference

The upstream generator docs are here:

[oapi-codegen](https://github.com/oapi-codegen/oapi-codegen)
