CREATE TABLE IF NOT EXISTS drs_object (
  id TEXT PRIMARY KEY,
  size BIGINT,
  created_time TIMESTAMPTZ,
  updated_time TIMESTAMPTZ,
  name TEXT,
  version TEXT,
  description TEXT
);

ALTER TABLE drs_object
  DROP COLUMN IF EXISTS file_name;

CREATE TABLE IF NOT EXISTS drs_object_access_method (
  object_id TEXT NOT NULL,
  url TEXT NOT NULL,
  type TEXT NOT NULL,
  FOREIGN KEY(object_id) REFERENCES drs_object(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS drs_object_controlled_access (
  object_id TEXT NOT NULL,
  resource TEXT NOT NULL,
  FOREIGN KEY(object_id) REFERENCES drs_object(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS drs_object_checksum (
  object_id TEXT NOT NULL,
  type TEXT NOT NULL,
  checksum TEXT NOT NULL,
  FOREIGN KEY(object_id) REFERENCES drs_object(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS drs_object_alias (
  alias_id TEXT PRIMARY KEY,
  object_id TEXT NOT NULL,
  FOREIGN KEY(object_id) REFERENCES drs_object(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS drs_object_name_alias (
  object_id TEXT NOT NULL,
  name_alias TEXT NOT NULL,
  PRIMARY KEY (object_id, name_alias),
  FOREIGN KEY(object_id) REFERENCES drs_object(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS drs_object_read_policy (
  object_id TEXT PRIMARY KEY,
  public_read BOOLEAN NOT NULL DEFAULT FALSE,
  FOREIGN KEY(object_id) REFERENCES drs_object(id) ON DELETE CASCADE
);

DROP TABLE IF EXISTS drs_object_browse_index;

CREATE INDEX IF NOT EXISTS drs_object_access_method_object_id_idx
  ON drs_object_access_method(object_id);

CREATE INDEX IF NOT EXISTS drs_object_checksum_object_id_idx
  ON drs_object_checksum(object_id);

CREATE INDEX IF NOT EXISTS drs_object_checksum_checksum_idx
  ON drs_object_checksum(checksum);

CREATE INDEX IF NOT EXISTS drs_object_checksum_checksum_type_object_id_idx
  ON drs_object_checksum(checksum, type, object_id);

CREATE INDEX IF NOT EXISTS drs_object_checksum_sha256_identity_idx
  ON drs_object_checksum(
    (replace(lower(trim(type)), '-', '')),
    (replace(lower(trim(checksum)), 'sha256:', '')),
    object_id
  );

CREATE INDEX IF NOT EXISTS drs_object_controlled_access_object_id_idx
  ON drs_object_controlled_access(object_id);

CREATE INDEX IF NOT EXISTS drs_object_controlled_access_resource_idx
  ON drs_object_controlled_access(resource);

CREATE INDEX IF NOT EXISTS drs_object_controlled_access_resource_object_id_idx
  ON drs_object_controlled_access(resource, object_id);

CREATE INDEX IF NOT EXISTS drs_object_controlled_access_object_id_resource_idx
  ON drs_object_controlled_access(object_id, resource);

CREATE INDEX IF NOT EXISTS drs_object_alias_object_id_idx
  ON drs_object_alias(object_id);

CREATE INDEX IF NOT EXISTS drs_object_name_alias_object_id_idx
  ON drs_object_name_alias(object_id);
