"use client";

import AppShell from "@/components/AppShell";
import ServicesTable from "@/components/ServicesTable";
import { useOfflineData } from "@/lib/useOfflineData";
import { fallbackServices } from "@/lib/fallbackData";
import type { Service } from "@/lib/types";

export default function ContainersPage() {
  const services = useOfflineData<Service[]>("services", "/api/services", fallbackServices, 10000);

  return (
    <AppShell title="Containers" subtitle="Running services and their status" isOffline={services.isOffline}>
      <ServicesTable services={services.data} />
    </AppShell>
  );
}
