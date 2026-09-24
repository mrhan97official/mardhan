"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  Boxes,
  Cloud,
  Cpu,
  Database,
  FolderKanban,
  Gauge,
  Globe,
  Layers,
  LayoutGrid,
  ScrollText,
  Settings,
  ShieldCheck,
  Users,
  X,
} from "lucide-react";

const NAV_ITEMS = [
  { label: "Overview", icon: LayoutGrid, href: "/" },
  { label: "Projects", icon: FolderKanban, href: "/projects" },
  { label: "Deployments", icon: Boxes, href: "/deployments" },
  { label: "Environments", icon: Layers, href: "/environments" },
  { label: "API Management", icon: Globe, href: "/api-management" },
  { label: "Databases", icon: Database, href: "/databases" },
  { label: "Containers", icon: Boxes, href: "/containers" },
  { label: "Logs", icon: ScrollText, href: "/logs" },
  { label: "Monitoring", icon: Gauge, href: "/monitoring" },
  { label: "Team", icon: Users, href: "/team" },
  { label: "Settings", icon: Settings, href: "/settings" },
];

export default function Sidebar({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const pathname = usePathname();

  return (
    <>
      {open && (
        <button
          aria-label="Tutup menu"
          onClick={onClose}
          className="fixed inset-0 z-40 bg-black/60 lg:hidden"
        />
      )}
      <aside
        className={`fixed z-50 inset-y-0 left-0 w-64 shrink-0 border-r border-base-border bg-base-900 flex flex-col transition-transform duration-200
        lg:sticky lg:top-0 lg:h-screen lg:translate-x-0
        ${open ? "translate-x-0" : "-translate-x-full"}`}
      >
        <div className="flex items-center justify-between gap-2 px-5 py-5">
          <div className="flex items-center gap-2.5">
            <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-accent-blue/15 text-accent-blue">
              <Cloud size={20} />
            </div>
            <div className="leading-tight">
              <p className="text-[13px] font-semibold tracking-wide text-slate-400">DEV</p>
              <p className="text-sm font-bold text-white -mt-0.5">CONTROL</p>
            </div>
          </div>
          <button
            aria-label="Tutup menu"
            onClick={onClose}
            className="rounded-lg p-1.5 text-slate-400 hover:bg-base-800 lg:hidden"
          >
            <X size={18} />
          </button>
        </div>

        <nav className="flex-1 space-y-1 overflow-y-auto px-3 py-2">
          {NAV_ITEMS.map(({ label, icon: Icon, href }) => {
            const active = href === "/" ? pathname === "/" : pathname === href || pathname?.startsWith(`${href}/`);
            return (
              <Link
                key={label}
                href={href}
                onClick={onClose}
                className={`flex items-center gap-3 rounded-xl px-3 py-2.5 text-sm font-medium transition-colors
                  ${
                    active
                      ? "bg-accent-blue/15 text-accent-blue"
                      : "text-slate-400 hover:bg-base-800 hover:text-slate-200"
                  }`}
              >
                <Icon size={18} />
                {label}
              </Link>
            );
          })}
        </nav>

        <div className="m-3 rounded-xl border border-base-border bg-base-800/70 p-3">
          <div className="flex items-center gap-2 text-sm font-medium text-emerald-400">
            <ShieldCheck size={16} />
            Panel Admin
          </div>
          <p className="mt-1 flex items-center gap-1 text-xs text-slate-500">
            <Cpu size={12} /> v1.0.13
          </p>
        </div>
      </aside>
    </>
  );
}
