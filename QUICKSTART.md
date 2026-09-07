## Quickstart

### Git submodule \(DRS schemas\)

The DRS spec is vendored as a submodule at:

* `ga4gh/data-repository-service-schemas`

Initialize/update it with:

```bash
git submodule update --init --recursive
```

The default OpenAPI file is:

* `ga4gh/data-repository-service-schemas/openapi/data_repository_service.openapi.yaml`

You can override it when generating code:

```bash
make gen OPENAPI=/path/to/custom/openapi.yaml
```

## Quickstart

### Prerequisites

* Go 1\.22\+
* Docker
* Git submodules initialized:

```bash
git submodule update --init --recursive
```

### Generate API stubs

```bash
make gen
# or override:
make gen OPENAPI=/path/to/custom/openapi.yaml
```

### Run tests

```bash
make test
```

### Run the server

```bash
make serve
```

Then hit for example:

* `/healthz`
* `/service-info`

### Serve docs \(MkDocs\)

```bash
make docs
```

Docs will be served on `http://localhost:8000`.

