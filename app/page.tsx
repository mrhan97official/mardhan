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
  fallbackEnvironments,
  fallbackInfra,
  fallbackLogs,
  fallbackOverview,
  fallbackPipeline,
  fallbackServices,
} from "@/lib/fallbackData";
import type {
  ActivityItem,
  ApiPerformance,
  Environment,
  InfraMetric,
  LiveLog,
  OverviewStats,
  PipelineStage,
  Service,
} from "@/lib/types";

export default function OverviewPage() {
  const overview = useOfflineData<OverviewStats>("overview", "/api/overview", fallbackOverview, 30000);
  const pipeline = useOfflineData<PipelineStage[]>("pipeline", "/api/deployments", fallbackPipeline, 5000);
  const environments = useOfflineData<Environment[]>("environments", "/api/environments", fallbackEnvironments, 30000);
  const infra = useOfflineData<InfraMetric[]>("infra", "/api/health", fallbackInfra, 15000);
  const perf = useOfflineData<ApiPerformance>("api-checks-v12", "/api/performance", fallbackApiPerformance, 30000);
  const services = useOfflineData<Service[]>("services", "/api/services", fallbackServices, 30000);
  const activity = useOfflineData<ActivityItem[]>("activity", "/api/activity", fallbackActivity, 30000);
  const logs = useOfflineData<LiveLog[]>("logs", "/api/logs", fallbackLogs, 10000);

  const anyOffline = [overview, pipeline, environments, infra, perf, services, activity, logs].some(
    (d) => d.isOffline
  );

  return (
    <AppShell title="Overview" subtitle="Infrastructure & deployment workspace" isOffline={anyOffline}>
      <StatCards stats={overview.data} />

      <div className="grid grid-cols-1 gap-4 sm:gap-6 xl:grid-cols-[1.6fr_1fr]">
        <DeploymentPipeline stages={pipeline.data} onDeployed={pipeline.reload} loading={pipeline.loading} error={pipeline.error} isOffline={pipeline.isOffline} />
        <EnvironmentStatus environments={environments.data} />
      </div>

      <div className="grid grid-cols-1 gap-4 sm:gap-6 xl:grid-cols-[1.6fr_1fr]">
        <InfraHealth metrics={infra.data} />
        <ApiPerformancePanel perf={perf.data} />
      </div>

      <div className="grid grid-cols-1 gap-4 sm:gap-6 lg:grid-cols-2 xl:grid-cols-3">
        <ServicesTable services={services.data} />
        <RecentActivity items={activity.data} />
        <div className="lg:col-span-2 xl:col-span-1">
          <LiveLogs logs={logs.data} />
        </div>
      </div>
    </AppShell>
  );
}
