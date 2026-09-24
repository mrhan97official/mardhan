"use client";

import {
  Bell,
  ChevronDown,
  LogOut,
  Menu,
  PanelLeftClose,
  PanelLeftOpen,
  Search,
  WifiOff,
} from "lucide-react";

export default function Header({
  onMenuClick,
  onSidebarToggle,
  sidebarExpanded,
  isOffline,
  title = "Overview",
  subtitle = "Infrastructure & deployment workspace",
}: {
  onMenuClick: () => void;
  onSidebarToggle: () => void;
  sidebarExpanded: boolean;
  isOffline: boolean;
  title?: string;
  subtitle?: string;
}) {
  return (
    <header className="sticky top-0 z-30 border-b border-base-border bg-base-950/85 backdrop-blur px-4 py-3 sm:px-6">
      <div className="flex items-center gap-3">
        <button
          aria-label="Buka menu"
          onClick={onMenuClick}
          className="rounded-lg p-2 text-slate-300 hover:bg-base-800 md:hidden"
        >
          <Menu size={20} />
        </button>

        <button
          type="button"
          aria-label={sidebarExpanded ? "Ciutkan sidebar" : "Tampilkan sidebar"}
          title={sidebarExpanded ? "Ciutkan sidebar" : "Tampilkan sidebar"}
          onClick={onSidebarToggle}
          className="hidden rounded-lg p-2 text-slate-300 hover:bg-base-800 md:inline-flex"
        >
          {sidebarExpanded ? <PanelLeftClose size={20} /> : <PanelLeftOpen size={20} />}
        </button>

        <div className="hidden sm:block">
          <h1 className="text-xl font-bold text-white">{title}</h1>
          <p className="text-xs text-slate-500">{subtitle}</p>
        </div>

        <div className="ml-auto flex flex-1 items-center justify-end gap-2 sm:gap-3">
          <div className="relative hidden w-full max-w-xs xl:block">
            <Search
              size={16}
              className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-slate-500"
            />
            <input
              type="text"
              placeholder="Cari proyek, layanan, atau log..."
              className="w-full rounded-xl border border-base-border bg-base-850 py-2 pl-9 pr-3 text-sm text-slate-200 placeholder:text-slate-500 focus:border-accent-blue/60"
            />
          </div>

          <button className="hidden items-center gap-2 rounded-xl border border-base-border bg-base-850 px-3 py-2 text-sm text-slate-300 hover:bg-base-800 xl:flex">
            Production Workspace
            <ChevronDown size={14} />
          </button>

          <div className="hidden items-center gap-1.5 rounded-xl border border-base-border bg-base-850 px-3 py-2 text-sm lg:flex">
            {isOffline ? (
              <>
                <WifiOff size={14} className="text-amber-400" />
                <span className="font-medium text-amber-400">Offline</span>
              </>
            ) : (
              <>
                <span className="h-2 w-2 rounded-full bg-emerald-400" />
                <span className="font-medium text-emerald-400">Online</span>
              </>
            )}
          </div>

          <button
            aria-label="Notifikasi"
            className="relative rounded-xl border border-base-border bg-base-850 p-2.5 text-slate-300 hover:bg-base-800"
          >
            <Bell size={16} />
            <span className="absolute right-1.5 top-1.5 h-1.5 w-1.5 rounded-full bg-red-500" />
          </button>

          <div className="flex items-center gap-2 rounded-xl border border-base-border bg-base-850 px-2.5 py-1.5">
            <div className="flex h-7 w-7 items-center justify-center rounded-full bg-accent-blue/20 text-xs font-semibold text-accent-blue">
              A
            </div>
            <span className="hidden text-sm font-medium text-slate-200 sm:inline">
              Admin
            </span>
          </div>
          <button type="button" aria-label="Keluar" title="Keluar" onClick={async () => {
            try {
              const response = await fetch("/api/session", { method: "DELETE", credentials: "same-origin", cache: "no-store" });
              if (!response.ok) throw new Error();
              window.dispatchEvent(new Event("devcontrol:logout"));
            } catch { window.alert("Gagal keluar. Periksa koneksi, lalu coba lagi."); }
          }} className="rounded-xl border border-base-border p-2 text-slate-300 hover:bg-base-800"><LogOut size={17} /></button>
        </div>
      </div>
    </header>
  );
}
