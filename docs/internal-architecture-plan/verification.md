# Verification record and execution gates

This is a planning audit. Existing tests provide baseline evidence. They do not prove a proposed package migration. No application code, tests, module files, generated bindings, or deployment configuration were changed during this audit.

## Scope and reproducibility

The execution baseline is `development` at `45d8199696866aabbc86942cff9ecb2a1bba7f31`, inspected on 2026-09-05. The original audit inspected `feature/refine` at `5288ba166a4021a39e121b37920cad6df727f95e`; that branch was subsequently merged into `development`. The intervening application change is confined to storage signing compatibility and its regression test. WP01 must refresh the behavior baseline on the execution commit before architectural moves begin. Existing unrelated untracked files, including the previous audit and local client provider, were preserved.

The refreshed tracked source inventory contains 28 Go packages under `internal`, 129 ordinary Go files, 74 `_test.go` files, and 28,917 ordinary-file lines. The line count includes comments, blank lines, and ordinary Go files that implement test support. It excludes test files. There are 37 named production interface declarations, including private interfaces. Eighteen are in `internal/db/interface.go`.

[Package inventory](package-inventory.tsv) records direct local production imports and importers across the tracked root, client, and apigen modules. It does not count standard-library or external imports. Test-only callers are excluded from those counts. [Interface inventory](interface-inventory.tsv) records declared methods and embedded interfaces, rather than flattened method counts.

To reproduce the inventory from the repository root, run:

```sh
rtk proxy bash -c 'audit_dir=$(mktemp -d /tmp/syfon-inventory-XXXXXX); cp docs/internal-architecture-plan/inventory.go.txt "$audit_dir/inventory.go"; go run "$audit_dir/inventory.go"'
```

The [inventory program](inventory.go.txt) uses `git ls-files` and the Go parser. It is a source inventory, not a type checker or a build-tag-aware dependency analysis. Run `go list` and focused tests to check the build selected by the environment. The initial `go list ./internal/...` completed successfully.

The organizer workflow supplied the responsibility map and domain-first target. Pstack's Minimize Reader Load principle led to removing forwarding facades and moving interfaces to their consumers. Build the Lever led to the rerunnable source inventory, rather than treating a manually counted package list as complete evidence. Discovery and fresh cross-review used disjoint Luna assignments; the single Sol governor resolved the final design and work-package ordering.

## Server composition baseline

This check passed during the audit in 0.786 seconds:

```sh
rtk proxy go test -count=1 -timeout=120s ./cmd/server -run 'Test(OpenAPISpecRoutesRegistered|AllRegisteredEndpoints_WithMocks|AdminRoutesNotRegistered|HealthOnlyServerExposesNoOptionalRoutes|ApplyCredentialEncryptionConfig|LoadConfiguredBucketScopes)'
```

It exercises registered endpoints, OpenAPI route parity, optional route groups, and configuration wiring. It does not exercise real cloud providers or PostgreSQL.

The complete LFS and middleware checks initially encountered sandbox restrictions on local test listeners. A rerun with loopback listeners enabled passed:

```sh
rtk proxy go test -count=1 -timeout=120s ./internal/api/lfs ./internal/api/middleware
```

LFS passed in 1.367 seconds and middleware passed in 1.463 seconds. The earlier listener failures are resolved baseline environment failures, not outstanding application test failures.

## Package audit checks

Five disjoint Luna discovery workers ran the existing package checks. Fresh Luna cross-review checked their source, caller, and contract evidence. The table distinguishes tests from compilation and external integration coverage.

| Package group | Command after `rtk proxy` | Observed result |
| --- | --- | --- |
| Object/domain packages | `go test -count=1 -timeout=120s ./internal/core ./internal/common ./internal/models ./internal/repair` | All four passed. |
| HTTP packages | `go test -count=1 -timeout=120s ./internal/api/apiutil ./internal/api/docs ./internal/api/drsapi ./internal/api/internaldrs ./internal/api/lfs ./internal/api/routeutil` | Docs, DRS and internal routes passed. LFS passed in the later loopback-enabled run above. `apiutil` and `routeutil` have no test files. |
| Identity/metrics packages | `go test -count=1 -timeout=120s ./internal/auth ./internal/authz ./internal/api/middleware ./internal/api/attribution ./internal/api/metrics` | Auth, authz and metrics passed. Middleware passed in the later loopback-enabled run above. Attribution has no test files. |
| Persistence/config/encryption | `go test -count=1 -timeout=120s ./internal/db ./internal/db/sqlite ./internal/db/postgres ./internal/config ./internal/crypto` | Tested packages passed. `db` has no test files. Live PostgreSQL integration was skipped because `SYFON_TEST_POSTGRES_DSN` was unset. |
| Storage/test helpers/version | `go test -count=1 -timeout=120s ./internal/urlmanager ./internal/signer ./internal/signer/azure ./internal/signer/file ./internal/signer/gcs ./internal/signer/s3 ./internal/testutils ./internal/version` | URL manager and four providers passed. `signer`, `testutils`, and `version` have no test files. This does not establish live cloud or Docker integration. |

Workers used `GOCACHE=/tmp/syfon-architecture-Zf4AG5/gocache` when needed. A cache directory is not part of the behavior contract. The version CLI was also run with `rtk proxy go run . version`; it exited successfully and printed the development version and existing build metadata labels. Linker injection was inspected in the Makefile but was not exercised with a release build.

## Rules for the future executor

Run focused tests for each changed package and its direct importers after completing that package's migration. A worker must report the exact command, exit status, test skips, and relevant behavior assertions. A package with no test files has only compile evidence. A model's agreement is not a substitute for an executable check.

Use the current paths before a move and the destination paths afterward. Recompute direct importers instead of relying on the baseline inventory after packages move. Once a coherent integrated wave passes its focused checks, run the full workspace suite once:

```sh
rtk proxy go test -count=1 -timeout=120s ./... ./client/... ./apigen/...
```

Do not run that suite once per worker. Do not call skipped provider or database tests verified. An unavailable external dependency is an explicit incomplete integration gate.

Package and interface migrations must preserve HTTP methods, paths, aliases, registration order, status codes, headers, body formats, generated bindings, authorization decisions, database transaction boundaries, object identity, cloud request construction, cache invalidation, and cancellation semantics. The work packages identify the cases where current behavior is surprising. Preserve those cases during structural migration and handle a behavior correction separately.

SDK type ownership requires independent module validation and a release plan. Workspace builds alone can hide version-resolution problems. Run `GOWORK=off` checks from each affected module against the versions intended for release. If new local root and client versions are mutually required, document and validate the release order with isolated module fixtures. Do not publish tags as part of executing this planning document unless separately authorized.

Regenerate API bindings only if their source schema or generator configuration changes. Structural server moves should leave generated output unchanged. Validate linker flags before moving `internal/version`. Test support must not appear in any production package's transitive import graph.

## Audit gaps

The planning audit has not run migration tests because it implements no runtime change. Its recorded package checks ran on the original `feature/refine` audit commit; WP01 owns refreshing them on the execution baseline before migration work. The audit has not established live PostgreSQL, Docker provider, cloud account, KMS, or independently published-module compatibility. The previous audit's test totals are historical and are not this execution baseline's results.

## Planning-packet checks

Final consistency checks found dispositions for all 28 inventoried packages and all 37 interface names in the interface ledger. All 14 work packages have schedule assignments, and every prerequisite precedes its dependent package. Local Markdown file targets resolve, all three TSV files have consistent column counts, and the Markdown files have no trailing whitespace. `git diff --exit-code` passed: there are no tracked-file modifications. The only new artifact relative to the initial working-tree inventory is this planning directory.
