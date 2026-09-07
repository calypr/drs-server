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

All Syfon API failures return `*apierror.APIError`. The error preserves the
HTTP status, exact machine-readable code, broad error category, public message,
request ID, request method and URL, response headers, and response body. Use
`errors.Is` with a broad sentinel for portable control flow:

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
