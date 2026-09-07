-- Normalize legacy drs_object.name values from full paths to basenames.
--
-- Usage:
--   psql "$DATABASE_URL" -f internal/persistence/postgres/scripts/normalize_object_names_to_basename_postgres.sql
--
-- The update only touches non-empty names whose slash/backslash-normalized
-- basename differs from the stored value. Access URLs, aliases, checksums, and
-- controlled_access rows are intentionally untouched.

BEGIN;

WITH candidates AS (
	SELECT
		id,
		name,
		regexp_replace(replace(btrim(name), E'\\', '/'), '^.*/', '') AS basename
	FROM drs_object
	WHERE name IS NOT NULL
		AND btrim(name) <> ''
),
changed AS (
	SELECT id, name, basename
	FROM candidates
	WHERE basename <> ''
		AND name <> basename
)
SELECT COUNT(*) AS rows_to_update
FROM changed;

WITH candidates AS (
	SELECT
		id,
		name,
		regexp_replace(replace(btrim(name), E'\\', '/'), '^.*/', '') AS basename
	FROM drs_object
	WHERE name IS NOT NULL
		AND btrim(name) <> ''
),
changed AS (
	SELECT id, name, basename
	FROM candidates
	WHERE basename <> ''
		AND name <> basename
)
SELECT id, name AS old_name, basename AS new_name
FROM changed
ORDER BY id
LIMIT 20;

WITH candidates AS (
	SELECT
		id,
		regexp_replace(replace(btrim(name), E'\\', '/'), '^.*/', '') AS basename
	FROM drs_object
	WHERE name IS NOT NULL
		AND btrim(name) <> ''
),
changed AS (
	SELECT id, basename
	FROM candidates
	WHERE basename <> ''
)
UPDATE drs_object AS o
SET
	name = changed.basename,
	updated_time = now()
FROM changed
WHERE o.id = changed.id
	AND o.name <> changed.basename;

WITH candidates AS (
	SELECT
		id,
		name,
		regexp_replace(replace(btrim(name), E'\\', '/'), '^.*/', '') AS basename
	FROM drs_object
	WHERE name IS NOT NULL
		AND btrim(name) <> ''
)
SELECT COUNT(*) AS rows_still_path_like
FROM candidates
WHERE basename <> ''
	AND name <> basename;

COMMIT;
