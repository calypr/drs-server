# Syfon documentation

Syfon is a Go implementation of the [GA4GH Data Repository Service (DRS)](https://ga4gh.github.io/data-repository-service-schemas/). It stores object metadata and signs access URLs for supported storage providers.

Use the guides by task:

- [Quick Start](quickstart.md) starts a local server and checks its health endpoint.
- [Local Deployment](local-deployment.md) describes a SQLite and local-auth deployment.
- [Kubernetes Deployment](kubernetes-deployment.md) describes the Gen3 Helm chart values that matter to Syfon.
- [Server Configuration](configuration.md) lists the config file fields and environment overrides.
- [Credential Encryption](encryption.md) explains how Syfon protects stored bucket credentials.
- [Plugin integration](plugins.md) documents the authentication and authorization plugin contracts.
- [Troubleshooting](troubleshooting.md) maps common startup and request failures to checks.

Developers can also read the [OpenAPI code-generation guide](operator-guide-code-generation.md), [transfer metrics guide](access-grants-transfer-metrics.md), and [multipart download guide](multi-cloud-multipart.md).

Architecture decisions are grouped under the Architecture Decisions section in the site navigation. Generated API contracts are served at `/index/openapi.yaml` when the docs route is enabled.
