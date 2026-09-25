-- DevControl schema for Cloudflare D1 (SQLite dialect)
-- Application and management tables. Safe to re-run to create tables missing from a
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

-- Each ZIP deployment has its own stages. The partial index permits different
-- targets to run together while preventing two writers to one repo.
CREATE TABLE IF NOT EXISTS deployment_jobs (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('new_app', 'update_app', 'self_update')),
  target TEXT NOT NULL,
  lock_key TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('Running', 'Success', 'Failed', 'Interrupted')),
  stages TEXT NOT NULL,
  lease_until TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_deployment_jobs_running_target ON deployment_jobs (lock_key) WHERE status = 'Running';
CREATE INDEX IF NOT EXISTS idx_deployment_jobs_updated ON deployment_jobs (updated_at DESC);

-- Durable stage cursor. The Cloudflare scheduler advances this one stage at
-- a time; the archive ZIP and signed Vercel ticket survive browser closure.
CREATE TABLE IF NOT EXISTS deployment_runner (
  id TEXT PRIMARY KEY,
  phase TEXT NOT NULL,
  ticket TEXT NOT NULL DEFAULT '',
  branch TEXT NOT NULL DEFAULT '',
  environment TEXT NOT NULL DEFAULT 'Production',
  claim_until TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  attempts INTEGER NOT NULL DEFAULT 0,
  message TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
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

-- A zone becomes active only after an admin approves it in Settings. D1
-- makes the choice available to running deployments before Vercel redeploys.
CREATE TABLE IF NOT EXISTS cloudflare_zone_approval (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  zone_id TEXT NOT NULL,
  zone_name TEXT NOT NULL,
  project_id TEXT NOT NULL,
  vercel_synced INTEGER NOT NULL DEFAULT 0,
  approved_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- One-click monitoring chooses Cloudflare when available or measures this
-- application's Go API traffic when no owned Cloudflare zone exists.
CREATE TABLE IF NOT EXISTS traffic_monitoring (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  mode TEXT NOT NULL CHECK (mode IN ('cloudflare', 'api')),
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS api_traffic_hourly (
  bucket TEXT PRIMARY KEY,
  requests INTEGER NOT NULL DEFAULT 0,
  response_bytes INTEGER NOT NULL DEFAULT 0
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

-- A single image per repository lives in the private R2 bucket. The version
-- changes on upload so every open Projects page can reload the new image.
CREATE TABLE IF NOT EXISTS project_thumbnails (
  repo TEXT PRIMARY KEY COLLATE NOCASE,
  version TEXT NOT NULL,
  object_key TEXT,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Pending direct-to-R2 uploads are tracked until verified and committed.
CREATE TABLE IF NOT EXISTS project_thumbnail_uploads (
  id TEXT PRIMARY KEY,
  repo TEXT NOT NULL COLLATE NOCASE,
  object_key TEXT NOT NULL,
  content_type TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_project_thumbnail_uploads_repo ON project_thumbnail_uploads (repo);
CREATE INDEX IF NOT EXISTS idx_project_thumbnail_uploads_time ON project_thumbnail_uploads (created_at);

-- Track uploaded object keys until they are removed, including older images
-- whose deletion failed during replacement. Project deletion drains this list.
CREATE TABLE IF NOT EXISTS project_thumbnail_objects (
  object_key TEXT PRIMARY KEY,
  repo TEXT NOT NULL COLLATE NOCASE,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_project_thumbnail_objects_repo ON project_thumbnail_objects (repo);

-- The current application logo is rendered into PWA icon sizes and stored in
-- private R2. A missing row means the original checked-in icon is displayed.
CREATE TABLE IF NOT EXISTS app_branding (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  version TEXT NOT NULL,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

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
