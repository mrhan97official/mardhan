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
  const [sidebarExpanded, setSidebarExpanded] = useState(false);

  return (
    <div className="app-shell fixed inset-0 flex w-full overflow-hidden bg-base-950">
      <Sidebar
        open={sidebarOpen}
        onClose={() => setSidebarOpen(false)}
        onToggle={() => setSidebarExpanded((current) => !current)}
        expanded={sidebarExpanded}
      />

      <div className="flex min-w-0 flex-1 flex-col overflow-hidden">
        <Header
          onMenuClick={() => setSidebarOpen(true)}
          isOffline={isOffline}
          title={title}
          subtitle={subtitle}
        />

        <main className="min-h-0 flex-1 space-y-2 overflow-y-auto overscroll-y-contain p-2 pb-[calc(0.5rem+env(safe-area-inset-bottom))] sm:space-y-3">
          {children}
        </main>
      </div>
    </div>
  );
}
