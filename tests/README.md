# Tests

Package tests live next to the code they exercise. Run all three Go modules with
`make test`, or run the authentication, configuration, encryption and client
request checks with `make test-security`.

This directory contains tests that cross process or repository boundaries.

| Directory | Purpose | Run |
| --- | --- | --- |
| `endpoints` | Build and start the Syfon binary, then exercise HTTP endpoints | `go test ./tests/endpoints/... -count=1` |
| `calypr` | Run git-drs against Syfon, Fence and Arborist in kind | See [Calypr integration](calypr/README.md) |
| `release` | Verify sibling module versions and interrupted release retries using local Git repositories | `make test-release` |
| `install` | Exercise the release installer with local download fixtures | `make test-install` |

Docker-backed CLI tests live in `cmd`. They exercise MinIO, fake-gcs-server and
Azurite through the current CLI and transfer APIs.

```sh
SYFON_E2E_DOCKER=1 go test ./cmd -run '^TestSyfonDocker' -count=1 -v
```

For an existing server, use `TestSyfonExternalServerE2E` in `cmd/external_e2e_test.go`.
The retired shell drivers used unauthenticated legacy signing and index payloads;
the CLI and Calypr suites cover the supported transfer paths.
