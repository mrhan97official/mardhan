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
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  diagnosis TEXT NOT NULL DEFAULT '',
  sync_note TEXT NOT NULL DEFAULT '',
  display_name TEXT NOT NULL DEFAULT ''
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
  app_url TEXT,
  display_name TEXT NOT NULL DEFAULT ''
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

-- One general or application promotion; its compressed banner image lives in
-- R2 through the same verified upload flow as project thumbnails.
CREATE TABLE IF NOT EXISTS app_promo_banner (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  target_repo TEXT NOT NULL,
  app_name TEXT NOT NULL DEFAULT '',
  app_url TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL,
  description TEXT NOT NULL,
  image_repo TEXT NOT NULL,
  version TEXT NOT NULL,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- The current application logo is rendered into PWA icon sizes and stored in
-- private R2. A missing row means the original checked-in icon is displayed.
CREATE TABLE IF NOT EXISTS app_branding (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  version TEXT NOT NULL,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Web Push (v1.0.61). The VAPID key pair is generated once and kept here
-- unless VAPID_PUBLIC_KEY/VAPID_PRIVATE_KEY are set in Vercel. runner_version
-- records the installed scheduled Worker source.
CREATE TABLE IF NOT EXISTS push_config (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  public_key TEXT NOT NULL,
  private_key TEXT NOT NULL,
  subject TEXT NOT NULL DEFAULT '',
  runner_version TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- One row per device (browser push endpoint). subject is "owner" or a member
-- id; events is a comma list of enabled notification kinds.
CREATE TABLE IF NOT EXISTS push_subscriptions (
  endpoint TEXT PRIMARY KEY,
  p256dh TEXT NOT NULL,
  auth TEXT NOT NULL,
  subject TEXT NOT NULL,
  events TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_push_subscriptions_subject ON push_subscriptions (subject);

-- Keys of notifications already sent, so retries never notify twice.
CREATE TABLE IF NOT EXISTS push_log (
  event_key TEXT PRIMARY KEY,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Vercel projects waiting for delayed removal: throwaway test projects
-- (<repo>-test-<nanotime>) and, since v1.0.70, the project of a failed
-- "Aplikasi Baru" that never went online, when its build log could not be
-- read yet or its first delete attempt failed. Removed after 30 minutes.
CREATE TABLE IF NOT EXISTS vercel_orphan_projects (
  project TEXT PRIMARY KEY,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
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
  revoked_at TEXT,
  expires_at TEXT,
  last_used_at TEXT
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

-- One row per finished deployment (success or failure) so each app keeps an
-- update history after its older ZIPs are deleted. `changes` is the JSON file
-- diff against the previous successful ZIP.
CREATE TABLE IF NOT EXISTS deployment_history (
  id TEXT PRIMARY KEY,
  repo TEXT NOT NULL,
  kind TEXT NOT NULL,
  target TEXT NOT NULL,
  status TEXT NOT NULL,
  file_name TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  sha256 TEXT NOT NULL DEFAULT '',
  changes TEXT NOT NULL DEFAULT '',
  message TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_deployment_history_repo ON deployment_history (repo, created_at DESC);

-- Members sign in with a long random token; only its SHA-256 hash is kept.
-- epoch invalidates every session of the member when bumped.
CREATE TABLE IF NOT EXISTS members (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  role TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  token_prefix TEXT NOT NULL DEFAULT '',
  ip_allowlist TEXT NOT NULL DEFAULT '[]',
  epoch INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_login_at TEXT,
  last_ip TEXT,
  revoked_at TEXT,
  expires_at TEXT
);

-- Failed sign-in counter per client IP (lockout after 5 failures).
CREATE TABLE IF NOT EXISTS auth_attempts (
  ip TEXT PRIMARY KEY,
  failures INTEGER NOT NULL DEFAULT 0,
  locked_until TEXT,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- owner_epoch: bumping it signs the owner out on every device.
CREATE TABLE IF NOT EXISTS auth_settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- Cookies signed out on the server (logout / step-up re-issue); rows expire
-- with the cookie they revoke.
CREATE TABLE IF NOT EXISTS revoked_sessions (
  nonce TEXT PRIMARY KEY,
  expires_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_revoked_sessions_expires ON revoked_sessions (expires_at);

-- Audit Aplikasi: one row per run (manual or scheduled), newest kept per app.
CREATE TABLE IF NOT EXISTS app_audits (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  app TEXT NOT NULL,
  score INTEGER NOT NULL,
  high INTEGER NOT NULL DEFAULT 0,
  medium INTEGER NOT NULL DEFAULT 0,
  low INTEGER NOT NULL DEFAULT 0,
  report TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT 'manual',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_app_audits_app_time ON app_audits (app, created_at DESC);

-- Pusat Keamanan: signed-in devices (listed and revocable one by one).
CREATE TABLE IF NOT EXISTS user_sessions (
  nonce TEXT PRIMARY KEY,
  subject TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL,
  ip TEXT NOT NULL DEFAULT '',
  user_agent TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_user_sessions_subject ON user_sessions (subject, created_at DESC);

-- Addresses each person has signed in from (new ones raise an alert).
CREATE TABLE IF NOT EXISTS login_origins (
  subject TEXT NOT NULL,
  ip TEXT NOT NULL,
  first_seen TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_seen TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (subject, ip)
);

-- Alerts raised by the gate, the patrol and the owner's own answers.
CREATE TABLE IF NOT EXISTS security_alerts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  alert_key TEXT NOT NULL UNIQUE,
  level TEXT NOT NULL CHECK (level IN ('waspada', 'siaga', 'darurat')),
  kind TEXT NOT NULL,
  title TEXT NOT NULL,
  detail TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  acknowledged_at TEXT,
  answer TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_security_alerts_open ON security_alerts (acknowledged_at, created_at DESC);

-- Patrol baselines (env hash, approved commit, 2FA seen, token fingerprints).
CREATE TABLE IF NOT EXISTS security_state (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
