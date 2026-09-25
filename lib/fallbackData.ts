import type {
  ActivityItem,
  ApiPerformance,
  Environment,
  GithubRepo,
  InfraMetric,
  LiveLog,
  OverviewStats,
  PipelineStage,
  Service,
} from "./types";

export const fallbackOverview: OverviewStats = {
  active_projects: 0,
  active_projects_change: 0,
  deployments_today: 0,
  deployments_change: 0,
  uptime: 0,
  uptime_change: 0,
  open_incidents: 0,
  incidents_change: 0,
};

export const fallbackPipeline: PipelineStage[] = [
  { id: 1, stage: "Ekstrak ZIP", duration: "-", status: "Pending", position: 1 },
  { id: 2, stage: "Uji Vercel", duration: "-", status: "Pending", position: 2 },
  { id: 3, stage: "GitHub", duration: "-", status: "Pending", position: 3 },
  { id: 4, stage: "Online Vercel", duration: "-", status: "Pending", position: 4 },
];

export const fallbackEnvironments: Environment[] = [
  { id: 1, name: "Development", region: "us-east-1", version: "v2.4.0", status: "Healthy" },
  { id: 2, name: "Staging", region: "eu-west-1", version: "v2.4.0", status: "Healthy" },
  { id: 3, name: "Production", region: "us-east-1", version: "v2.3.8", status: "Online" },
];

export const fallbackServices: Service[] = [
  { id: 1, name: "Auth Service", status: "Healthy", uptime: 99.99, version: "v2.4.0" },
  { id: 2, name: "API Gateway", status: "Healthy", uptime: 99.98, version: "v2.4.0" },
  { id: 3, name: "Database", status: "Healthy", uptime: 99.99, version: "v2.3.8" },
  { id: 4, name: "Storage", status: "Degraded", uptime: 99.95, version: "v2.4.0" },
  { id: 5, name: "Queue", status: "Healthy", uptime: 99.99, version: "v2.4.0" },
];

export const fallbackActivity: ActivityItem[] = [
  { id: 1, title: "Production deployment completed", description: "v2.3.8 deployed to production", icon: "check", created_at: "5m ago" },
  { id: 2, title: "API key rotated", description: "Service account key updated", icon: "key", created_at: "12m ago" },
  { id: 3, title: "Database backup finished", description: "Daily backup completed successfully", icon: "database", created_at: "28m ago" },
  { id: 4, title: "New project created", description: "customer-portal", icon: "box", created_at: "2h ago" },
  { id: 5, title: "Team member added", description: "jane.doe@company.com", icon: "user", created_at: "3h ago" },
];

export const fallbackLogs: LiveLog[] = [
  { id: 1, level: "INFO", message: "Starting deployment process...", created_at: "14:24:01" },
  { id: 2, level: "INFO", message: "Pulling image: app:2.3.8", created_at: "14:24:03" },
  { id: 3, level: "INFO", message: "Creating containers...", created_at: "14:24:12" },
  { id: 4, level: "INFO", message: "Health check passed", created_at: "14:24:28" },
  { id: 5, level: "INFO", message: "Routing traffic to new version", created_at: "14:24:31" },
  { id: 6, level: "INFO", message: "Deployment completed successfully", created_at: "14:24:33" },
  { id: 7, level: "WARN", message: "High memory usage detected (78%)", created_at: "14:27:14" },
  { id: 8, level: "INFO", message: "Database backup started", created_at: "14:32:09" },
  { id: 9, level: "INFO", message: "Database backup completed successfully", created_at: "14:34:21" },
];

export const fallbackApiPerformance: ApiPerformance = {
  response_time_ms: 0,
  response_time_change: 0,
  request_volume: 0,
  request_volume_change: 0,
  error_rate: 0,
  error_rate_change: 0,
  series_latency: [],
  series_volume: [],
  series_errors: [],
  range: "24h",
};

function wave(base: number, spread: number, n = 20) {
  return Array.from({ length: n }, (_, i) =>
    Math.max(0, Math.round(base + Math.sin(i / 2.3) * spread + (Math.random() - 0.5) * spread * 0.6))
  );
}

export const fallbackInfra: InfraMetric[] = [
  { metric: "cpu", values: wave(18, 8), current: 18 },
  { metric: "memory", values: wave(42, 10), current: 42 },
  { metric: "network", values: wave(28, 9), current: 28 },
  { metric: "requests", values: wave(55, 15), current: 55 },
];

// Empty on purpose: unlike the dashboard's demo fallbacks above, there's no
// sensible placeholder repo list. If /api/github-repos can't be reached the
// picker comes back empty and the UI displays the API error.
export const fallbackGithubRepos: GithubRepo[] = [];

export const summarySparklines = {
  projects: wave(60, 20, 12),
  deployments: wave(50, 18, 12),
  uptime: wave(70, 6, 12),
  incidents: wave(40, 25, 12),
};
