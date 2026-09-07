# Contributing to `syfon`

Thank you for your interest in contributing to `syfon`, a lightweight reference implementation of a GA4GH Data Repository Service (DRS) server in Go.

This document outlines how to set up your environment, make changes, and submit them.

## Project overview

`syfon`:

* Implements a GA4GH DRS\-compatible HTTP API in Go.
* Uses the official GA4GH DRS OpenAPI spec via a Git submodule at `ga4gh/data-repository-service-schemas`.
* Generates server stubs into `apigen` from that OpenAPI spec.

## Getting started

### Fork and clone

1. Fork the repository on GitHub.
2. Clone your fork:

   ```bash
   git clone git@github.com:<you>/syfon.git
   cd syfon
   ```

3. Initialize submodules:

   ```bash
   git submodule update --init --recursive
   ```

### Branching

Create a feature branch off `main`:

```bash
git checkout -b feature/<short-description>
```

Use short, descriptive names, e.g. `feature/add-service-info`, `fix/healthz-handler`.

## Development workflow

See QUICKSTART.md for details on common tasks.

### Updating the OpenAPI spec

If your change requires a newer DRS spec:

```bash
cd ga4gh/data-repository-service-schemas
git fetch origin
git checkout <tag-or-branch>
cd -
git add ga4gh/data-repository-service-schemas
git commit -m "Update DRS schemas to <tag-or-branch>"
```

Then regenerate:

```bash
make gen
```

Commit both the submodule update and any generated code.

## Coding guidelines

### Go style

* Follow standard Go conventions (`gofmt`, idiomatic naming).
* Keep handlers and business logic small and testable.
* Prefer composition over inheritance\-like patterns.

Before committing, run:

```bash
gofmt -w ./cmd ./internal
go test ./...
```

### Generated code

* Do not manually edit files under `apigen`.
* Regenerate using `make gen` when the OpenAPI spec changes.
* Commit the regenerated files along with the spec or handler changes.

#### Generated code policy (`apigen/`)

This repository keeps generated sources in version control.

* Generated files in `apigen/` **must be committed** with the PR that changes generation inputs.
* Regenerate `apigen/` when any of the following change:
  * OpenAPI documents under `apigen/openapi/`
  * Generator config/templates used by `make gen`
  * GA4GH schema submodule updates that affect generated output
* PRs that change generation inputs should include:
  * the input change
  * regenerated `apigen/` output
  * a short note in the PR description that codegen was run
* Generated files should include a `// GENERATED` header (or equivalent generator banner) so they are clearly machine-generated.

Regeneration command:

```bash
make gen
```

## Testing

* Add or update tests alongside your changes.
* Ensure `make test` passes before opening a pull request.
* For new endpoints or behaviors, include tests that cover both success and error paths.

## Documentation

> 🧪🧪🧪🧪 Update the docs/

* Update `README.md` or docs (served via `make mkdocs`) when public behavior or endpoints change.
* Keep examples minimal, accurate, and aligned with the DRS spec.

## Submitting changes

1. Ensure your branch is up to date with `main`.
2. Run:

   ```bash
   make test
   ```

3. Push your branch:

   ```bash
   git push -u origin feature/<short-description>
   ```

4. Open a pull request against the upstream `main` branch.
5. In the PR description, explain:
    * What changed.
    * Why it is needed.
    * Any implications for API, configuration, or deployment.

Address review feedback with additional commits; avoid force\-pushing unless requested.

## License and contribution terms

By contributing to this repository, you agree that your contributions will be licensed under the terms of the project license described in `LICENSE`.
