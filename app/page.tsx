"use client";

import AppShell from "@/components/AppShell";
import StatCards from "@/components/StatCards";
import DeploymentPipeline from "@/components/DeploymentPipeline";
import EnvironmentStatus from "@/components/EnvironmentStatus";
import InfraHealth from "@/components/InfraHealth";
import ApiPerformancePanel from "@/components/ApiPerformancePanel";
import ServicesTable from "@/components/ServicesTable";
import RecentActivity from "@/components/RecentActivity";
import LiveLogs from "@/components/LiveLogs";
import { useOfflineData } from "@/lib/useOfflineData";
import {
  fallbackActivity,
  fallbackApiPerformance,
  fallbackInfra,
  fallbackLogs,
  fallbackOverview,
  fallbackServices,
} from "@/lib/fallbackData";
import type {
  ActivityItem,
  ApiPerformance,
  Environment,
  InfraMetric,
  LiveLog,
  OverviewStats,
  DeploymentJob,
  Service,
} from "@/lib/types";

export default function OverviewPage() {
  const overview = useOfflineData<OverviewStats>("overview", "/api/overview", fallbackOverview, 10000);
  const pipeline = useOfflineData<DeploymentJob[]>("deployment-jobs-v16", "/api/deployments", [], 5000);
  const environments = useOfflineData<Environment[]>("vercel-environments-v30", "/api/environments", [], 30000);
  const infra = useOfflineData<InfraMetric[]>("infra-live-v27", "/api/health", fallbackInfra, 15000);
  const perf = useOfflineData<ApiPerformance>("api-checks-v12", "/api/performance", fallbackApiPerformance, 30000);
  const services = useOfflineData<Service[]>("services", "/api/services", fallbackServices, 10000);
  const activity = useOfflineData<ActivityItem[]>("activity", "/api/activity", fallbackActivity, 30000);
  const logs = useOfflineData<LiveLog[]>("logs", "/api/logs", fallbackLogs, 10000);

  const anyOffline = [overview, pipeline, environments, infra, perf, services, activity, logs].some(
    (d) => d.isOffline
  );

  return (
    <AppShell title="Overview" subtitle="Infrastructure & deployment workspace" isOffline={anyOffline}>
      <StatCards stats={overview.data} />

      <div className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)] md:gap-3 xl:gap-6">
        <DeploymentPipeline jobs={pipeline.data} limit={3} onDeployed={pipeline.reload} error={pipeline.error} />
        <EnvironmentStatus environments={environments.data} loading={environments.loading} error={environments.error} updatedAt={environments.updatedAt} viewAll />
      </div>

      <div className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,2.3fr)_minmax(0,1fr)] md:gap-3 xl:gap-6">
        <InfraHealth metrics={infra.data} />
        <ApiPerformancePanel perf={perf.data} />
      </div>

      <div className="grid grid-cols-1 gap-4 md:grid-cols-3 md:gap-3 xl:gap-6">
        <ServicesTable services={services.data} />
        <RecentActivity items={activity.data} />
        <div className="min-w-0">
          <LiveLogs logs={logs.data} />
        </div>
      </div>
    </AppShell>
  );
}
