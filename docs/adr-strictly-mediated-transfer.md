# ADR: Strictly Mediated Data Transfer via Syfon Server

## Status
Accepted

## Context
Since its inception, Syfon has supported a "Dual-Path" transfer model for moving data between the client and cloud storage:
1.  **Native Path**: The client uses native cloud SDKs (AWS, GCP, Azure) to talk directly to buckets using its own credentials.
2.  **Mediated Path**: The client talks to the Syfon Server, which signs short-lived HTTPS URLs for specific operations (Download/Upload).

Maintaining both paths has led to significant architectural debt, including:
- **Dependency Bloat**: The client binary must include the full SDK surface for all supported clouds.
- **Logic Duplication**: Multipart upload orchestration, retry logic, and error handling are implemented twice (once in the native SDKs and once in the Syfon-mediated drivers).
- **Security Sprawl**: Users are often required to manage two sets of credentials (Syfon tokens and Cloud keys).

## Decision
We will mothball the "Native Path" in the Syfon Client. All data transfers will now be **Strictly Mediated** by the Syfon Server.

1.  **Client Simplification**: All native cloud providers (S3, GCS, Azure) will be removed from the `client/xfer/providers/` directory.
2.  **Standardized Handshake**: The client will exclusively use the Syfon Server APIs to obtain signed URLs for both single-stream and multipart operations.
3.  **Multipart via Mediation**: Large file transfers will be handled by the client's `transfer.Engine` by requesting individual part URLs from the server, then performing standard HTTP uploads to those endpoints.
4.  **SDK Removal**: Cloud-specific Go SDKs will be removed from the `client/go.mod` to reduce binary size and security surface area.

## Rationale
- **Single Source of Truth**: The Syfon Server's `internal/signer` and `internal/provider` packages are the authoritative gatekeepers for data access. Centralizing logic there ensures that audit logs and security policies are never bypassed.
- **Binary Efficiency**: A CLI tool shouldn't need several hundred megabytes of cloud SDK dependencies to perform authenticated transfers. Using standard HTTP for the data path is sufficient.
- **Architectural Purity**: This design aligns with the principle that Syfon *is* the storage abstraction. The fact that the underlying storage is S3 or GCS should be an implementation detail hidden from the client.

## Consequences
- **Pros**:
    - Massively reduced client complexity and maintenance overhead.
    - Improved security by eliminating the need for cloud credentials on the user's machine.
    - Smaller, faster CLI binaries.
- **Cons**:
    - The Syfon Server becomes a critical path for all data operations (S3 direct-access fallback is removed).
    - Slight performance overhead during the "Handshake" phase to sign URLs.
    - Loss of certain vendor-specific optimizations (e.g., S3 Transfer Acceleration) unless explicitly proxied or signed by the server.
