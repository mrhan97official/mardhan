"use client";

import { useState } from "react";
import Sidebar from "@/components/Sidebar";
import Header from "@/components/Header";

export default function AppShell({
  title,
  subtitle,
  isOffline = false,
  children,
}: {
  title: string;
  subtitle?: string;
  isOffline?: boolean;
  children: React.ReactNode;
}) {
  const [sidebarOpen, setSidebarOpen] = useState(false);

  return (
    <div className="flex h-screen overflow-hidden bg-base-950">
      <Sidebar open={sidebarOpen} onClose={() => setSidebarOpen(false)} />

      <div className="flex min-w-0 flex-1 flex-col overflow-hidden">
        <Header
          onMenuClick={() => setSidebarOpen(true)}
          isOffline={isOffline}
          title={title}
          subtitle={subtitle}
        />

        <main className="flex-1 overflow-y-auto space-y-4 p-4 pb-[calc(1rem+env(safe-area-inset-bottom))] sm:space-y-6 sm:p-6">
          {children}
        </main>
      </div>
    </div>
  );
}
