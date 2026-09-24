-- Run this single SELECT in the Cloudflare D1 Console after applying schema.sql.
-- All fourteen rows must say ADA; _cf_KV is Cloudflare's internal table.
WITH expected(name) AS (
  VALUES ('overview_stats'), ('environments'), ('deployment_pipeline'),
         ('services'), ('activity_log'), ('live_logs'),
         ('api_performance'), ('infra_metrics'), ('zip_archives'),
         ('schema_migrations'), ('managed_apis'), ('api_keys'),
         ('api_check_metrics'), ('admin_audit_log')
)
SELECT expected.name AS tabel,
       CASE WHEN sqlite_schema.name IS NULL THEN 'BELUM ADA' ELSE 'ADA' END AS status
FROM expected
LEFT JOIN sqlite_schema ON sqlite_schema.name = expected.name
                       AND sqlite_schema.type = 'table'
ORDER BY expected.name;
