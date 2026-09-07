# ADR: Consolidated JSON-First Persistence for DRS Objects

## Status
Proposed

## Context
Syfon's current persistence layer for DRS objects uses a highly normalized relational schema across both SQLite and PostgreSQL. A single DRS object is fragmented across multiple tables:
- `drs_object`: Core metadata (ID, size, timestamps).
- `drs_object_access_method`: Access URLs and protocols.
- `drs_object_checksum`: Hash values (SHA256, MD5, etc.).
- `drs_object_authz`: Internal RBAC resource paths.
- `drs_object_alias`: Secondary identifiers.

This normalization has led to "Wrapper Code Hell," characterized by:
1.  **Logic Duplication**: Manual SQL joins and struct scanners must be implemented identically for both SQLite and Postgres.
2.  **Mapping Boilerplate**: Over 1,500 lines of code are dedicated purely to marshaling the relational rows back into the nested GA4GH `drs.DrsObject` structure.
3.  **Fragility**: Adding a single field to the OpenAPI specification requires database migrations across all providers and manual updates to dozens of scanners and injectors.
4.  **Type Soup**: Redundant "Internal" vs "Record" vs "API" structs are needed to bridge the gap between flat SQL rows and nested API types.

## Decision
We will transition to a **Consolidated JSON-First Persistence** model for DRS objects.

1.  **Unified Table**: The `drs_object` table will be enhanced with a `data_json` column (JSONB in Postgres, JSON string in SQLite).
2.  **Schema De-fragmentation**: The side tables for access methods, checksums, and authorizations will be decommissioned as primary storage. Their data will instead live within the `data_json` blob, reflecting the official GA4GH DRS object structure.
3.  **Indexed Search Columns**: For performance, we will retain (or use computed/JSON indexes for) critical search keys as top-level columns:
    - `id` (Primary Key)
    - `checksum_sha256` (Indexed for fast CAS lookups)
    - `resource_prefix` (For RBAC filtering)
4.  **Direct Marshaling**: The database layer will marshal/unmarshal the `drs.DrsObject` type directly into the `data_json` column, eliminating the middleman "Record" structs.

## Rationale
- **Development Velocity**: New fields in the DRS specification can be supported instantly without database migrations or mapping code updates.
- **Maintainability**: Deleting ~1,000 lines of manual mapping logic significantly reduces the bug surface area.
- **Read Performance**: Most DRS operations are single-object lookups. Fetching a single JSON blob is significantly more efficient than performing a 4-table join on every request.
- **Spec Alignment**: The DB structure will now naturally mirror the GA4GH specification, reducing the cognitive load for developers.

## Consequences
- **Pros**:
    - Drastic reduction in "Wrapper Code" boilerplate.
    - Simplified `internal/core` models (no more redundant `InternalObject` wrappers).
    - Future-proof against specification changes.
- **Cons**:
    - Requires a data migration for existing installations.
    - Queries for complex cross-object statistics may become slightly more complex (requiring JSON path expressions).
    - Loss of certain strict relational foreign-key constraints on nested access methods.
