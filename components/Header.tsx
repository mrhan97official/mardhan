"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import SidebarLogo from "@/components/SidebarLogo";
import { useTheme } from "@/components/ThemeProvider";
import type { DeploymentJob } from "@/lib/types";
import {
  Bell,
  Check,
  ChevronDown,
  Clock3,
  LogOut,
  Moon,
  Search,
  Settings,
  Sun,
  X,
} from "lucide-react";

const DEPLOYMENT_LABEL: Record<DeploymentJob["kind"], string> = {
  new_app: "Aplikasi baru",
  update_app: "Update aplikasi",
  self_update: "Update diri",
};

const STATUS_LABEL: Record<DeploymentJob["status"], string> = {
  Running: "Sedang berjalan",
  Success: "Berhasil",
  Failed: "Gagal",
  Interrupted: "Terputus",
};

export default function Header({
  onMenuClick,
  isOffline,
  title = "Overview",
  subtitle = "Infrastructure & deployment workspace",
}: {
  onMenuClick: () => void;
  isOffline: boolean;
  title?: string;
  subtitle?: string;
}) {
  const { theme, setTheme } = useTheme();
  const [profileOpen, setProfileOpen] = useState(false);
  const [notificationsOpen, setNotificationsOpen] = useState(false);
  const [notificationJobs, setNotificationJobs] = useState<DeploymentJob[]>([]);
  const [notificationLoading, setNotificationLoading] = useState(true);
  const [notificationError, setNotificationError] = useState(false);
  const [loggingOut, setLoggingOut] = useState(false);
  const [profileError, setProfileError] = useState("");
  const profileRef = useRef<HTMLDivElement>(null);
  const notificationRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!profileOpen && !notificationsOpen) return;
    const closeOutside = (event: PointerEvent) => {
      if (profileOpen && !profileRef.current?.contains(event.target as Node)) setProfileOpen(false);
      if (notificationsOpen && !notificationRef.current?.contains(event.target as Node)) setNotificationsOpen(false);
    };
    const closeEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      if (profileOpen) profileRef.current?.querySelector("button")?.focus();
      if (notificationsOpen) notificationRef.current?.querySelector("button")?.focus();
      setProfileOpen(false);
      setNotificationsOpen(false);
    };
    document.addEventListener("pointerdown", closeOutside);
    document.addEventListener("keydown", closeEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOutside);
      document.removeEventListener("keydown", closeEscape);
    };
  }, [profileOpen, notificationsOpen]);

  useEffect(() => {
    let active = true;
    let requestId = 0;
    async function loadNotifications() {
      const currentRequest = ++requestId;
      try {
        const response = await fetch("/api/deployments", { cache: "no-store", credentials: "same-origin" });
        if (!response.ok) throw new Error("Gagal mengambil deployment");
        const jobs: unknown = await response.json();
        if (!Array.isArray(jobs)) throw new Error("Respons deployment tidak valid");
        if (active && currentRequest === requestId) { setNotificationJobs(jobs as DeploymentJob[]); setNotificationError(false); }
      } catch {
        if (active && currentRequest === requestId) setNotificationError(true);
      } finally {
        if (active && currentRequest === requestId) setNotificationLoading(false);
      }
    }
    const onVisible = () => { if (document.visibilityState === "visible") void loadNotifications(); };
    void loadNotifications();
    const timer = window.setInterval(onVisible, 15_000);
    window.addEventListener("deployment:changed", onVisible);
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      active = false;
      requestId++;
      window.clearInterval(timer);
      window.removeEventListener("deployment:changed", onVisible);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, []);

  const hasFailedDeployment = notificationJobs.some((job) => job.status === "Failed" || job.status === "Interrupted");
  const hasRunningDeployment = notificationJobs.some((job) => job.status === "Running");

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
    <header className="app-header relative z-30 shrink-0 bg-base-950/85 backdrop-blur">
      <div className="border-y border-base-border px-2 py-3">
        <div className="flex items-center gap-3">
        <div className="md:hidden">
          <SidebarLogo expanded={false} onClick={onMenuClick} label="Buka menu" />
        </div>

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

          <div ref={notificationRef} className="relative" onBlur={(event) => {
            if (!event.currentTarget.contains(event.relatedTarget)) setNotificationsOpen(false);
          }}>
            <button
              type="button"
              aria-label="Notifikasi deployment"
              aria-expanded={notificationsOpen}
              aria-haspopup="menu"
              onClick={() => { setProfileOpen(false); setNotificationsOpen((open) => !open); }}
              className="relative p-2.5 text-slate-300 transition-colors hover:text-white"
            >
              <Bell size={16} />
              {(hasFailedDeployment || hasRunningDeployment) && (
                <span aria-hidden="true" className={`absolute right-1.5 top-1.5 h-1.5 w-1.5 rounded-full ${hasFailedDeployment ? "bg-red-500" : "bg-purple-400"}`} />
              )}
            </button>
            {notificationsOpen && (
              <div role="menu" aria-label="Notifikasi deployment" className="absolute -right-12 top-full z-40 mt-2 w-[min(20rem,calc(100vw-2rem))] rounded-xl border border-base-border bg-base-900 p-1.5 text-sm shadow-2xl">
                <span aria-hidden="true" className="pointer-events-none absolute -top-[5px] right-[3.75rem] h-2.5 w-2.5 rotate-45 border-l border-t border-base-border bg-base-900" />
                <p className="border-b border-base-border px-3 py-2 font-semibold text-white">Notifikasi deployment</p>
                {notificationJobs.length === 0 && (
                  <p className="px-3 py-4 text-xs text-slate-400">
                    {notificationLoading ? "Memuat status…" : notificationError ? "Status belum bisa dimuat. Coba buka lagi nanti." : "Belum ada deployment terbaru."}
                  </p>
                )}
                {notificationJobs.slice(0, 4).map((job) => (
                  <Link key={job.id} href="/deployments" role="menuitem" onClick={() => setNotificationsOpen(false)} className="flex items-start gap-2.5 rounded-lg px-3 py-2.5 hover:bg-base-800">
                    <span className={`mt-0.5 ${job.status === "Success" ? "text-emerald-400" : job.status === "Running" ? "text-purple-400" : "text-amber-400"}`}>
                      {job.status === "Success" ? <Check size={16} /> : job.status === "Running" ? <Clock3 size={16} /> : <X size={16} />}
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-xs font-medium text-slate-100">{DEPLOYMENT_LABEL[job.kind]} · {job.target}</span>
                      <span className="mt-0.5 block text-[11px] text-slate-400">{STATUS_LABEL[job.status]}</span>
                    </span>
                  </Link>
                ))}
                {notificationError && notificationJobs.length > 0 && (
                  <p className="px-3 py-1 text-[11px] text-amber-300">Status terbaru belum bisa diperbarui.</p>
                )}
                <Link href="/deployments" role="menuitem" onClick={() => setNotificationsOpen(false)} className="mt-1 block border-t border-base-border px-3 py-2.5 text-xs font-medium text-accent-blue hover:text-blue-400">
                  Lihat pipeline
                </Link>
              </div>
            )}
          </div>

          <div ref={profileRef} className="relative" onBlur={(event) => {
            if (!event.currentTarget.contains(event.relatedTarget)) setProfileOpen(false);
          }}>
            <button
              type="button"
              aria-label={`Profil Admin, ${isOffline ? "offline" : "online"}`}
              aria-expanded={profileOpen}
              aria-haspopup="menu"
              onClick={() => { setNotificationsOpen(false); setProfileError(""); setProfileOpen((open) => !open); }}
              className="flex items-center gap-2 px-1 py-1 text-slate-200 transition-colors hover:text-white"
            >
              <span className="relative flex h-7 w-7 items-center justify-center rounded-full bg-accent-blue/20 text-xs font-semibold text-accent-blue">
                A
                <span aria-hidden="true" title={isOffline ? "Offline" : "Online"} className={`absolute -bottom-0.5 -right-0.5 h-2.5 w-2.5 rounded-full border-2 border-base-950 ${isOffline ? "bg-amber-400" : "bg-emerald-400"}`} />
              </span>
              <span className="hidden text-sm font-medium sm:inline md:hidden xl:inline">Admin</span>
              <ChevronDown size={14} className={`transition-transform ${profileOpen ? "rotate-180" : ""}`} />
            </button>
            {profileOpen && (
              <div role="menu" aria-label="Menu profil" className="absolute right-0 top-full z-40 mt-2 w-56 rounded-xl border border-base-border bg-base-900 p-1.5 text-sm shadow-2xl">
                <span aria-hidden="true" className="pointer-events-none absolute -top-[5px] right-9 h-2.5 w-2.5 rotate-45 border-l border-t border-base-border bg-base-900 sm:right-[4.75rem] md:right-9 xl:right-[4.75rem]" />
                <div className="border-b border-base-border px-3 py-2.5">
                  <p className="font-semibold text-white">Admin</p>
                  <p className="text-xs text-slate-400">Administrator</p>
                </div>
                <Link href="/settings" role="menuitem" onClick={() => setProfileOpen(false)} className="mt-1 flex items-center gap-2.5 rounded-lg px-3 py-2.5 text-slate-200 hover:bg-base-800">
                  <Settings size={16} /> Pengaturan
                </Link>
                <div role="group" aria-label="Mode tampilan" className="border-t border-base-border px-2.5 py-2">
                  <p className="mb-1.5 px-1 text-xs text-slate-400">Mode tampilan</p>
                  <div className="grid grid-cols-2 gap-1 rounded-lg bg-base-800/70 p-1">
                    <button type="button" role="menuitemradio" aria-checked={theme === "light"} onClick={() => setTheme("light")} className={`flex items-center justify-center gap-1.5 rounded-md px-2 py-1.5 text-xs font-medium ${theme === "light" ? "bg-base-900 text-accent-blue shadow-sm" : "text-slate-400 hover:text-slate-200"}`}><Sun size={14} /> Terang</button>
                    <button type="button" role="menuitemradio" aria-checked={theme === "dark"} onClick={() => setTheme("dark")} className={`flex items-center justify-center gap-1.5 rounded-md px-2 py-1.5 text-xs font-medium ${theme === "dark" ? "bg-base-900 text-accent-blue shadow-sm" : "text-slate-400 hover:text-slate-200"}`}><Moon size={14} /> Gelap</button>
                  </div>
                </div>
                <button type="button" role="menuitem" disabled={loggingOut} onClick={() => void logout()} className="flex w-full items-center gap-2.5 rounded-lg px-3 py-2.5 text-left text-red-300 hover:bg-red-500/10 disabled:opacity-50">
                  <LogOut size={16} /> {loggingOut ? "Keluar…" : "Keluar"}
                </button>
                {profileError && <p role="alert" className="px-3 py-2 text-xs text-red-300">{profileError}</p>}
              </div>
            )}
          </div>
        </div>
        </div>
      </div>
    </header>
  );
}
