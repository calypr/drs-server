# Quick Start

This is the fastest way to get a local Syfon server running.

For the raw config schema, see [Server Configuration](configuration.md). For a fuller local setup, see [Local Deployment](local-deployment.md).

## Prerequisites

- Go 1.24+
- SQLite3 (`sqlite3`)
- Git

## 1. Clone and enter the repo

```bash
git clone <your-repo-url>
cd syfon
```

## 2. Create a minimal local config

```yaml
port: 8080

auth:
  mode: local
  basic:
    username: drs-user
    password: drs-pass

database:
  sqlite:
    file: ./drs_local.db

buckets:
  - bucket: local-bucket
    provider: s3
    region: us-east-1
    endpoint: http://localhost:9000
    access_key: minio-user
    secret_key: minio-pass
```

## 3. Start the server

```bash
go run . serve --config config.local.yaml
```

## 4. Smoke test

```bash
curl -u drs-user:drs-pass http://localhost:8080/healthz
```

## What To Read Next

- [Local Deployment](local-deployment.md) for a practical SQLite plus local-auth setup
- [Kubernetes Deployment](kubernetes-deployment.md) for the Helm chart
- [Server Configuration](configuration.md) when you need field-by-field config details
