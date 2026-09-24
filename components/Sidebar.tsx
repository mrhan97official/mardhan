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
  expanded,
}: {
  open: boolean;
  onClose: () => void;
  expanded: boolean;
}) {
  const pathname = usePathname();

  return (
    <>
      {open && (
        <button
          aria-label="Tutup menu"
          onClick={onClose}
          className="fixed inset-0 z-40 bg-black/60 md:hidden"
        />
      )}
      <aside
        className={`fixed inset-y-0 left-0 z-50 flex w-64 shrink-0 flex-col border-r border-base-border bg-base-900 transition-[transform,width] duration-200
        md:sticky md:top-0 md:h-screen md:translate-x-0 ${expanded ? "md:w-64" : "md:w-20"}
        ${open ? "translate-x-0" : "-translate-x-full"}`}
      >
        <div
          className={`flex items-center justify-between gap-2 px-5 py-5 ${
            expanded ? "md:px-5" : "md:justify-center md:px-3"
          }`}
        >
          <div className="flex items-center gap-2.5">
            <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-accent-blue/15 text-accent-blue">
              <Cloud size={20} />
            </div>
            <div className={`leading-tight ${expanded ? "md:block" : "md:hidden"}`}>
              <p className="text-[13px] font-semibold tracking-wide text-slate-400">DEV</p>
              <p className="text-sm font-bold text-white -mt-0.5">CONTROL</p>
            </div>
          </div>
          <button
            aria-label="Tutup menu"
            onClick={onClose}
            className="rounded-lg p-1.5 text-slate-400 hover:bg-base-800 md:hidden"
          >
            <X size={18} />
          </button>
        </div>

        <nav className="flex-1 space-y-1 overflow-y-auto px-3 py-2 md:overflow-visible">
          {NAV_ITEMS.map(({ label, icon: Icon, href }) => {
            const active = href === "/" ? pathname === "/" : pathname === href || pathname?.startsWith(`${href}/`);
            return (
              <Link
                key={label}
                href={href}
                onClick={onClose}
                aria-label={label}
                className={`group relative flex items-center gap-3 rounded-xl py-2.5 text-sm font-medium transition-colors ${
                  expanded ? "px-3 md:justify-start" : "px-3 md:justify-center md:px-0"
                }
                  ${
                    active
                      ? "bg-accent-blue/15 text-accent-blue"
                      : "text-slate-400 hover:bg-base-800 hover:text-slate-200"
                  }`}
              >
                <Icon size={18} className="shrink-0" />
                <span className={`whitespace-nowrap ${expanded ? "md:inline" : "md:hidden"}`}>
                  {label}
                </span>
                {!expanded && (
                  <span
                    aria-hidden="true"
                    className="pointer-events-none invisible absolute left-full top-1/2 z-[60] ml-3 hidden -translate-y-1/2 whitespace-nowrap rounded-lg border border-base-border bg-base-800 px-3 py-2 text-xs font-semibold text-slate-100 opacity-0 shadow-xl transition-opacity before:absolute before:left-0 before:top-1/2 before:h-2 before:w-2 before:-translate-x-1/2 before:-translate-y-1/2 before:rotate-45 before:border-b before:border-l before:border-base-border before:bg-base-800 md:block md:group-hover:visible md:group-hover:opacity-100 md:group-focus-visible:visible md:group-focus-visible:opacity-100"
                  >
                    {label}
                  </span>
                )}
              </Link>
            );
          })}
        </nav>

        <div
          className={`group relative m-3 rounded-xl border border-base-border bg-base-800/70 p-3 ${
            expanded ? "" : "md:flex md:justify-center"
          }`}
        >
          <div className="flex items-center gap-2 text-sm font-medium text-emerald-400">
            <ShieldCheck size={16} className="shrink-0" />
            <span className={expanded ? "md:inline" : "md:hidden"}>Panel Admin</span>
          </div>
          <p className={`mt-1 items-center gap-1 text-xs text-slate-500 ${expanded ? "flex" : "flex md:hidden"}`}>
            <Cpu size={12} /> v1.0.15
          </p>
          {!expanded && (
            <span
              aria-hidden="true"
              className="pointer-events-none invisible absolute left-full top-1/2 z-[60] ml-3 hidden -translate-y-1/2 whitespace-nowrap rounded-lg border border-base-border bg-base-800 px-3 py-2 text-xs font-semibold text-slate-100 opacity-0 shadow-xl transition-opacity before:absolute before:left-0 before:top-1/2 before:h-2 before:w-2 before:-translate-x-1/2 before:-translate-y-1/2 before:rotate-45 before:border-b before:border-l before:border-base-border before:bg-base-800 md:block md:group-hover:visible md:group-hover:opacity-100"
            >
              Panel Admin · v1.0.15
            </span>
          )}
        </div>
      </aside>
    </>
  );
}
