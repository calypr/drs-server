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
