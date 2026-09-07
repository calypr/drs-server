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
  "log"

  syclient "github.com/calypr/syfon/client"
)

func main() {
  c := syclient.New(
    "http://127.0.0.1:8080",
    syclient.WithBasicAuth("user", "pass"),
  )

  err := c.Buckets().Put(context.Background(), syclient.PutBucketRequest{
    Bucket:       "cbds",
    Provider:     "s3",
    Region:       "us-east-1",
    AccessKey:    "...",
    SecretKey:    "...",
    Organization: "syfon",
    ProjectID:    "e2e",
  })
  if err != nil {
    log.Fatal(err)
  }

  if err := c.Health().Ping(context.Background()); err != nil {
    log.Fatal(err)
  }
}
```
