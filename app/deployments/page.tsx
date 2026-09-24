"use client";

import AppShell from "@/components/AppShell";
import DeploymentPipeline from "@/components/DeploymentPipeline";
import { useOfflineData } from "@/lib/useOfflineData";
import type { DeploymentJob } from "@/lib/types";

export default function DeploymentsPage() {
  const pipeline = useOfflineData<DeploymentJob[]>("deployment-jobs-v16", "/api/deployments", [], 5000);

  return (
    <AppShell title="Deployments" subtitle="Pipeline stages and rollout status" isOffline={pipeline.isOffline}>
      <DeploymentPipeline jobs={pipeline.data} onDeployed={pipeline.reload} loading={pipeline.loading} error={pipeline.error} isOffline={pipeline.isOffline} />
    </AppShell>
  );
}
