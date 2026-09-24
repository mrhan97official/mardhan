-- DevControl schema for Cloudflare D1 (SQLite dialect)
-- Fourteen application and management tables. Safe to re-run to create tables missing from a
-- partial setup; IF NOT EXISTS does not upgrade columns in existing tables.
-- Do not run db/seed.sql repeatedly: it inserts demo rows each time.

CREATE TABLE IF NOT EXISTS overview_stats (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  active_projects INTEGER NOT NULL,
  active_projects_change REAL NOT NULL,
  deployments_today INTEGER NOT NULL,
  deployments_change REAL NOT NULL,
  uptime REAL NOT NULL,
  uptime_change REAL NOT NULL,
  open_incidents INTEGER NOT NULL,
  incidents_change REAL NOT NULL,
  updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS environments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  region TEXT NOT NULL,
  version TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('Healthy', 'Online', 'Degraded', 'Down'))
);

CREATE TABLE IF NOT EXISTS deployment_pipeline (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  stage TEXT NOT NULL,
  duration TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('Success', 'Running', 'Pending', 'Failed')),
  position INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS services (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('Healthy', 'Degraded', 'Down')),
  uptime REAL NOT NULL,
  version TEXT NOT NULL,
  repo TEXT,
  branch TEXT DEFAULT 'main',
  app_url TEXT
);

CREATE TABLE IF NOT EXISTS activity_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  title TEXT NOT NULL,
  description TEXT NOT NULL,
  icon TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS live_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  level TEXT NOT NULL CHECK (level IN ('INFO', 'WARN', 'ERROR')),
  message TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS api_performance (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  response_time_ms INTEGER NOT NULL,
  response_time_change REAL NOT NULL,
  request_volume INTEGER NOT NULL,
  request_volume_change REAL NOT NULL,
  error_rate REAL NOT NULL,
  error_rate_change REAL NOT NULL,
  updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);

-- One row per reading; the /api/health handler groups these by metric into
-- a sparkline series. metric is one of: cpu, memory, network, requests.
CREATE TABLE IF NOT EXISTS infra_metrics (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  metric TEXT NOT NULL CHECK (metric IN ('cpu', 'memory', 'network', 'requests')),
  value REAL NOT NULL,
  recorded_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_infra_metrics_metric_time ON infra_metrics (metric, recorded_at);
CREATE INDEX IF NOT EXISTS idx_live_logs_created_at ON live_logs (created_at);
CREATE INDEX IF NOT EXISTS idx_activity_created_at ON activity_log (created_at);

-- Original ZIPs live in a private R2 bucket; this table tracks every upload
-- and preserves the last successful version when an update fails.
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

-- Management metadata. Existing application tables and ZIP archives are never reset.
CREATE TABLE IF NOT EXISTS schema_migrations (
  version TEXT PRIMARY KEY,
  applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS managed_apis (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  project TEXT NOT NULL,
  path TEXT NOT NULL,
  method TEXT NOT NULL CHECK (method IN ('GET', 'HEAD')),
  environment TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_managed_apis_project ON managed_apis (project);

CREATE TABLE IF NOT EXISTS api_keys (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  key_prefix TEXT NOT NULL,
  key_hash TEXT NOT NULL UNIQUE,
  scopes TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  revoked_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_api_keys_active ON api_keys (key_hash, revoked_at);

-- Real checks initiated by an admin. These are not claimed to represent
-- traffic inside other applications or all incoming DevControl requests.
CREATE TABLE IF NOT EXISTS api_check_metrics (
  id TEXT PRIMARY KEY,
  api_id TEXT NOT NULL,
  status_code INTEGER NOT NULL,
  latency_ms INTEGER NOT NULL,
  checked_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_api_check_metrics_time ON api_check_metrics (checked_at DESC);

CREATE TABLE IF NOT EXISTS admin_audit_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  action TEXT NOT NULL,
  target TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_admin_audit_time ON admin_audit_log (created_at DESC);
