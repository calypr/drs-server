# ADR: OpenAPI Generation vs Middleware Enforcement Boundaries

## Status
Proposed

## Date
2026-04-14

## Decision Owners
Syfon maintainers

## Context
There is active discussion about combining:

1. OpenAPI-driven generated code (`apigen` and potential generator changes such as `oapi-codegen`), and
2. Runtime middleware concerns (authn/authz, request/response validation, standardized error behavior).

These are related but different layers. Blurring them causes implementation confusion, especially around who is responsible for returning `400` vs `401/403` vs `500`.

## Decision
Adopt a layered contract:

1. **Generation layer (OpenAPI -> code)** defines API shapes and typed plumbing.
2. **Middleware layer** enforces cross-cutting runtime policy before/after handlers.
3. **Service/domain layer** enforces business rules and backend-specific invariants.

Generator choice (`openapi-generator` vs `oapi-codegen`) does not replace middleware or service responsibilities. It changes how strongly typed and ergonomic the generated API boundary is.

## Layer Responsibilities

### 1) Generation layer (`apigen`, generated routers/types/interfaces)
In scope:
- Typed request/response models from OpenAPI.
- Route scaffolding and handler interfaces.
- Basic schema-driven validation hooks where supported by the generator/runtime.

Out of scope:
- Authorization policy decisions.
- Environment-specific RBAC behavior.
- Business state validation (DB/object/workflow-dependent).
- Operational logging/metrics policy.

### 2) Middleware layer (request pipeline)
In scope:
- Authentication and authorization checks.
- Request precondition checks (headers/content type/body size/basic format constraints).
- Optional response contract validation.
- Consistent error envelope/telemetry for cross-cutting failures.

Out of scope:
- Object- and workflow-specific domain logic that requires service/database context.

### 3) Service/domain layer (`service/*`, domain handlers)
In scope:
- Business invariants and workflow transitions.
- Backend capability checks (storage/database/provider conditions).
- Domain errors that should map to 4xx/5xx after domain evaluation.

Out of scope:
- Generic authn/authz entry checks that should be centralized.

## Error Ownership Matrix

| Status Class | Primary Owner | Typical Causes |
|---|---|---|
| `400` / `415` / `422` | Generation + Middleware | malformed payload, unsupported content type, schema/request precondition failure |
| `401` / `403` | Middleware | missing/invalid identity, insufficient permissions |
| `404` (routing) | Router/Generation layer | unknown path/method |
| `404` (domain) | Service layer | object/resource not found after valid route/auth |
| `409` / `412` | Service layer | domain conflicts/precondition failures |
| `500` | Middleware or Service | unexpected runtime or backend failure |

Notes:
- A generator may emit some `400` behavior, but production consistency still requires middleware + shared error mapping.
- Response validation in enforce mode may intentionally convert contract violations to `500`.

## RBAC Customization Model
RBAC is expected to vary per deployment. Therefore:

- Middleware should define **extension points** (policy resolver/authorizer interfaces).
- Route-to-policy mapping belongs in server configuration or policy registry, not generated OpenAPI models.
- OpenAPI may describe security schemes and required auth presence, but **authorization semantics remain runtime-configurable**.

This preserves portability: generated API contracts stay stable while RBAC strategy changes by environment.

## Request Lifecycle (Target)

1. Route resolution and typed decode (generation/router layer).
2. Middleware authn/authz checks (`401/403`).
3. Middleware request validation/preconditions (`400/415/422`).
4. Handler/service business logic (`2xx/4xx/5xx` domain outcomes).
5. Optional middleware response validation (audit/enforce policy).
6. Standardized error/log/metric emission.

## Consequences

### Positive
- Clear ownership boundaries reduce duplicated checks and regressions.
- Teams can migrate generator tooling independently from authz policy.
- Consistent client-facing error behavior despite backend or RBAC differences.

### Tradeoffs
- Requires discipline to avoid reintroducing handler-level duplicate validations.
- Needs policy registry maintenance as routes evolve.
- Some validation may exist in both generated plumbing and middleware; overlap must be intentional and documented.

## Rollout Plan

1. Document current route/error ownership and identify duplicated checks.
2. Introduce/standardize middleware interfaces for authz and request validation.
3. Keep generated types/scaffolding as contract source of truth.
4. Incrementally remove duplicate handler prechecks once middleware parity tests pass.
5. If evaluating `oapi-codegen`, run as a tooling migration track, not a policy migration.

## Non-Goals
- This ADR does not select a final generator today.
- This ADR does not define one universal RBAC model.
- This ADR does not remove service-layer business validation.

## Summary
Generated OpenAPI code and middleware are complementary:

- **Generation** gives typed API contracts.
- **Middleware** enforces cross-cutting runtime guarantees.
- **Service layer** owns business correctness.

They should be composed, not conflated.
