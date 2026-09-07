# ADR: Accept Anonymous Inline Fields from oapi-codegen for DRS Access Methods

## Status
Proposed

## Date
2026-04-16

## Decision Owners
Syfon maintainers

## Context
The DRS OpenAPI source includes reusable schema components such as `AccessURL` and `Authorizations`, but the `AccessMethod` schema still defines `access_url` and `authorizations` as inline object properties in the official specification.

When `oapi-codegen` generates Go from that schema, it emits:

- named component types for `AccessURL` and `Authorizations`, and
- anonymous inline field types for `AccessMethod.AccessUrl` and `AccessMethod.Authorizations`.

That means downstream Go code cannot cleanly use the named types as field values without a schema change or a generator fork. The result is a type-shape mismatch between the reusable models and the actual generated field types.

## Problem
We want a client architecture that is:

- generated where possible,
- maintainable in Go,
- and not dependent on the old `apitypes` shim.

However, trying to force named field types through the overlay/codegen layer has not worked cleanly for this specific schema shape. The generated code still prefers anonymous inline structs for the `AccessMethod` nested fields.

Example of the mismatch:

```go
am := drsapi.AccessMethod{
    AccessUrl: &drsapi.AccessURL{Url: "s3://bucket/path"},
}
```

This is not assignable to the generated `AccessMethod.AccessUrl` field because the field is an anonymous inline struct, not `*drsapi.AccessURL`.

## Decision
For now, downstream client code will use the anonymous generated field types directly.

In practice this means:

- keep the official DRS schema unchanged,
- keep the generated client/server packages,
- stop trying to coerce `AccessMethod` nested fields into named aliases,
- and adapt client wrappers to the generated anonymous shapes.

## Consequences
### Positive
- No change to the official OpenAPI source.
- No custom generator fork.
- No additional Makefile or bundling complexity.
- The client can move forward using the actual generated shape instead of fighting it.

### Negative
- Downstream code is less ergonomic.
- Builders and conversion helpers are a little uglier.
- The client layer remains coupled to the generator’s exact output shape for these fields.

## Mitigations
- Keep the anonymous-field usage contained to the client/DRS boundary.
- Avoid leaking those shapes throughout the rest of the codebase.
- Revisit this only if the official schema changes or if a generator patch becomes worth maintaining.

## Notes
This decision is specific to `AccessMethod.access_url` and `AccessMethod.authorizations`.
It does not imply the rest of the generated API needs to remain awkward; only this inline-field shape is being accepted as-is.

