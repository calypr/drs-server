# ADR: Internal DRS Delta (Non-LFS Path)

## Status
Proposed (for internal review and standards pitch)

## Date
2026-03-19

## Spec Baseline
This ADR is based on the GA4GH DRS schema work from:
- Upstream repository: [ga4gh/data-repository-service-schemas](https://github.com/ga4gh/data-repository-service-schemas)
- Upload/bulk-focused branch used for comparison: [feature/issue-416-drs-upload](https://github.com/ga4gh/data-repository-service-schemas/tree/feature/issue-416-drs-upload)


## Scope
This document describes the delta between:
1. Official GA4GH DRS spec capabilities, and
2. The internal API capabilities required for our production workflow.

Git LFS is intentionally out of scope for this ADR. LFS remains an optional compatibility interface for users who want stock `git lfs` commands, but it is not the architectural baseline described here.

---

## Problem Statement
Our primary workflow requires:
- Multipart upload and download for very large objects.
- Resumable transfer semantics (upload and download).
- Bulk record validity checks by SHA256.
- Rich object registration with policy-aware metadata.
- Strong authz enforcement and auditable failure modes.

Current DRS spec has improved bulk and registration capabilities, but does not yet provide all transfer-lifecycle semantics we require.

---

## What Official DRS Already Covers (and we should use directly)
These are not "gaps" and should stay on official endpoints:
- Bulk object registration (`/objects/register`).
- Bulk object retrieval (`/objects`).
- Bulk delete (`/objects/delete`).
- Bulk access-method updates (`/objects/access-methods`).
- Bulk checksum add (`/objects/checksums`) where implemented.
- Upload-request style handoff (`/upload-request`) where implemented.

Implementation note:
- API-level bulk support must be backed by DB-level bulk execution (single-query/set-based patterns), not per-object loops.

---

## Required Internal Delta (Not Adequately Covered by Current DRS)

### 1) Multipart Upload Session Lifecycle
Needed behavior:
- Explicit `init -> part upload -> complete/abort` state machine.
- Idempotent part commits.
- Resume from partial state after interruption.
- Server-side validation of part manifests.

Why this is a gap:
- DRS registration/upload abstractions do not fully standardize multipart state management and resume semantics across providers.

### 2) Multipart/Resumable Download Semantics
Needed behavior:
- Reliable range-based download strategy for large objects.
- Resume token/state for interrupted downloads.
- Optional server-side advisory for chunk sizing/concurrency.

Why this is a gap:
- DRS access methods expose where/how to access data, but do not fully define resumable download lifecycle and consistency semantics.

### 3) Bulk SHA256 Validity Endpoint
Needed behavior:
- Input: list of SHA256 values.
- Output: map of `sha256 -> valid:boolean`.
- Valid means: object record exists, references a registered bucket, and has valid S3 path syntax (object byte existence check is intentionally out of scope).

Why this is a gap:
- DRS supports lookup/access patterns, but does not define this exact high-throughput validity contract required by our push pipeline.

### 4) Registration Preflight/Validation Contract
Needed behavior:
- Validate candidate metadata/authz/storage target before durable registration.
- Return deterministic, per-candidate validation errors.

Why this is a gap:
- DRS registration exists, but explicit preflight contract for policy+storage validation without commit is not consistently standardized.

### 5) Rich Registration Envelope Around Base DRS Object
Needed behavior:
- Preserve `DrsObject` schema, but allow additional operational metadata needed for policy and workflow integrity.
- Keep this extension explicit and versioned.

Why this is a gap:
- Forcing all operational concerns into base `DrsObject` either loses fidelity or causes incompatible field pressure.

---

## Conformance Risk If We Remove Internal Delta Too Early
If we hard-conform to official DRS today without these internal capabilities:
- Large-file durability regresses (multipart/resume behavior weakens).
- Throughput regresses in high-volume push/pull workflows.
- Validation fidelity regresses (more client-side guesswork).
- Operational troubleshooting degrades under partial failures.

These are functional regressions, not cosmetic differences.

---

## Target Architecture
- Official DRS endpoints are canonical wherever they provide required behavior.
- Internal endpoints exist only for features not yet standardized in DRS.
- Internal capabilities are designed as candidate DRS extensions, not permanent private forks.

Goal:
- Minimize internal superset over time,
- Without losing transfer durability, resumability, or validation guarantees.

---

## Proposal to Upstream / Spec Evolution
Propose the following additions/clarifications to DRS:
1. Standard multipart upload session model (init/part/complete/abort/resume).
2. Standard resumable download guidance/contract (range and resume semantics).
3. Standard bulk SHA validity endpoint contract.
4. Standard registration preflight endpoint.
5. Extension envelope guidance for operational metadata around `DrsObject`.

---

## Migration Guidance
1. Keep using official DRS bulk endpoints in all new code where supported.
2. Keep internal transfer/validity endpoints as temporary scaffolding.
3. Maintain a parity matrix: `internal feature -> official DRS equivalent`.
4. Remove internal endpoints only after parity is validated by e2e tests for large-file resumable workflows.

---

## Appendix: Endpoint Parity Matrix

| Feature | Official DRS Endpoint(s) | Current Internal Endpoint(s) | Current Decision | Retirement Criteria |
|---|---|---|---|---|
| Bulk object registration | `POST /ga4gh/drs/v1/objects/register` | N/A (for core path) | Use official DRS now | Already canonical |
| Bulk object retrieval | `POST /ga4gh/drs/v1/objects` | `POST /index/bulk/documents` (compat) | Use official DRS for new code | Remove compat endpoint after no callers remain and e2e passes |
| Bulk delete | `POST /ga4gh/drs/v1/objects/delete` | Legacy delete flows in compat paths | Use official DRS for new code | Remove legacy delete paths after parity + migration window |
| Bulk access-method updates | `POST /ga4gh/drs/v1/objects/access-methods` | Legacy update flows in compat paths | Use official DRS for new code | Remove compat update paths after parity tests |
| Bulk checksum addition | `POST /ga4gh/drs/v1/objects/checksums` | N/A | Implement official endpoint fully | Endpoint returns non-`501` and passes integration tests |
| Bulk SHA validity (`sha -> bool`) | No direct equivalent today | `POST /index/bulk/sha256/validity` | Keep internal | Propose/land DRS bulk validity contract; migrate callers |
| Multipart upload init | No direct lifecycle equivalent today | `POST /data/multipart/init` | Keep internal | Official DRS multipart session model exists and is adopted |
| Multipart upload part URL / part commit | No direct lifecycle equivalent today | `POST /data/multipart/upload` | Keep internal | Official DRS multipart part lifecycle exists and is adopted |
| Multipart upload complete | No direct lifecycle equivalent today | `POST /data/multipart/complete` | Keep internal | Official DRS multipart completion semantics adopted + e2e parity |
| Multipart/resumable download control | Partial via access methods only | Internal range/resume behavior (service + client logic) | Keep internal behavior | Official DRS resumable download semantics available + parity tests |
| Registration preflight (validate without commit) | No consistent dedicated endpoint today | Internal validation path(s) in registration workflow | Keep internal | Dedicated DRS preflight endpoint standardized and implemented |
| Rich registration envelope around `DrsObject` | Base `DrsObject` + candidates only | Internal model wrappers/fields | Keep internal wrapper | Standard extension envelope guidance adopted and implemented |

Notes:
- “Official DRS Endpoint(s)” are listed as implemented in our generated spec branch and server surface.
- Internal endpoints above are part of current deployed behavior and should be treated as temporary where a standards-equivalent path exists.

---

## Executive Summary
This is not "DRS vs internal API". It is "use DRS wherever complete, and fill current spec gaps for production-grade transfer durability and validation."  
LFS compatibility is optional and separate from this core architecture decision.
