# Contributing to Syfon

Syfon is a Go DRS server with a separate client module and generated OpenAPI modules. Keep changes small, testable, and aligned with the [current documentation](docs/index.md).

## Set up a checkout

Install the Go version declared in `go.mod`, then clone the repository and initialize its schema submodule:

```bash
git clone https://github.com/calypr/syfon.git
cd syfon
git submodule update --init --recursive
```

Create a branch from the repository's default branch:

```bash
git switch -c fix/short-description
```

## Build and test

Run the focused checks while you work:

```bash
make build
make test-unit
```

Build output goes to `bin/syfon`. Use `bin/syfon serve --config <file>` to run the server from the checkout. `make test` runs the ungated Go package tests across the root, client, and `apigen` modules. Docker-backed CLI tests skip unless `SYFON_E2E_DOCKER=1`; run them with the command in [`tests/README.md`](tests/README.md). The Calypr kind tests and installer tests use the separate commands listed there.

Build the bundled authentication examples when you need to exercise plugin startup:

```bash
make build-plugins
```

See [Plugin integration](docs/plugins.md) for the contract and the limits of the example Gen3 plugin.

## Change API schemas

The canonical GA4GH schema is the `data-repository-service-schemas` submodule. Local contracts live under `apigen/openapi/`. Run code generation after changing either source:

```bash
make gen
```

Commit the input changes and the generated output together. Do not edit files under `apigen/client` or `apigen/server` by hand. Read the [OpenAPI code-generation guide](docs/operator-guide-code-generation.md) for the generated package layout and runtime serving path.

## Migrate root-module imports

The root package `github.com/calypr/syfon/common` was removed intentionally. Move access-resource helpers to `github.com/calypr/syfon/client/access` and move `NormalizeOid` and `NormalizeChecksum` to `github.com/calypr/syfon/client/hash`. Exported names and signatures remain unchanged, and the existing client API remains unchanged. Update old root imports before upgrading. Because the root module is pre-1.0, treat this import removal as a breaking minor release.

## Write code and docs

Format Go code with `gofmt`. Keep HTTP adapters, authorization, storage, and persistence concerns in their owning packages. Add tests with the package that owns the behavior.

Update `docs/` or the root README when a public command, configuration field, endpoint, or deployment workflow changes. Keep examples executable and use `bin/syfon` for commands built from a checkout.

Build the documentation before opening a pull request:

```bash
make docs
```

## Open a pull request

Before you open a pull request:

1. Run the focused tests for changed packages.
2. Run `make test` when the change crosses package or route boundaries.
3. Run `make docs` when you change documentation or navigation.
4. Describe behavior changes, migration steps, and any checks you could not run.

Do not include credentials, generated local databases, build output, or secrets in a pull request.
