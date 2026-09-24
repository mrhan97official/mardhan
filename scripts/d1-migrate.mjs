#!/usr/bin/env node
// Runs a .sql file (db/schema.sql or db/seed.sql) against Cloudflare D1
// over the same HTTP API the deployed app uses (pkg/d1/client.go) — instead
// of the `wrangler d1 execute` CLI, so nobody has to paste database_id into
// wrangler.toml. It reads CF_ACCOUNT_ID / CF_D1_DATABASE_ID / CF_API_TOKEN
// from the environment, falling back to a local .env.local or .env file (the
// same variables already set in Vercel — see .env.example).
//
// Usage: node scripts/d1-migrate.mjs ./db/schema.sql
import { readFileSync, existsSync } from "node:fs";
import { basename } from "node:path";

// Every D1 table read or written by api/gateway.go.
const expectedTables = {
  overview_stats: ["id", "active_projects", "active_projects_change", "deployments_today", "deployments_change", "uptime", "uptime_change", "open_incidents", "incidents_change", "updated_at"],
  environments: ["id", "name", "region", "version", "status"],
  deployment_pipeline: ["id", "stage", "duration", "status", "position"],
  deployment_jobs: ["id", "kind", "target", "lock_key", "status", "stages", "lease_until", "created_at", "updated_at"],
  services: ["id", "name", "status", "uptime", "version", "repo", "branch", "app_url"],
  activity_log: ["id", "title", "description", "icon", "created_at"],
  live_logs: ["id", "level", "message", "created_at"],
  api_performance: ["id", "response_time_ms", "response_time_change", "request_volume", "request_volume_change", "error_rate", "error_rate_change", "updated_at"],
  infra_metrics: ["id", "metric", "value", "recorded_at"],
  zip_archives: ["id", "scope", "target", "filename", "object_key", "size_bytes", "sha256", "status", "source", "created_at"],
  schema_migrations: ["version", "applied_at"],
  managed_apis: ["id", "name", "project", "path", "method", "environment", "enabled", "created_at"],
  api_keys: ["id", "name", "key_prefix", "key_hash", "scopes", "created_at", "revoked_at"],
  api_check_metrics: ["id", "api_id", "status_code", "latency_ms", "checked_at"],
  admin_audit_log: ["id", "action", "target", "created_at"],
  app_branding: ["id", "version", "updated_at"],
};
const expectedIndexes = ["idx_deployment_jobs_running_target"];

function loadDotEnv(file) {
  if (!existsSync(file)) return;
  for (const line of readFileSync(file, "utf8").split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const eq = trimmed.indexOf("=");
    if (eq === -1) continue;
    const key = trimmed.slice(0, eq).trim();
    let value = trimmed.slice(eq + 1).trim();
    if (
      (value.startsWith('"') && value.endsWith('"')) ||
      (value.startsWith("'") && value.endsWith("'"))
    ) {
      value = value.slice(1, -1);
    }
    if (process.env[key] === undefined) process.env[key] = value;
  }
}

function splitStatements(sql) {
  return sql
    .split("\n")
    .filter((line) => !line.trimStart().startsWith("--"))
    .join("\n")
    .split(";")
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}

async function query(endpoint, token, sql) {
  const res = await fetch(endpoint, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ sql }),
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok || body.success !== true || !Array.isArray(body.result) ||
      body.result.some((item) => item.success === false)) {
    const message = body.errors?.[0]?.message ||
      body.result?.find((item) => item.success === false)?.error ||
      `HTTP ${res.status}: D1 did not confirm the statement`;
    throw new Error(String(message));
  }
  return body.result[0]?.results ?? [];
}

async function verifySchema(endpoint, token) {
  const rows = await query(endpoint, token,
    "SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%'");
  const found = new Set(rows.map((row) => row.name));
  const missingTables = Object.keys(expectedTables).filter((name) => !found.has(name));
  const missingColumns = [];
  for (const [table, expected] of Object.entries(expectedTables)) {
    if (!found.has(table)) continue;
    const columns = await query(endpoint, token, `PRAGMA table_info(${table})`);
    const present = new Set(columns.map((column) => column.name));
    for (const column of expected) {
      if (!present.has(column)) missingColumns.push(`${table}.${column}`);
    }
  }
  const indexRows = await query(endpoint, token, "SELECT name FROM sqlite_schema WHERE type = 'index'");
  const foundIndexes = new Set(indexRows.map((row) => row.name));
  const missingIndexes = expectedIndexes.filter((name) => !foundIndexes.has(name));
  if (missingTables.length || missingColumns.length || missingIndexes.length) {
    throw new Error(
      `D1 schema incomplete. Missing tables: ${missingTables.join(", ") || "none"}. ` +
      `Missing columns: ${missingColumns.join(", ") || "none"}. ` +
      `Missing indexes: ${missingIndexes.join(", ") || "none"}. ` +
      "Apply db/schema.sql again for missing tables; for existing services tables, " +
      "apply the relevant db/migrations/*.sql for missing columns."
    );
  }
  console.log(`Verified ${Object.keys(expectedTables).length} application tables, their columns, and deployment lock index in D1.`);
}

async function main() {
  const file = process.argv[2];
  if (!file) {
    console.error("Usage: node scripts/d1-migrate.mjs <path-to-sql-file|--verify>");
    process.exit(1);
  }

  loadDotEnv(".env.local");
  loadDotEnv(".env");
  const accountId = process.env.CF_ACCOUNT_ID;
  const databaseId = process.env.CF_D1_DATABASE_ID;
  const token = process.env.CF_API_TOKEN;
  if (!accountId || !databaseId || !token) {
    console.error(
      "Missing CF_ACCOUNT_ID / CF_D1_DATABASE_ID / CF_API_TOKEN.\n" +
      "Set them in .env.local, .env, or the shell environment — the same values you put in Vercel."
    );
    process.exit(1);
  }

  const endpoint = `https://api.cloudflare.com/client/v4/accounts/${accountId}/d1/database/${databaseId}/query`;

  if (file === "--verify") {
    await verifySchema(endpoint, token);
    return;
  }

  const statements = splitStatements(readFileSync(file, "utf8"));
  const isOneTimeMigration = file.includes("/migrations/") || file.includes("\\migrations\\");

  for (const [index, sql] of statements.entries()) {
    try {
      await query(endpoint, token, sql);
    } catch (error) {
      const message = String(error.message);
      if (isOneTimeMigration && message.toLowerCase().includes("duplicate column name")) {
        console.log(`skip (already applied): ${sql.slice(0, 60)}...`);
        continue;
      }
      throw new Error(`Statement ${index + 1}/${statements.length} failed: ${sql.slice(0, 80)}...\n  -> ${message}`);
    }
  }

  console.log(`Done: ${statements.length} statement(s) from ${file} applied to D1.`);
  if (basename(file) === "schema.sql") await verifySchema(endpoint, token);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
