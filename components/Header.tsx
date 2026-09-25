"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import {
  Bell,
  ChevronDown,
  LogOut,
  Menu,
  PanelLeftClose,
  PanelLeftOpen,
  Search,
  Settings,
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
  const [profileOpen, setProfileOpen] = useState(false);
  const [loggingOut, setLoggingOut] = useState(false);
  const [profileError, setProfileError] = useState("");
  const profileRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!profileOpen) return;
    const closeOutside = (event: PointerEvent) => {
      if (!profileRef.current?.contains(event.target as Node)) setProfileOpen(false);
    };
    const closeEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") { setProfileOpen(false); profileRef.current?.querySelector("button")?.focus(); }
    };
    document.addEventListener("pointerdown", closeOutside);
    document.addEventListener("keydown", closeEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOutside);
      document.removeEventListener("keydown", closeEscape);
    };
  }, [profileOpen]);

  async function logout() {
    setLoggingOut(true);
    setProfileError("");
    try {
      const response = await fetch("/api/session", { method: "DELETE", credentials: "same-origin", cache: "no-store" });
      if (!response.ok) throw new Error();
      setProfileOpen(false);
      window.dispatchEvent(new Event("devcontrol:logout"));
    } catch {
      setProfileError("Gagal keluar. Periksa koneksi, lalu coba lagi.");
    } finally { setLoggingOut(false); }
  }

  return (
    <header className="sticky top-0 z-30 border-b border-base-border bg-base-950/85 backdrop-blur px-4 pb-3 pt-[calc(0.75rem+env(safe-area-inset-top,0px))] sm:px-6">
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
          <h1 className="text-xl font-bold text-white md:text-base xl:text-xl">{title}</h1>
          <p className="text-xs text-slate-500 md:hidden xl:block">{subtitle}</p>
        </div>

        <div className="ml-auto flex flex-1 items-center justify-end gap-2 sm:gap-3">
          <div className="relative hidden w-full max-w-xs md:block md:min-w-0 md:max-w-[9rem] lg:max-w-[12rem] xl:max-w-xs">
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

          <button className="hidden items-center gap-2 rounded-xl border border-base-border bg-base-850 px-3 py-2 text-sm text-slate-300 hover:bg-base-800 md:flex md:px-2 md:text-xs xl:px-3 xl:text-sm">
            <span className="xl:hidden">Prod</span><span className="hidden xl:inline">Production Workspace</span>
            <ChevronDown size={14} />
          </button>

          <div className="hidden items-center gap-1.5 rounded-xl border border-base-border bg-base-850 px-3 py-2 text-sm md:flex md:px-2 md:text-xs xl:px-3 xl:text-sm">
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
            className="relative p-2.5 text-slate-300 transition-colors hover:text-white"
          >
            <Bell size={16} />
            <span className="absolute right-1.5 top-1.5 h-1.5 w-1.5 rounded-full bg-red-500" />
          </button>

          <div ref={profileRef} className="relative" onBlur={(event) => {
            if (!event.currentTarget.contains(event.relatedTarget)) setProfileOpen(false);
          }}>
            <button
              type="button"
              aria-label="Profil Admin"
              aria-expanded={profileOpen}
              aria-haspopup="menu"
              onClick={() => { setProfileError(""); setProfileOpen((open) => !open); }}
              className="flex items-center gap-2 px-1 py-1 text-slate-200 transition-colors hover:text-white"
            >
              <span className="flex h-7 w-7 items-center justify-center rounded-full bg-accent-blue/20 text-xs font-semibold text-accent-blue">A</span>
              <span className="hidden text-sm font-medium sm:inline md:hidden xl:inline">Admin</span>
              <ChevronDown size={14} className={`transition-transform ${profileOpen ? "rotate-180" : ""}`} />
            </button>
            {profileOpen && (
              <div role="menu" aria-label="Menu profil" className="absolute right-0 top-full z-40 mt-2 w-56 rounded-xl border border-base-border bg-base-900 p-1.5 text-sm shadow-2xl">
                <div className="border-b border-base-border px-3 py-2.5">
                  <p className="font-semibold text-white">Admin</p>
                  <p className="text-xs text-slate-400">Administrator</p>
                </div>
                <Link href="/settings" role="menuitem" onClick={() => setProfileOpen(false)} className="mt-1 flex items-center gap-2.5 rounded-lg px-3 py-2.5 text-slate-200 hover:bg-base-800">
                  <Settings size={16} /> Pengaturan
                </Link>
                <button type="button" role="menuitem" disabled={loggingOut} onClick={() => void logout()} className="flex w-full items-center gap-2.5 rounded-lg px-3 py-2.5 text-left text-red-300 hover:bg-red-500/10 disabled:opacity-50">
                  <LogOut size={16} /> {loggingOut ? "Keluar…" : "Keluar"}
                </button>
                {profileError && <p role="alert" className="px-3 py-2 text-xs text-red-300">{profileError}</p>}
              </div>
            )}
          </div>
        </div>
      </div>
    </header>
  );
}
