-- DevControl seed data mirroring the reference dashboard design

INSERT INTO overview_stats (active_projects, active_projects_change, deployments_today, deployments_change, uptime, uptime_change, open_incidents, incidents_change) VALUES
  (12, 20, 28, 12, 99.99, 0.01, 2, -60);

INSERT INTO environments (name, region, version, status) VALUES
  ('Development', 'us-east-1', 'v2.4.0', 'Healthy'),
  ('Staging', 'eu-west-1', 'v2.4.0', 'Healthy'),
  ('Production', 'us-east-1', 'v2.3.8', 'Online');

INSERT INTO deployment_pipeline (stage, duration, status, position) VALUES
  ('Ekstrak ZIP', '-', 'Pending', 1),
  ('Uji Vercel', '-', 'Pending', 2),
  ('GitHub', '-', 'Pending', 3),
  ('Online Vercel', '-', 'Pending', 4);

INSERT INTO services (name, status, uptime, version) VALUES
  ('Auth Service', 'Healthy', 99.99, 'v2.4.0'),
  ('API Gateway', 'Healthy', 99.98, 'v2.4.0'),
  ('Database', 'Healthy', 99.99, 'v2.3.8'),
  ('Storage', 'Degraded', 99.95, 'v2.4.0'),
  ('Queue', 'Healthy', 99.99, 'v2.4.0');

INSERT INTO activity_log (title, description, icon, created_at) VALUES
  ('Production deployment completed', 'v2.3.8 deployed to production', 'check', '2024-03-15 14:29:00'),
  ('API key rotated', 'Service account key updated', 'key', '2024-03-15 14:22:00'),
  ('Database backup finished', 'Daily backup completed successfully', 'database', '2024-03-15 14:06:00'),
  ('New project created', 'customer-portal', 'box', '2024-03-15 12:34:00'),
  ('Team member added', 'jane.doe@company.com', 'user', '2024-03-15 11:34:00');

INSERT INTO live_logs (level, message, created_at) VALUES
  ('INFO', 'Starting deployment process...', '2024-03-15 14:24:01'),
  ('INFO', 'Pulling image: app:2.3.8', '2024-03-15 14:24:03'),
  ('INFO', 'Creating containers...', '2024-03-15 14:24:12'),
  ('INFO', 'Health check passed', '2024-03-15 14:24:28'),
  ('INFO', 'Routing traffic to new version', '2024-03-15 14:24:31'),
  ('INFO', 'Deployment completed successfully', '2024-03-15 14:24:33'),
  ('WARN', 'High memory usage detected (78%)', '2024-03-15 14:27:14'),
  ('INFO', 'Database backup started', '2024-03-15 14:32:09'),
  ('INFO', 'Database backup completed successfully', '2024-03-15 14:34:21');

INSERT INTO api_performance (response_time_ms, response_time_change, request_volume, request_volume_change, error_rate, error_rate_change) VALUES
  (142, -18, 12400, 24, 0.2, -56);

INSERT INTO infra_metrics (metric, value, recorded_at) VALUES
  ('cpu', 16.9, '2024-03-14 18:00:00'),
  ('cpu', 19.1, '2024-03-14 19:15:47'),
  ('cpu', 25.1, '2024-03-14 20:31:34'),
  ('cpu', 23.0, '2024-03-14 21:47:22'),
  ('cpu', 26.1, '2024-03-14 23:03:09'),
  ('cpu', 23.7, '2024-03-15 00:18:56'),
  ('cpu', 19.2, '2024-03-15 01:34:44'),
  ('cpu', 18.8, '2024-03-15 02:50:31'),
  ('cpu', 12.4, '2024-03-15 04:06:18'),
  ('cpu', 12.0, '2024-03-15 05:22:06'),
  ('cpu', 7.8, '2024-03-15 06:37:53'),
  ('cpu', 7.4, '2024-03-15 07:53:41'),
  ('cpu', 10.5, '2024-03-15 09:09:28'),
  ('cpu', 15.4, '2024-03-15 10:25:15'),
  ('cpu', 14.0, '2024-03-15 11:41:03'),
  ('cpu', 18.1, '2024-03-15 12:56:50'),
  ('cpu', 23.8, '2024-03-15 14:12:37'),
  ('cpu', 28.0, '2024-03-15 15:28:25'),
  ('cpu', 26.5, '2024-03-15 16:44:12'),
  ('cpu', 24.7, '2024-03-15 18:00:00'),
  ('memory', 45.8, '2024-03-14 18:00:00'),
  ('memory', 42.6, '2024-03-14 19:15:47'),
  ('memory', 52.5, '2024-03-14 20:31:34'),
  ('memory', 50.0, '2024-03-14 21:47:22'),
  ('memory', 49.0, '2024-03-14 23:03:09'),
  ('memory', 47.2, '2024-03-15 00:18:56'),
  ('memory', 45.5, '2024-03-15 01:34:44'),
  ('memory', 45.5, '2024-03-15 02:50:31'),
  ('memory', 36.1, '2024-03-15 04:06:18'),
  ('memory', 35.7, '2024-03-15 05:22:06'),
  ('memory', 33.8, '2024-03-15 06:37:53'),
  ('memory', 31.0, '2024-03-15 07:53:41'),
  ('memory', 33.6, '2024-03-15 09:09:28'),
  ('memory', 32.6, '2024-03-15 10:25:15'),
  ('memory', 36.5, '2024-03-15 11:41:03'),
  ('memory', 42.0, '2024-03-15 12:56:50'),
  ('memory', 49.7, '2024-03-15 14:12:37'),
  ('memory', 50.4, '2024-03-15 15:28:25'),
  ('memory', 50.5, '2024-03-15 16:44:12'),
  ('memory', 51.9, '2024-03-15 18:00:00'),
  ('network', 27.7, '2024-03-14 18:00:00'),
  ('network', 30.3, '2024-03-14 19:15:47'),
  ('network', 37.0, '2024-03-14 20:31:34'),
  ('network', 38.1, '2024-03-14 21:47:22'),
  ('network', 35.0, '2024-03-14 23:03:09'),
  ('network', 35.9, '2024-03-15 00:18:56'),
  ('network', 32.8, '2024-03-15 01:34:44'),
  ('network', 31.6, '2024-03-15 02:50:31'),
  ('network', 26.7, '2024-03-15 04:06:18'),
  ('network', 20.2, '2024-03-15 05:22:06'),
  ('network', 23.0, '2024-03-15 06:37:53'),
  ('network', 16.3, '2024-03-15 07:53:41'),
  ('network', 19.5, '2024-03-15 09:09:28'),
  ('network', 24.5, '2024-03-15 10:25:15'),
  ('network', 23.7, '2024-03-15 11:41:03'),
  ('network', 30.0, '2024-03-15 12:56:50'),
  ('network', 30.3, '2024-03-15 14:12:37'),
  ('network', 37.3, '2024-03-15 15:28:25'),
  ('network', 38.9, '2024-03-15 16:44:12'),
  ('network', 36.8, '2024-03-15 18:00:00'),
  ('requests', 59.5, '2024-03-14 18:00:00'),
  ('requests', 59.1, '2024-03-14 19:15:47'),
  ('requests', 68.8, '2024-03-14 20:31:34'),
  ('requests', 70.6, '2024-03-14 21:47:22'),
  ('requests', 70.7, '2024-03-14 23:03:09'),
  ('requests', 66.8, '2024-03-15 00:18:56'),
  ('requests', 66.7, '2024-03-15 01:34:44'),
  ('requests', 61.8, '2024-03-15 02:50:31'),
  ('requests', 49.7, '2024-03-15 04:06:18'),
  ('requests', 46.5, '2024-03-15 05:22:06'),
  ('requests', 35.7, '2024-03-15 06:37:53'),
  ('requests', 42.5, '2024-03-15 07:53:41'),
  ('requests', 43.6, '2024-03-15 09:09:28'),
  ('requests', 52.1, '2024-03-15 10:25:15'),
  ('requests', 55.9, '2024-03-15 11:41:03'),
  ('requests', 56.0, '2024-03-15 12:56:50'),
  ('requests', 63.0, '2024-03-15 14:12:37'),
  ('requests', 70.4, '2024-03-15 15:28:25'),
  ('requests', 64.3, '2024-03-15 16:44:12'),
  ('requests', 68.3, '2024-03-15 18:00:00');
