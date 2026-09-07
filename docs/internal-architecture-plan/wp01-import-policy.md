# WP01 import policy

Run the direct production-import check from the repository root:

```sh
scripts/check_internal_import_policy.sh
```

Run the deterministic policy fixtures before changing the rules:

```sh
scripts/check_internal_import_policy.sh --self-test
```

The script uses the root module's `go list ./...` production package graph and
checks direct imports in the target domain, HTTP, storage-provider,
persistence, maintenance, and usage packages. The `.Imports` field excludes
test-only imports. It exits with status 0 and prints a pass line when no
forbidden edge exists. It exits with status 1 and prints each `package ->
import` edge when a forbidden import is found. Provider children may import
their cloud SDKs, and persistence dialect children may import their SQL
drivers; their other forbidden edges are still checked. `storage/address`
accepts only standard-library paths. The testsupport prohibition applies to
every root production package inspected, including packages not yet named by
the architecture rules. A `go list` or environment failure prints a
diagnostic and exits nonzero before evaluating policy edges.

WP01 intentionally leaves existing `internal/core`, `internal/common`,
`internal/models`, `internal/db`, and other legacy imports in place. The script
does not reject those imports until the later migration packages exist; this
keeps the characterization baseline useful and avoids turning WP01 into a
runtime or package-move change.
