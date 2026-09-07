# Problem / Solution: Configurable `/index` Page Size For Large Project Listings

## Problem

Large project views in the frontend currently need to walk the full Syfon `/index` listing to derive a complete repo-style directory view.

For projects with tens of thousands of records, the current effective page size can force many sequential requests. For example:

- project size: about 41,351 records
- page size: 1,000
- requests required: about 42 to 43 list calls

That adds noticeable latency even when each request is individually reasonable.

## Why This Matters

The frontend needs the complete record set to derive an accurate directory structure from Syfon-backed records.

When the page size is too small:

- first usable render is delayed for large projects
- project browsing feels sluggish compared to GitHub-style repo navigation
- network overhead becomes dominated by pagination churn rather than payload transfer
- clients are incentivized to make ad hoc assumptions about partial data

## Desired Outcome

Syfon should support larger, practical list-page sizes for scoped `/index` reads, especially for exact project listings.

At minimum, operators should be able to configure or raise the maximum page size so clients can request larger pages such as `limit=10000` when needed.

That cap increase is only part of the fix. The main performance improvement for large exact project listings comes from:

- cursor pagination via `start=<last_id>` instead of deep `page` offsets
- index-driven scoped ID selection on `(resource, object_id)`
- split hydration of base rows, access methods, and checksums instead of a single fanout join

## Proposed Direction

Add server-side configurability for `/index` list page size limits.

Options:

1. Make the current internal max list-page size configurable through Syfon settings.
2. Keep a safe default, but allow operators to raise the cap for deployments that expect large project listings.
3. Optionally distinguish between:
   - unscoped/global list limits
n   - scoped `organization/project` list limits
4. Document the recommended client behavior when requesting large pages.

## Considerations

Open questions:

- What default max remains safe for memory and response size?
- Should exact project-scoped requests be allowed a higher cap than broad unscoped queries?
- Should large-page support be combined with cursor-based pagination improvements, or handled independently?
- Are there proxy, gateway, or timeout constraints that make very large pages undesirable in some deployments?

## Acceptance Criteria

- Syfon operators can configure the max allowed `/index` page size.
- A deployment can safely allow larger list pages such as `10000` for large project browsing workflows.
- `page=` should be treated as compatibility-only for large traversals; clients should prefer `start=`.
- Interactive UIs should typically request `1000-2000` records per page and reserve `10000` for explicit bulk-load flows.
- The behavior is documented so clients know what page sizes are expected to succeed.

## Current Status

Frontend currently keeps the existing paging behavior. This note tracks the Syfon-side improvement so large project listings can complete in fewer round trips.
