# TODO: Fix Scoped `/index` Pagination Performance

## Problem

Exact project listings like `GET /index?organization=...&project=...` are slow at scale.

For large projects, current behavior can take multiple seconds per 1k records. The slowdown is not just "too many round trips"; it also comes from how pagination and hydration work.

## Current Behavior

The current scoped `/index` list path has two separate costs:

1. `page` currently becomes `offset = page * limit`.
2. Scoped list queries then use `LIMIT/OFFSET`, so later pages get slower as offsets grow.

After ID selection, the server hydrates full objects for the page. Bulk hydration currently joins base objects, access methods, and checksums in one fanout query, which multiplies rows for objects with multiple access methods and multiple checksums.

That means exact project listings pay both:

- deep-page offset cost
- per-page full-record hydration cost

## Why Raising `limit` Alone Is Not Enough

Larger pages reduce request count but do not remove offset cost.

Larger pages also make the current hydration query heavier. A 10k-page limit may worsen latency and memory if applied before the query-path fixes land.

Raising the cap may still become useful later as an operational tuning option, but it should not be treated as the primary fix for slow exact project listings.

## Proposed Fix Order

1. Make scoped `/index` clients prefer cursor pagination via `start` over `page`.
2. Keep `page` support for compatibility, but treat it as legacy behavior for large traversals.
3. Rewrite bulk object hydration to use separate batched reads:
   - base `drs_object` rows
   - `drs_object_access_method` rows for selected IDs
   - `drs_object_checksum` rows for selected IDs
   - merge the result sets in Go
4. Re-measure large-project listing performance.
5. Only after that, reconsider whether scoped list-page caps should be raised.

## API / Compatibility Notes

No new endpoint is required.

`start` should remain the preferred pagination mechanism for large listings. `page` should remain supported for backward compatibility.

Any future increase to max page size should be documented as an operational tuning option, not the primary optimization.

## Acceptance Criteria

- Exact project-scoped listings no longer depend on offset pagination in the recommended client path.
- Later-page latency does not degrade the way it does with deep offsets today.
- Hydration no longer uses the fanout join shape that multiplies rows across access methods and checksums.
- Performance validation covers a large dataset and explicitly includes later-page timings.

## Test / Validation Notes

- Compare `page` traversal versus `start` traversal for equal result sets.
- Benchmark deep-page latency on a dataset around 30k records.
- Benchmark hydration before and after the query split.
- Verify stable ordering for cursor traversal.

## Related Notes

See [problem-solution-configurable-index-page-size.md](/Users/peterkor/Desktop/BMEG/syfon-complex/syfon/docs/problem-solution-configurable-index-page-size.md) for adjacent background on page-size caps.

That page-size note is incomplete by itself because it does not address the main query-path bottlenecks: offset pagination and bulk hydration fanout.

## Implementation Boundaries

This note is intentionally scoped to the Syfon `/index` exact `organization/project` listing path.

It does not broaden scope to `requestor`, metrics endpoints, or unrelated list paths. The implementation step after this doc should remain separate from this documentation-only change.
