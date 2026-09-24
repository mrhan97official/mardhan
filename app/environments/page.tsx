"use client";

import AppShell from "@/components/AppShell";
import EnvironmentStatus from "@/components/EnvironmentStatus";
import { useOfflineData } from "@/lib/useOfflineData";
import { fallbackEnvironments } from "@/lib/fallbackData";
import type { Environment } from "@/lib/types";

export default function EnvironmentsPage() {
  const environments = useOfflineData<Environment[]>("environments", "/api/environments", fallbackEnvironments, 30000);

  return (
    <AppShell title="Environments" subtitle="Health and version per environment" isOffline={environments.isOffline}>
      <EnvironmentStatus environments={environments.data} />
    </AppShell>
  );
}
