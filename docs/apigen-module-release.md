# `apigen` Module Versioning

`apigen` is a separate Go module:

- module path: `github.com/calypr/syfon/apigen`
- module root: `syfon/apigen`

## Local Development

This repo uses `syfon/go.work`:

```txt
use (
  .
  ./apigen
  ./client
)
```

So local builds (including Docker builds from `syfon/`) can resolve `apigen` without `replace` directives in `go.mod`.

## Production / CI

Production consumers should pin a published `apigen` version in `go.mod`, for example:

```go
require github.com/calypr/syfon/apigen vX.Y.Z
```

Do not rely on `replace` directives for released builds.

Run the independent-module gate before publishing:

```bash
make test-modules
```

The gate sets `GOWORK=off`, downloads each module graph, runs the complete test
suite for the root, `apigen`, and `client` modules, and runs a committed
external consumer fixture. The fixture starts a local HTTP server and exercises
both generated clients and SDK error matching, so it catches workspace-only
imports and missing published package paths.

The generated package layout and shared `errorapi` contract must be present in
the published `apigen` version selected by both the root and client modules.
Regenerate the bindings with the pinned parser and native Fiber v3/strict templates before
publishing. Validate the exact released versions with this gate; do not rely on
the workspace to hide missing package paths.

## Releasing `apigen`

From the `syfon` repository:

1. Commit generated `apigen/*` changes.
2. Tag the module version:

```bash
git tag apigen/vX.Y.Z
git push origin apigen/vX.Y.Z
```

3. Bump dependent modules:

```bash
# in syfon
go get github.com/calypr/syfon/apigen@vX.Y.Z
go mod tidy

# in data-client / git-drs (if they import apigen transitively/directly)
go get github.com/calypr/syfon/apigen@vX.Y.Z
go mod tidy
```

The API package move is a source migration. Replace imports from
`apigen/client/*` and `apigen/server/*` with the matching consolidated package
under `apigen/*`. Keep `lfsapi` separate because its Git LFS media types and
authentication headers are part of the wire contract. Keep generated `207`
multi-status response types for bulk operations.

For error handling, import `github.com/calypr/syfon/apigen/errorapi` and use
its exact or broad sentinels. The SDK's `services.ErrObjectNotFound` remains a
deprecated compatibility alias during migration. The transient
`client/sdkerror` package is not part of the published contract.
