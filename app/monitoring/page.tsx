"use client";

import AppShell from "@/components/AppShell";
import InfraHealth from "@/components/InfraHealth";
import { useOfflineData } from "@/lib/useOfflineData";
import { fallbackInfra } from "@/lib/fallbackData";
import type { InfraMetric } from "@/lib/types";

export default function MonitoringPage() {
  const infra = useOfflineData<InfraMetric[]>("infra", "/api/health", fallbackInfra, 15000);

  return (
    <AppShell title="Monitoring" subtitle="CPU, memory, network, and request metrics" isOffline={infra.isOffline}>
      <InfraHealth metrics={infra.data} />
    </AppShell>
  );
}
