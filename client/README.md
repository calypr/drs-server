# syfon client (Go SDK)

[![Go Reference](https://pkg.go.dev/badge/github.com/calypr/syfon/client.svg)](https://pkg.go.dev/github.com/calypr/syfon/client)
[![Go Report Card](https://goreportcard.com/badge/github.com/calypr/syfon/client)](https://goreportcard.com/report/github.com/calypr/syfon/client)
[![Go Version](https://img.shields.io/badge/go-1.26.1-00ADD8?logo=go)](https://go.dev/doc/devel/release)
[![CI](https://github.com/calypr/syfon/actions/workflows/ci.yaml/badge.svg)](https://github.com/calypr/syfon/actions/workflows/ci.yaml)
[![Client Coverage](https://codecov.io/gh/calypr/syfon/branch/development/graph/badge.svg?flag=client)](https://app.codecov.io/gh/calypr/syfon/tree/development)
[![dependabot](https://img.shields.io/badge/dependabot-enabled-025E8C?logo=dependabot)](https://github.com/calypr/syfon/security/dependabot)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](../LICENSE)
[![Latest Release](https://img.shields.io/github/v/release/calypr/syfon)](https://github.com/calypr/syfon/releases)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](../CONTRIBUTING.md)
[![GitHub Stars](https://img.shields.io/github/stars/calypr/syfon?style=social)](https://github.com/calypr/syfon/stargazers)

This module provides a reusable Go client for Syfon APIs.

- Module: `github.com/calypr/syfon/client`
- Pattern: Vault-style grouped services (`Client.Data()`, `Client.Index()`, `Client.Buckets()`, etc.)
- Current surface:
  - Health: `/healthz`
  - Data: `/data/upload`, `/data/upload/{file_id}`, `/data/upload/bulk`, `/data/download/{file_id}`, multipart endpoints
  - Index: `/index`, `/index/{id}`, bulk endpoints
  - Buckets: `/data/buckets`, `/data/buckets/{bucket}`, `/data/buckets/{bucket}/scopes`
  - Core: `/index/v1/sha256/validity`
  - Metrics: `/index/v1/metrics/*`

## Usage

```go
package main

import (
  "context"
  "errors"
  "log"

  "github.com/calypr/syfon/apigen/bucketapi"
  "github.com/calypr/syfon/apigen/errorapi"
  "github.com/calypr/syfon/client/apierror"
  syclient "github.com/calypr/syfon/client"
)

func main() {
  c, err := syclient.New(
    "http://127.0.0.1:8080",
    syclient.WithBasicAuth("user", "pass"),
  )
  if err != nil {
    log.Fatal(err)
  }

  provider := "s3"
  region := "us-east-1"
  accessKey := "..."
  secretKey := "..."

  err = c.Buckets().Put(context.Background(), bucketapi.PutBucketRequest{
    Bucket:       "cbds",
    Provider:     &provider,
    Region:       &region,
    AccessKey:    &accessKey,
    SecretKey:    &secretKey,
    Organization: "syfon",
    ProjectId:    "e2e",
  })
  if err != nil {
    log.Fatal(err)
  }

  if err := c.Health().Ping(context.Background()); err != nil {
    var apiErr *apierror.APIError
    if errors.As(err, &apiErr) {
      log.Printf("Syfon request %s failed with %s (%d), request ID %s", apiErr.URL, apiErr.Code, apiErr.Status, apiErr.RequestID)
    }
    if errors.Is(err, errorapi.ErrUnavailable) {
      log.Printf("Syfon is temporarily unavailable")
    }
    log.Fatal(err)
  }
}
```

After it successfully reads an HTTP response body, Syfon maps responses with
status 400 or greater to `*apierror.APIError`. The error preserves the HTTP
status, exact machine-readable code, broad error category, public message,
request ID, request method and URL, response headers, and response body. A
response-body read failure remains an ordinary wrapped error. Use `errors.Is`
with a broad sentinel for portable control flow:

```go
record, err := c.DRS().GetObject(context.Background(), "object-id")
if errors.Is(err, errorapi.ErrNotFound) {
  // The object does not exist or is not visible to this caller.
}
```

`APIError.Code` has the exported `errorapi.ErrorCode` type and preserves
unknown server-defined codes. Exact sentinels work with `errors.Is`, while the
broad sentinel remains available for fallback handling:

```go
switch {
case errors.Is(err, errorapi.ErrObjectChecksumImmutable):
  // Do not retry this update with a different checksum.
case errors.Is(err, errorapi.ErrConflict):
  // Handle another conflict reason.
}
```

Code comparisons such as
`apiErr.Code == errorapi.ErrorCodeObjectChecksumImmutable` are also stable.
`APIError.Category` has the exported `errorapi.ErrorCategory` type. The
exported broad sentinels are `ErrNotFound`, `ErrUnauthorized`, `ErrForbidden`,
`ErrConflict`, `ErrInvalidInput`, `ErrRateLimited`, and `ErrUnavailable`.

## Error contract

Import error definitions from `github.com/calypr/syfon/apigen/errorapi`.
Use an exact sentinel when the operation needs one reason:

```go
if errors.Is(err, errorapi.ErrObjectNotFound) {
	// The requested object is missing.
}
```

Use `errorapi.ErrNotFound` when any not-found reason is enough. An exact
not-found error also matches the broad sentinel. The broad sentinel does not
identify a particular exact reason. The old error constants and type aliases
from `client/apierror` were removed. `client/services.ErrObjectNotFound` is
the deprecated compatibility alias; use `errorapi.ErrNotFound` to preserve its
broad matching behavior. The `client/apierror` package owns the
`APIError` response type, while `apigen/errorapi` owns sentinels and codes.

The client returns ordinary wrapped errors for request construction, transport
failure, response-body reads, and malformed JSON in a successful response. A
malformed non-2xx response, after its body is read, still returns
`*apierror.APIError`. The parser uses the HTTP status when the response body is
empty or is not valid JSON. For a valid payload, a known exact code from the
top-level or nested `error` object wins over numeric code or status fields, and
its category is derived from that code. An unknown code string is kept in
`APIError.Code` for forward compatibility. The parsed message and request ID
are preserved.

The server accepts `X-Request-Id`. If the header is empty, the server creates a
request ID. The server includes that ID in the error response and logs it with
the internal error. Error responses use public-safe status text for 5xx
messages. Inspect `APIError.Body` or server logs only in a controlled
diagnostic context, because those values can contain provider or database
details. When both the response header and JSON body contain a request ID, the
client uses the response header.

## LFS and multipart behavior

`LFSService.Batch` and `LFSService.StageMetadata` require their generated 200
response payloads. A missing success payload is returned as a shared
`*apierror.APIError`. LFS batch object errors remain in the batch response and
must be checked per object. `LFSService.Verify` accepts only HTTP 200.

The request layer retries only bodyless `GET`, `HEAD`, and `OPTIONS` calls by
default. Multipart writes are not retried by that layer. The multipart engine
persists a checkpoint after initialization and after each successful part, then
removes it after successful completion. A failed completion leaves the
checkpoint for a later resume. A repeated completion that returns not found is
an error; the client does not infer success from a prior attempt. Server-side
multipart sessions are process-memory state, so a server restart loses the
session lookup. This is separate from the client checkpoint, which can support
resuming a transfer after a client restart.
