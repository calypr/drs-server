# ADR: One-Time Program Bootstrap Claim Without User-Held Admin Credentials

- Status: Proposed
- Date: 2026-04-06
- Decision owners: Syfon + Requestor + Gen3 platform maintainers
- Related systems: Syfon, Requestor, Fence, Arborist

## Context

We need a user to prove control of an external bucket and become the initial owner of a new Gen3 program namespace when that namespace does not yet exist.

The core problem is not normal RBAC. The core problem is **one-time, high-impact authorization mutation**:

- User is authenticated but not yet authorized on `/programs/<new>`.
- System must allow exactly one initial ownership grant for that namespace.
- This action must not rely on a reusable admin token in user-controlled space (browser/CLI logs/network traces).

Current components are strong but incomplete for this exact step:

- Arborist is a policy engine and exposes primitives (`create resource`, `create policy`, `grant policy to user`), but does not implement one-time claim workflows.
- Requestor is a request workflow service for existing resources, but does not currently implement single-use bootstrap claim semantics.
- Syfon can validate bucket state/credentials and store bucket scopes, but does not currently provide one-time claim orchestration.
- Fence issues user/client tokens, but token issuance alone does not encode “single use claim for namespace bootstrap.”

## Problem statement (crux)

If bootstrap is implemented using a reusable elevated token (or equivalent bearer secret exposed to user space), compromise of that secret can escalate to platform-wide namespace abuse.

Therefore, we need a design where:

1. No admin-equivalent credential is ever delivered to end users.
2. Bucket validation can be turned into a bootstrap permission exactly once.
3. Replay of proof material does not yield repeated ownership grants.

## Decision

Adopt a **single-use claim record** model:

- Syfon validates bucket control and issues a server-side persisted claim record (`claim_id`), not an admin token.
- Requestor redeems that claim exactly once through server-to-server verification.
- Requestor performs Arborist mutations using a narrowly scoped service identity.
- Claim redemption is atomic and irreversible (`NEW -> REDEEMED`), with TTL and requester binding.

This keeps powerful credentials inside trusted services and turns bucket proof into a one-time capability.

## Detailed decision

### Security invariants

1. User only holds normal Fence user token.
2. User never receives Requestor service credentials or Arborist admin-like credentials.
3. Claim is bound to:
   - requesting username
   - bucket identity
   - target program slug (or deterministic mapping)
   - expiry time
4. Claim can be redeemed once, enforced by DB transaction + unique constraints.
5. Requestor service identity is least-privileged to only create/grant approved bootstrap artifacts.

### Control flow

1. User authenticates via Fence and calls Syfon bucket validation path.
2. Syfon validates bucket control and creates `bucket_claim` row:
   - `claim_id`, `username`, `bucket`, `program_slug`, `nonce`, `status=NEW`, `expires_at`.
3. User calls Requestor bootstrap endpoint with `claim_id` and desired slug.
4. Requestor validates user token and calls Syfon internal verify endpoint using service-to-service auth.
5. Syfon returns claim details if valid and `status=NEW`.
6. Requestor checks binding (username/slug), acquires atomic lock/transaction, and redeems claim.
7. Requestor executes Arborist primitives:
   - create `/programs/<slug>` resource path
   - create owner policy from fixed template
   - grant policy to user
8. Requestor stores audit record and returns success.

If redemption fails mid-flight, claim moves to terminal failed state or remains safely retryable with idempotency key (implementation choice), but never allows unbounded replay.

## Why this decision

- Solves the exact gap: one-time approval semantics.
- Avoids introducing user-visible admin secrets.
- Uses existing Arborist capability set without forking.
- Keeps responsibilities clean:
  - Syfon = external proof authority
  - Requestor = workflow + one-time claim control
  - Arborist = policy decision and storage
  - Fence = identity/provider of service credentials

## Alternatives considered

1. **Give user an elevated token after bucket proof**
   - Rejected: unacceptable credential theft blast radius.

2. **Put all bootstrap logic directly into Arborist**
   - Rejected: Arborist is policy engine, not external proof + one-time workflow orchestrator.

3. **Encode dynamic one-time behavior in `user.yaml`**
   - Rejected: static config cannot represent runtime single-use transitions safely.

4. **Manual admin approval through Requestor ticket**
   - Rejected for this use case: does not satisfy self-service bootstrap requirement.

## Comparison to GitHub’s model (relevance)

GitHub generally avoids exposing high-privilege backend credentials to end users for sensitive account/repo/org mutations. Instead, they use:

- user-authenticated intent,
- backend-issued short-lived/limited capabilities,
- server-side state transitions and audit logs,
- re-verification/challenges for high-risk actions,
- and strict separation between user tokens and privileged internal service actions.

This ADR applies the same principle: **end user proves identity and ownership intent; backend performs privileged mutation through controlled, audited, least-privilege service identities.**

## Operational notes

- Add comprehensive audit events in both Syfon and Requestor.
- Add replay detection metrics and alerting.
- Enforce short claim TTL (for example 5 minutes).
- Use deterministic slug normalization + reserved names policy.
- Add explicit ownership transfer procedure outside bootstrap path.

## Consequences

### Positive

- Strong compromise resistance versus token-scrape scenarios.
- Clear, explainable trust boundaries for security review.
- Minimal required changes to Arborist itself.

### Negative / tradeoffs

- More orchestration complexity across Syfon and Requestor.
- Need schema/migration work for claim lifecycle state.
- Requires careful idempotency and failure-state design.

## Implementation checkpoint criteria

1. No endpoint returns privileged service credentials to user.
2. Claim cannot be redeemed more than once under concurrency.
3. Replay of same `claim_id` fails deterministically.
4. Bootstrap creates exactly expected Arborist artifacts and nothing broader.
5. Security logs can reconstruct who claimed what, when, and through which proof.
