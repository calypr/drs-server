# Troubleshooting

This page is for operators running Syfon, not for development workflow issues.

## Server Will Not Start

### `no database specified in config`

Syfon requires exactly one database backend.

Use one of:

- `database.sqlite.file` for local deployments
- `database.postgres.*` for Gen3 and normal Kubernetes deployments

### `multiple databases specified in config`

You configured both `database.sqlite` and `database.postgres`.

Remove one of them. Local deployments should normally keep SQLite. Gen3 and Helm-based deployments should normally keep PostgreSQL.

### `auth.mode is required` or `invalid auth.mode`

Set `auth.mode` to exactly one of:

- `local`
- `gen3`

### `auth.mode "gen3" requires postgres database`

`gen3` mode requires PostgreSQL in normal operation.

Fix:

- use PostgreSQL for Gen3 deployments
- or switch the deployment to `auth.mode: local` if this is a local operator setup

### `auth.mode "local" requires auth.basic.username/password`

The documented local path requires Basic Auth unless you explicitly set:

```yaml
auth:
  allow_unauthenticated: true
```

That bypass is for development only.

## Storage Configuration Problems

### `bucket credential not found`

Syfon could not find a configured bucket entry that matches the requested bucket.

Check:

- the `bucket` name in `buckets`
- whether you intended to use `buckets` or legacy `s3_credentials`
- whether the client is targeting the same bucket name you configured

### Upload URL is generated but upload fails

This usually means Syfon was able to sign the request, but the client could not reach the storage endpoint.

Check:

- `endpoint` is correct
- `http` vs `https` matches the object store
- the object store is reachable from the client network
- the bucket credentials are valid for that bucket

### Presigned upload or download points at the wrong path

Check your bucket routing rules:

- `resources` if you want organization/project-derived mappings
- `bucket_scopes` if you want exact explicit mappings

If you are using the Helm chart, prefer `config.buckets` and keep the routing rules inside that block.

## Credential Encryption Problems

### `encrypted credential found but master key is not configured`

Syfon found encrypted bucket credentials, but it cannot load the KEK required to decrypt them.

Check:

- `DRS_CREDENTIAL_LOCAL_KEY_FILE` points at the expected KEK file
- or `credential_encryption.local_key_file` points at the expected KEK file
- or `DRS_CREDENTIAL_MASTER_KEY` is set to the expected key material

### Bucket credentials worked before but fail after restart

This usually means the KEK changed or the KEK file was not persisted.

For local deployments:

- persist the KEK file alongside the SQLite DB
- do not rotate or replace it unless you are also re-encrypting stored credentials

## Authentication Problems

### `401 Unauthorized` in local mode

Check the Basic Auth credentials you configured in:

- `auth.basic.username`
- `auth.basic.password`

If you are using a reverse proxy, also confirm it is forwarding the `Authorization` header.

### `401` or `403` for every request in `gen3` mode

Check:

- `auth.fence_url` points at the correct public Fence endpoint
- PostgreSQL is configured and reachable
- the incoming token is actually being forwarded to Syfon

For local integration testing only, you can use Gen3 mock auth. That is a test path, not the normal operator path.

## Database Problems

### SQLite: `database is locked`

SQLite is appropriate for a single local Syfon instance, not a multi-replica deployment.

Fix:

- run a single local server process
- avoid sharing one SQLite file across multiple writers
- use PostgreSQL for multi-instance deployments

### PostgreSQL connection failures

Check:

- `DRS_DB_HOST`
- `DRS_DB_PORT`
- `DRS_DB_USER`
- `DRS_DB_PASSWORD`
- `DRS_DB_DATABASE`
- `DRS_DB_SSLMODE`

If you are using the Helm chart, those are injected from the app DB secret. Verify the secret contents first.

## Kubernetes Problems

### Pod starts but Syfon immediately exits

Check:

- the rendered config Secret mounted at `/etc/drs/config.yaml`
- the app DB secret providing `DRS_DB_*`
- whether `config.auth.mode` and the chosen database backend are compatible

### Init job fails

The chart's PostgreSQL init job creates the app role, creates the app database, and applies the schema.

Check:

- admin DB credentials
- app DB credentials
- PostgreSQL network reachability
- whether you intended to disable `postgres.initJob.enabled`

## Docs Problems

### A docs page does not match the real chart

For Kubernetes behavior, treat the chart as the source of truth:

- [gen3-helm `helm/syfon`](https://github.com/calypr/gen3-helm/tree/ohsu-develop/helm/syfon)
