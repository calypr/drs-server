# OpenAPI code generation

Syfon keeps its DRS contract and Syfon-specific HTTP contracts in OpenAPI documents. `make gen` bundles those inputs and refreshes the generated server and client packages.

## Schema inputs

The canonical DRS schema is the Git submodule at:

```text
data-repository-service-schemas/openapi/data_repository_service.openapi.yaml
```

The local contracts are:

```text
apigen/openapi/lfs.openapi.yaml
apigen/openapi/bucket.openapi.yaml
apigen/openapi/metrics.openapi.yaml
apigen/openapi/internal.openapi.yaml
```

The Makefile variables that select these inputs are `OPENAPI`, `SCHEMAS_SUBMODULE`, and `OPENAPI_DIR`.

## Generate bindings

Initialize the schema checkout when needed:

```bash
make init-schemas
```

Generate all contracts:

```bash
make gen
```

To use another DRS OpenAPI file, pass an absolute or repository-relative path:

```bash
make gen OPENAPI=/path/to/data_repository_service.openapi.yaml
```

The command bundles the DRS document into `apigen/openapi/openapi.yaml`, then writes generated files under `apigen/server/*` and `apigen/client/*`. The server configs use `oapi-codegen` Fiber and strict-server templates. The client configs generate request and response bindings without server adapters.

Do not edit generated files by hand. Change an OpenAPI input or generator config, run `make gen`, and commit the input and generated output together.

## Choose the right change

| Change | Edit | Run |
| --- | --- | --- |
| DRS endpoint or model | The schema submodule or the DRS overlay | `make gen` |
| LFS, bucket, metrics, or internal shape | The matching file under `apigen/openapi` | `make gen` |
| Generated naming or server template | The matching file under `apigen/codegen` or `apigen/templates` | `make gen` |
| Runtime route, middleware, or handler behavior | `internal/httpapi`, `internal/access`, or the owning domain package | No generation unless the contract also changes |

When an operation changes its request or response shape, update the OpenAPI document first. When only runtime behavior changes, keep the generated contract unchanged.

## Serve the generated documents

When `routes.docs` is enabled, the server provides:

- `GET /index/swagger` for Swagger UI;
- `GET /index/openapi.yaml` for the merged OpenAPI document;
- the individual local spec routes used by the docs and validation tooling.

The server reads embedded specs first and can use filesystem specs for local development. Route and spec changes need focused endpoint tests.

## Check the schema revision

The superproject records the submodule revision. Inspect it with:

```bash
git ls-tree HEAD data-repository-service-schemas
git -C data-repository-service-schemas rev-parse HEAD
```

Read `.gitmodules` for the configured upstream. Update the submodule pointer and regenerated output in the same change when the schema revision changes.
