"use client";

import AppShell from "@/components/AppShell";
import DeploymentPipeline from "@/components/DeploymentPipeline";
import { useOfflineData } from "@/lib/useOfflineData";
import { fallbackPipeline } from "@/lib/fallbackData";
import type { PipelineStage } from "@/lib/types";

export default function DeploymentsPage() {
  const pipeline = useOfflineData<PipelineStage[]>("pipeline", "/api/deployments", fallbackPipeline, 5000);

  return (
    <AppShell title="Deployments" subtitle="Pipeline stages and rollout status" isOffline={pipeline.isOffline}>
      <DeploymentPipeline stages={pipeline.data} onDeployed={pipeline.reload} loading={pipeline.loading} error={pipeline.error} isOffline={pipeline.isOffline} />
    </AppShell>
  );
}
