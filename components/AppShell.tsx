"use client";

import { useState } from "react";
import { usePathname } from "next/navigation";
import { ShieldAlert } from "lucide-react";
import Sidebar from "@/components/Sidebar";
import { isAdminRole, useSession } from "@/lib/session";
import Header from "@/components/Header";

const ADMIN_PAGES = ["/settings", "/databases", "/api-management"];

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
  const pathname = usePathname() ?? "/";
  const { role } = useSession();
  // Opening an admin-only page by URL shows a clear notice instead of empty
  // cards and "sesi admin diperlukan" errors.
  const restricted = ADMIN_PAGES.some((page) => pathname === page || pathname.startsWith(`${page}/`)) && !isAdminRole(role);

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

        <main className="min-h-0 flex-1 space-y-2 overflow-y-auto overscroll-y-contain p-2 pb-[calc(0.5rem+env(safe-area-inset-bottom))]">
          {restricted ? (
            <section className="card flex items-start gap-2 p-2 text-sm text-slate-300">
              <ShieldAlert size={18} className="mt-0.5 shrink-0 text-amber-300" />
              <span>Halaman ini khusus owner/admin. Hubungi owner jika Anda membutuhkan akses.</span>
            </section>
          ) : children}
        </main>
      </div>
    </div>
  );
}
