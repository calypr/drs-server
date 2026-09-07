# Problem / Solution: Project Hierarchy Index For Repo-Style Browsing

## Problem

The current frontend project view derives a virtual directory tree by loading all Syfon index records for a project into browser memory and then grouping them by path-like metadata.

That works for small and medium projects, but it does not scale well for large projects:

- the client must fetch the full project record set
- the client must hold all records in memory at once
- directory structure is recomputed client-side from flat records
- large projects create noticeable latency and memory pressure in the browser

This is especially awkward when the bucket/object path is already the real source of hierarchy, but Syfon only exposes the project as a flat record listing.

## Desired Outcome

Syfon should own a hierarchical browsing layer for project-scoped data.

The goal is to support repo-style navigation without requiring clients to load every record in the project just to compute folders.

## Proposed Direction

Add a hierarchy table or materialized hierarchical index derived from bucket/object paths for each `{organization, project}` scope.

The hierarchy layer should:

- map records into normalized path segments
- preserve exact project scope boundaries
- support directory listing by prefix/path
- allow clients to request only one directory level at a time
- avoid full project scans for normal browsing

## Possible Shapes

1. A dedicated hierarchy table keyed by:
   - organization
   - project
   - normalized path
   - entry type (`file` or `directory`)
   - record or object identifier

2. A materialized project-tree index refreshed when records are created, updated, or deleted.

3. A new API surface for repo-style browsing, for example:
   - list root entries for a project
   - list children under a given path prefix
   - fetch file metadata for a selected entry

## Data Inputs

The hierarchy should be derived from authoritative object/bucket path information where available, rather than forcing the frontend to infer structure from mixed metadata fields.

Open questions:

- Which field should be canonical for hierarchy derivation: bucket path, object key, or another normalized source?
- How should conflicts be handled when metadata names differ from object paths?
- Should directories be materialized eagerly or derived lazily and cached?
- How should renames or path changes be represented historically?

## Why This Helps

A hierarchy layer would let the frontend request only the current directory view instead of the full project.

Benefits:

- lower browser memory usage
- faster initial project load
- less pagination churn on large projects
- cleaner contract for repo-style navigation
- less duplicated tree-building logic across clients

## Acceptance Criteria

- A client can browse a project directory tree without loading all project records.
- A client can request entries for a single path prefix or directory level.
- The hierarchy is scoped exactly to `{organization, project}`.
- The API can support large projects without requiring full in-browser reconstruction.

## Current Status

Frontend currently derives the directory structure from the full flat record set. This note tracks the Syfon-side improvement needed to make repo-style browsing scale properly.
