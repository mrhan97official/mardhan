-- Safe to run on existing D1 databases. Original ZIP bytes are stored in R2.
CREATE TABLE IF NOT EXISTS zip_archives (
  id TEXT PRIMARY KEY,
  scope TEXT NOT NULL CHECK (scope IN ('app', 'self')),
  target TEXT NOT NULL,
  filename TEXT NOT NULL,
  object_key TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  sha256 TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending', 'current', 'previous', 'failed')),
  source TEXT NOT NULL CHECK (source IN ('upload', 'github_snapshot')),
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_zip_archives_target ON zip_archives (scope, target, created_at DESC);
