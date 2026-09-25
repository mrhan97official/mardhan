export interface OverviewStats {
  active_projects: number;
  active_projects_change: number;
  deployments_today: number;
  deployments_change: number;
  uptime: number;
  uptime_change: number;
  open_incidents: number;
  incidents_change: number;
}

export interface Environment {
  id: number;
  name: string;
  region: string;
  version: string;
  status: "Healthy" | "Online" | "Degraded" | "Down";
}

export interface PipelineStage {
  id: number;
  stage: string;
  duration: string;
  status: "Success" | "Running" | "Pending" | "Failed";
  position: number;
  message?: string;
  updated_at?: string;
  started_at?: number;
  finished_at?: number;
}

export interface DeploymentJob {
  id: string;
  kind: "new_app" | "update_app" | "self_update";
  target: string;
  status: "Running" | "Success" | "Failed" | "Interrupted";
  stages: PipelineStage[];
  created_at: string;
  updated_at: string;
}

export interface Service {
  id: number;
  name: string;
  status: "Healthy" | "Degraded" | "Down";
  uptime: number;
  version: string;
  repo?: string;
  branch?: string;
  app_url?: string | null;
}

export interface ActivityItem {
  id: number;
  title: string;
  description: string;
  icon: string;
  created_at: string;
}

export interface LiveLog {
  id: number;
  level: "INFO" | "WARN" | "ERROR";
  message: string;
  created_at: string;
}

export interface ApiPerformance {
  response_time_ms: number;
  response_time_change: number;
  request_volume: number;
  request_volume_change: number;
  error_rate: number;
  error_rate_change: number;
  series_latency?: number[];
  series_volume?: number[];
  series_errors?: number[];
  range?: "24h" | "7d" | "30d";
  has_comparison?: boolean;
}

export interface InfraMetric {
  metric: "cpu" | "memory" | "network" | "requests";
  values: number[];
  current: number;
}

export interface GithubRepo {
  full_name: string;
  name: string;
  description?: string;
  default_branch: string;
  private: boolean;
  archived?: boolean;
  fork?: boolean;
  language?: string;
  html_url?: string;
  pushed_at?: string;
  stargazers_count?: number;
  open_issues_count?: number;
}
