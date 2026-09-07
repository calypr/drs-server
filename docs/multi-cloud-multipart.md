# Multi-Cloud Multipart Downloads (Server-Side)

This document explains the architecture and rationale for the server-side multipart download support in Syfon.

## Abstract

To support high-performance parallel downloads across multiple cloud providers (AWS, GCP, Azure), Syfon implements a **Range-Based Part Signing** mechanism. This ensures cryptographic compatibility with modern signing protocols (like GCS V4) while removing the complexity of local cloud credentials from the end-user.

## The Problem: "Protocol Fragmentation"

Historically, parallel downloads were achieved by obtaining a single signed GET URL for an entire object and then appending standard HTTP `Range` headers to sub-requests.

While this works for AWS S3, it fails for:
1. **GCS V4**: Google Cloud's modern signing protocol requires the `Range` header to be part of the canonical request during the signing process. If a client attempts to add a `Range` header to a URL that wasn't explicitly signed for it, Google rejects the request.
2. **Azure**: Similar restrictions apply to Shared Access Signatures (SAS) which can be scoped to specific operations.

## The Solution: Explicit Part Signing

Syfon introduces the `/data/download/{file_id}/part` endpoint. Instead of the client "guessing" how to sign a range, it explicitly asks the server:

> "I need a signed URL for bytes 1000 to 2000 of file X."

### Architectural Changes

1. **Modular Backends**: The `urlmanager` package was refactored from a monolithic file into provider-specific handlers (`s3.go`, `gcs.go`, `azure.go`). 
2. **Cryptographic Binding**: Each backend now implements a specialized `GetDownloadPartURL` method that satisfies the specific signature requirements of that cloud provider (e.g., adding the range to the V4 canonical request for GCS).
3. **Internal Data API**: A new internal route `/data/download/{file_id}/part` was added to propagate these requests securely through the DRS resolution layer.

## Security and Compliance

- **No Local Keys**: Users no longer need AWS/GCP/Azure keys on their local machines. The Syfon server (which holds the service account) performs the signing.
- **Principle of Least Privilege**: Signed URLs returned via the `/part` endpoint are cryptographically restricted to a specific byte range, preventing unauthorized access to the rest of the object.
- **Auditing**: Every range request is a first-class API call to Syfon, allowing for granular tracking of data egress.

## Implementation Details

- **GCS**: Uses the GCS Go SDK to generate V4 signed URLs with the `Range` header pre-negotiated.
- **S3**: Continues to support standard range headers but provides a consistent API for the client.
- **Azure**: Generates SAS tokens with range-specific permissions.
