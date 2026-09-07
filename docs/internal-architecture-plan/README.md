# Internal architecture execution plan

Execution baseline: `development` at `45d8199696866aabbc86942cff9ecb2a1bba7f31`.

## Verdict

The current package layout hides several useful systems inside `internal/core` and spreads their data contracts through `internal/models`, `internal/common`, and `internal/db`. Split ownership into objects, buckets, storage, transfers, and usage. Put HTTP and SQL at the edges, move interfaces to their consumers, migrate every caller, and delete the four catch-all packages.

The package names raised in the audit resolve as follows:

- Delete `internal/core` after its object, bucket, storage, transfer, usage, and service-info callers move.
- Delete `internal/common` after each symbol moves to a named owner. Keep only small `faults` and `requestmeta` packages for proven cross-layer contracts.
- Delete `internal/models`. Use explicit domain values, including distinct physical `RecordID` and canonical `ContentID` types.
- Delete interface-only `internal/db`. Move SQL to `internal/persistence/sqlite` and `internal/persistence/postgres`; define ports beside consumers.
- Split `internal/api/internaldrs` by workflow under `internal/httpapi`. A rename to `internalapi` would preserve the same problem.
- Merge `internal/auth` and `internal/authz` into `internal/access`, with authentication integrations in `internal/access/authentication` and Fiber wiring in `internal/httpapi/middleware`.
- Move signing and multipart provider code from `urlmanager` and `signer` into `internal/storage` and its provider children.
- Move `internal/repair` to `internal/maintenance/scoperepair`. It coordinates object, bucket, and storage operations, so it belongs to none of those packages.
- Delete `internal/testutils`. Keep one real SQLite fixture under `internal/testsupport/sqlite`; keep small fakes beside their tests.
- Keep `internal/config` and `internal/version`. Rename credential-specific `internal/crypto` to `internal/credentialcipher`.

Git LFS remains a supported HTTP protocol. Its generated request and response rules stay in `internal/httpapi/lfs`; target selection, multipart operations, and pending metadata move to `internal/transfers`. `internal/httpapi/apidocs` serves Swagger and OpenAPI at runtime. It is distinct from the repository's author-maintained `docs` directory. Attribution means the access-issued events and grants produced by DRS, internal transfers, and LFS. `transfers` assembles the access facts, while `usage` owns event identity, persistence ports, accounting, and reports.

This packet replaces the older audit's recommendation to retain most package boundaries. The older untracked document was not edited.

This audit changed documentation only. It did not change application code, generated bindings, tests, configuration, modules, commits, or releases. The [architecture](architecture.md), [interface ledger](interfaces.md), [work packages](work-packages.md), [inventory](package-inventory.tsv), and [verification record](verification.md) form one execution packet.

## Entry and exit conditions

Start from the exact baseline or re-run the inventory and reconcile every changed caller before using this plan. Preserve the current routes, JSON, authorization, identity, SQL transaction, provider, cache, and plugin behavior unless a work package says otherwise. Keep behavior fixes in the backlog described in [work-packages.md](work-packages.md#behavior-change-backlog).

Finish only when all structural checks pass, all retired imports are zero, both ordinary and isolated-module tests pass, and the remaining external gates are recorded. A workspace build cannot prove a separately released client module. SQL mocks cannot prove live PostgreSQL. Provider unit tests cannot prove real cloud, KMS, or local-file deployment behavior. Publishing modules or tags requires separate authorization.

## Copyable execution prompt

```text
Execute docs/internal-architecture-plan from integration branch refactor/arc, created at development commit 45d8199696866aabbc86942cff9ecb2a1bba7f31. Use one Sol-medium governor and Luna-xhigh implementation workers, with at most five workers in a wave. Give workers disjoint files or isolated branches. The Sol governor owns shared-file integration, dependency decisions, and final prose. Do not add an automatic Astra review. Implement work packages in the documented dependency order, preserve the listed semantic contracts, and run each package's focused checks before its handoff. Run the full workspace suite once after integration. Run PostgreSQL, provider, KMS, and isolated-module tests in configured disposable test environments, and report unavailable gates. Do not mix behavior-change backlog items into structural moves. Regenerate bindings only if their source schema or generator configuration changes. Stop before module publication or schema deployment unless separately authorized. Record exact commands, skips, and external gaps.
```
