# ADR: Big-Bang Migration to `oapi-codegen` + Gin Runtime

## Status
Accepted

## Date
2026-04-14

## Context
The server previously used:

- `openapi-generator` (`go-server`) generated controller/router contract (`ImplResponse`, mux route tables)
- `gorilla/mux` as the primary HTTP runtime

This created an architecture split between generated contract wiring and runtime middleware strategy, and made strict request/response contract enforcement harder to standardize.

## Decision
Adopt a big-bang migration for the server runtime and DRS contract wiring:

1. `oapi-codegen` is the canonical DRS server generator.
2. Gin is the only HTTP runtime entrypoint for `cmd/server`.
3. DRS routes are registered using `oapi-codegen` strict gin handlers.
4. Existing service-domain logic remains authoritative and is exposed through a service-owned `oapi-codegen` gin server implementation.
5. Validation policy:
   - request validation: enforce
   - response validation: audit in production, enforce in CI/pre-prod

## Scope
The runtime cutover covers all exposed server route groups:

- GA4GH DRS routes
- Internal compatibility routes
- LFS routes
- Metrics routes
- Swagger/OpenAPI docs
- `healthz`

## Ownership Matrix
| Layer | Responsibility |
|---|---|
| Generated (`oapi-codegen`) | shape decoding/encoding, strict handler interfaces |
| Middleware | authn/authz, request/response contract policy, common error/logging behavior |
| Service/domain | business invariants, storage/database semantics, domain-specific error decisions |

## Breaking Change Policy
This migration is intentionally breaking:

- Prior `openapi-generator` controller/router contract is retired as the canonical server interface.
- No compatibility guarantee is provided for previous generated server integration surfaces.
- `apigen` package/type churn is permitted where required by migration.

## Consequences
Positive:

- One runtime model (gin) for all endpoint groups.
- One generated server interface model (`oapi-codegen` strict handlers) for DRS routes.
- Cleaner boundary between API contract layer and domain layer.

Tradeoffs:

- Transitional complexity while existing service signatures continue to evolve toward generated interfaces.
- Generated model differences may require incremental cleanup in adjacent packages.
