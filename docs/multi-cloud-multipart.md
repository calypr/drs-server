# Ranged downloads across storage providers

Syfon can issue a signed URL for one inclusive byte range of an object. The client uses that endpoint for parallel downloads when the object and provider support ranged reads.

## Request a signed range

Call the internal endpoint with the object ID and inclusive `start` and `end` query parameters:

```http
GET /data/download/{file_id}/part?start=1000&end=1999
```

The server resolves the object, checks the caller's access, and returns a signed URL for that range. The client sends the provider's required range request to the returned URL. Syfon records the range in its transfer event when it issues the URL.

## Provider behavior

The provider adapter creates the signature. S3 signs a `Range` header, GCS includes the range header in its V4 signed request, and Azure includes the range in its SAS-backed request. The local file provider reads the requested range without a remote signature.

## Client behavior

The client checks object metadata before selecting a transfer strategy. It uses a single stream for small objects or sources without range support. For larger ranged objects, it requests ranges and writes them into the destination concurrently. If a source ignores a range request and returns the full body, the client reports that condition and can fall back to a single stream.

The endpoint is an internal Syfon contract. Clients should use the client transfer package or the documented server API rather than constructing provider-specific URLs themselves.
