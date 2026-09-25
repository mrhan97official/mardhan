"use client";

import AppShell from "@/components/AppShell";
import EnvironmentStatus from "@/components/EnvironmentStatus";
import { useOfflineData } from "@/lib/useOfflineData";
import type { Environment } from "@/lib/types";

export default function EnvironmentsPage() {
  const environments = useOfflineData<Environment[]>("vercel-environments-v30", "/api/environments", [], 30000);

  return (
    <AppShell title="Environments" subtitle="Health and version per environment" isOffline={environments.isOffline}>
      <EnvironmentStatus environments={environments.data} loading={environments.loading} error={environments.error} updatedAt={environments.updatedAt} onRefresh={environments.reload} />
    </AppShell>
  );
}
