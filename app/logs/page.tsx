"use client";

import AppShell from "@/components/AppShell";
import LiveLogs from "@/components/LiveLogs";
import { useOfflineData } from "@/lib/useOfflineData";
import { fallbackLogs } from "@/lib/fallbackData";
import type { LiveLog } from "@/lib/types";

export default function LogsPage() {
  const logs = useOfflineData<LiveLog[]>("logs", "/api/logs", fallbackLogs, 10000);

  return (
    <AppShell title="Logs" subtitle="Live application and deployment logs" isOffline={logs.isOffline}>
      <LiveLogs logs={logs.data} />
    </AppShell>
  );
}
