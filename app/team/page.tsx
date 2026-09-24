"use client";

import AppShell from "@/components/AppShell";
import RecentActivity from "@/components/RecentActivity";
import { useOfflineData } from "@/lib/useOfflineData";
import { fallbackActivity } from "@/lib/fallbackData";
import type { ActivityItem } from "@/lib/types";

export default function TeamPage() {
  const activity = useOfflineData<ActivityItem[]>("activity", "/api/activity", fallbackActivity, 30000);

  return (
    <AppShell title="Team" subtitle="Recent team and account activity" isOffline={activity.isOffline}>
      <RecentActivity items={activity.data} />
    </AppShell>
  );
}
