# Quick Start

This guide starts Syfon with SQLite, local Basic Auth, and an S3-compatible object store. For the full config reference, see [Server Configuration](configuration.md).

## Prerequisites

Install Go and Docker. Initialize the DRS schema submodule, then build the server:

```bash
git submodule update --init --recursive
make build
```

Start MinIO with the credentials used by the config:

```bash
docker run --name minio --rm -p 9000:9000 \
  -e MINIO_ROOT_USER=minio-user \
  -e MINIO_ROOT_PASSWORD=minio-pass \
  quay.io/minio/minio server /data
```

In a second terminal, create `local-bucket` before you start Syfon:

```bash
docker run --rm --network container:minio --entrypoint sh minio/mc -c \
  'mc alias set local http://127.0.0.1:9000 minio-user minio-pass && \
   mc mb -p local/local-bucket'
```

The examples use `localhost:9000`, the bucket `local-bucket`, and the credentials `minio-user` and `minio-pass`. Keep the MinIO terminal running while you use Syfon.

## Create a config file

Save this as `config.local.yaml`:

```yaml
port: 8080

auth:
  mode: local
  basic:
    username: drs-user
    password: drs-pass

database:
  sqlite:
    file: ./data/drs.db

credential_encryption:
  local_key_file: ./data/.syfon-credential-kek

buckets:
  - bucket: local-bucket
    provider: s3
    region: us-east-1
    endpoint: http://localhost:9000
    access_key: minio-user
    secret_key: minio-pass
    resources:
      - organization: example
        projects:
          - project_id: example
```

Create the directories named by the config before starting the server:

```bash
mkdir -p data
```

## Start Syfon

```bash
bin/syfon serve --config config.local.yaml
```

In another terminal, check the health endpoint:

```bash
curl -u drs-user:drs-pass http://localhost:8080/healthz
```

## Use the CLI

Set the server and local credentials for CLI requests:

```bash
export SYFON_SERVER_URL=http://localhost:8080
export SYFON_USERNAME=drs-user
export SYFON_PASSWORD=drs-pass
```

Upload and list a file:

```bash
bin/syfon upload --file ./README.md --org example --project example
bin/syfon ls --organization example --project example
```

Download a record by DID from the `ls` output:

```bash
bin/syfon download --did <did> --out /tmp/README.md
```

The upload command derives a DID from the file checksum and project scope when you omit `--did`. The `--project` flag is required in that case.

## Read next

- [Local Deployment](local-deployment.md) covers local storage and Docker runs.
- [Kubernetes Deployment](kubernetes-deployment.md) covers the Gen3 chart.
- [Server Configuration](configuration.md) documents every config field.
- [Troubleshooting](troubleshooting.md) covers startup, storage, auth, and database errors.
