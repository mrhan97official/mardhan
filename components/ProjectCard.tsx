"use client";

import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import Link from "next/link";
import { AlertCircle, Archive, ExternalLink, FileArchive, GitBranch, Github, History, ImageIcon, Loader2, Lock, MoreVertical, RefreshCw, RotateCcw, ShieldCheck, Star, Trash2, X } from "lucide-react";
import { useDeploymentOverlay } from "@/components/deployment/DeploymentOverlayProvider";
import { UpdateHistoryDialog, ZipArchiveDialog } from "@/components/ProjectDialogs";
import ProjectImagesDialog from "@/components/ProjectImagesDialog";
import { useTheme } from "@/components/ThemeProvider";
import { cardThumbnail, imageURL, type ImageSlot, type ProjectImages } from "@/lib/projectImages";
import { notifyDataChanged } from "@/lib/liveUpdates";
import { canDeploy, canUseApp, isAdminRole, useSession } from "@/lib/session";
import type { GithubRepo } from "@/lib/types";

const LANGUAGE_COLORS: Record<string, string> = {
  TypeScript: "bg-blue-400",
  JavaScript: "bg-yellow-400",
  Go: "bg-cyan-400",
  Python: "bg-emerald-400",
  Rust: "bg-orange-500",
  Java: "bg-red-400",
  HTML: "bg-orange-400",
  CSS: "bg-purple-400",
  Shell: "bg-slate-400",
};

// Compact dropdown row; the menu is narrow so long labels may wrap.
const MENU_ITEM = "flex w-full items-center gap-1.5 rounded-lg px-2 py-1.5 text-left text-xs leading-tight";

function timeAgo(iso?: string): string | null {
  if (!iso) return null;
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return null;
  const seconds = Math.max(0, Math.floor((Date.now() - then) / 1000));
  const units: [number, string][] = [
    [60, "detik"],
    [60, "menit"],
    [24, "jam"],
    [30, "hari"],
    [12, "bulan"],
    [Infinity, "tahun"],
  ];
  let value = seconds;
  let label = "detik";
  for (const [size, unitLabel] of units) {
    if (value < size) {
      label = unitLabel;
      break;
    }
    value = Math.floor(value / size);
    label = unitLabel;
  }
  if (seconds < 60) return "baru saja";
  return `${value} ${label} lalu`;
}

export default function ProjectCard({ repo, displayName, appUrl, linked = false, source = "github", serviceName, updating = false, images = {}, uploading = false, uploadingSlot = null, uploadProgress = null, imageError, onUploadImage, onRemoveImage, onDelete }: {
  repo: GithubRepo;
  displayName?: string;
  appUrl?: string | null;
  linked?: boolean;
  source?: "github" | "stored";
  /** Name of the app's deployment record; set only when it can be updated from here. */
  serviceName?: string;
  /** An "Aplikasi Baru"/"Update Aplikasi" run for this app is in progress. */
  updating?: boolean;
  images?: ProjectImages;
  uploading?: boolean;
  uploadingSlot?: ImageSlot | null;
  uploadProgress?: number | null;
  imageError?: string;
  onUploadImage: (slot: ImageSlot, file: File) => void;
  onRemoveImage: (slot: ImageSlot) => void;
  onDelete: () => void;
}) {
  const menuRef = useRef<HTMLDivElement>(null);
  const [actionsOpen, setActionsOpen] = useState(false);
  const [dialog, setDialog] = useState<"zip" | "history" | "images" | null>(null);
  const { theme } = useTheme();
  // Light mode shows the light thumbnail, dark mode the dark one; with only
  // one of them set, that one is used in both modes.
  const thumbnail = cardThumbnail(images, theme);
  const [repairBroken, setRepairBroken] = useState(false);
  const [repairDialog, setRepairDialog] = useState(false);
  const [repairMode, setRepairMode] = useState<"repair" | "reimport">("repair");
  const [reimportConfirm, setReimportConfirm] = useState("");
  const [reimportAPIURL, setReimportAPIURL] = useState("");
  const [repairBusy, setRepairBusy] = useState(false);
  const [repairTicket, setRepairTicket] = useState<string | null>(null);
  const [repairMessage, setRepairMessage] = useState("");
  const [repairError, setRepairError] = useState("");
  const [repairedURL, setRepairedURL] = useState<string | null>(null);
  const session = useSession();
  const { role } = session;
  const admin = isAdminRole(role);
  const openDeploy = useDeploymentOverlay();
  // Owner, admin and operator deploy apps; only apps already deployed by
  // DevControl (with a saved deployment record) can be updated.
  // Operators limited to chosen apps only see Update on those apps.
  const canUpdate = canDeploy(role) && linked && !!serviceName && canUseApp(session, serviceName ?? "");
  const [failedVersion, setFailedVersion] = useState<string | null>(null);
  const name = repo.full_name.split("/")[1] ?? repo.full_name;
  const languageDot = repo.language ? LANGUAGE_COLORS[repo.language] ?? "bg-slate-400" : null;
  const pushed = timeAgo(repo.pushed_at);
  const shownURL = repairedURL ?? appUrl;
  const repairStorageKey = `devcontrol-app-repair:${repo.full_name.toLowerCase()}`;

  useEffect(() => {
    try {
      const pending = sessionStorage.getItem(repairStorageKey);
      if (pending) {
        setRepairMode(sessionStorage.getItem(`${repairStorageKey}:mode`) === "reimport" ? "reimport" : "repair");
        setRepairTicket(pending);
        setRepairBusy(true);
        setRepairDialog(true);
        setRepairMessage("Melanjutkan pemeriksaan deployment Vercel…");
      }
    } catch { /* storage unavailable */ }
  }, [repairStorageKey]);

  useEffect(() => {
    if (!actionsOpen || !admin || !appUrl || repairedURL) return;
    const controller = new AbortController();
    setRepairBroken(false);
    fetch(`/api/app-repair?repo=${encodeURIComponent(repo.full_name)}`, { cache: "no-store", credentials: "same-origin", signal: controller.signal })
      .then((response) => response.ok ? response.json() : null)
      .then((result) => { if (!controller.signal.aborted && result?.broken && result.app_url === appUrl) setRepairBroken(true); })
      .catch(() => {});
    return () => controller.abort();
  }, [actionsOpen, admin, appUrl, repairedURL, repo.full_name]);

  useEffect(() => {
    if (!repairTicket) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      try {
        const response = await fetch(`/api/app-repair?ticket=${encodeURIComponent(repairTicket)}`, { cache: "no-store", credentials: "same-origin" });
        const result = await response.json();
        if (!active) return;
        if (!response.ok) throw new Error(result.error || "Status pemulihan tidak dapat diperiksa");
        if (result.status === "ready") {
          setRepairMessage(result.message || "Aplikasi sudah online");
          setRepairedURL(result.app_url);
          setRepairBroken(false);
          setRepairTicket(null);
          setRepairBusy(false);
          try { sessionStorage.removeItem(repairStorageKey); sessionStorage.removeItem(`${repairStorageKey}:mode`); } catch { /* storage unavailable */ }
          notifyDataChanged();
        } else if (result.status === "failed") {
          setRepairError(result.message || "Build pemulihan gagal");
          setRepairTicket(null);
          setRepairBusy(false);
          try { sessionStorage.removeItem(repairStorageKey); sessionStorage.removeItem(`${repairStorageKey}:mode`); } catch { /* storage unavailable */ }
        } else {
          setRepairMessage(result.message || "Build Vercel sedang berjalan…");
          timer = setTimeout(poll, 4000);
        }
      } catch (error) {
        if (active) {
          setRepairError(error instanceof Error ? error.message : "Status pemulihan tidak dapat diperiksa");
          setRepairTicket(null);
          setRepairBusy(false);
          try { sessionStorage.removeItem(repairStorageKey); sessionStorage.removeItem(`${repairStorageKey}:mode`); } catch { /* storage unavailable */ }
        }
      }
    };
    timer = setTimeout(poll, 2500);
    return () => { active = false; clearTimeout(timer); };
  }, [repairTicket, repairStorageKey]);

  const startRepair = async (action: "repair" | "reimport") => {
    if (repairBusy) return;
    if (action === "reimport" && reimportConfirm !== repo.full_name) return;
    setRepairBusy(true);
    setRepairError("");
    setRepairMessage(action === "reimport" ? "Memeriksa project web lama dan menyiapkan pengganti…" : "Memeriksa import Git dan project Vercel…");
    try {
      const response = await fetch(`/api/app-repair?repo=${encodeURIComponent(repo.full_name)}`, {
        method: "POST", credentials: "same-origin",
        ...(action === "reimport" ? { headers: { "Content-Type": "application/json" }, body: JSON.stringify({ action: "reimport", confirmation: reimportConfirm, api_url: reimportAPIURL.trim() }) } : {}),
      });
      const result = await response.json();
      if (!response.ok) throw new Error(result.error || "Pemulihan gagal dimulai");
      setRepairMessage(result.message || "Pemulihan sedang berjalan");
      if (result.status === "ready" && result.app_url) {
        setRepairedURL(result.app_url);
        setRepairBroken(false);
        setRepairBusy(false);
        notifyDataChanged();
      } else if (result.status === "pending" && result.ticket) {
        try { sessionStorage.setItem(repairStorageKey, result.ticket); sessionStorage.setItem(`${repairStorageKey}:mode`, action); } catch { /* storage unavailable */ }
        setRepairTicket(result.ticket);
      } else {
        throw new Error("Respons pemulihan Vercel tidak lengkap");
      }
    } catch (error) {
      setRepairError(error instanceof Error ? error.message : "Pemulihan gagal dimulai");
      setRepairBusy(false);
    }
  };

  useEffect(() => {
    if (!failedVersion) return;
    const retryImage = () => setFailedVersion(null);
    window.addEventListener("online", retryImage);
    return () => window.removeEventListener("online", retryImage);
  }, [failedVersion]);

  useEffect(() => {
    if (!actionsOpen) return;
    const closeOutside = (event: PointerEvent) => {
      if (!menuRef.current?.contains(event.target as Node)) setActionsOpen(false);
    };
    const closeEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") { setActionsOpen(false); menuRef.current?.querySelector("button")?.focus(); }
    };
    document.addEventListener("pointerdown", closeOutside);
    document.addEventListener("keydown", closeEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOutside);
      document.removeEventListener("keydown", closeEscape);
    };
  }, [actionsOpen]);

  return (
    <div className={`card relative flex min-w-0 flex-col gap-1.5 p-1.5 ${actionsOpen ? "z-30" : "z-0"}`}>
      <div className="relative aspect-[16/10] rounded-lg border border-base-border bg-base-800">
        <div className="absolute inset-0 overflow-hidden rounded-lg">
          <div className="absolute inset-0 bg-gradient-to-br from-blue-500/25 via-base-800 to-violet-500/20" aria-hidden="true">
            <svg viewBox="0 0 400 300" preserveAspectRatio="xMidYMid slice" className="h-full w-full text-blue-300/20" fill="none">
              <path d="M0 72h400M0 144h400M0 216h400M80 0v300M160 0v300M240 0v300M320 0v300" stroke="currentColor" strokeWidth="1" />
              <rect x="70" y="55" width="260" height="190" rx="14" fill="var(--thumbnail-window)" stroke="#5189da" strokeOpacity=".35" />
              <path d="M70 92h260" stroke="#5189da" strokeOpacity=".35" />
              <circle cx="90" cy="74" r="5" fill="#60a5fa" fillOpacity=".75" />
              <circle cx="108" cy="74" r="5" fill="#a78bfa" fillOpacity=".6" />
              <rect x="92" y="116" width="102" height="106" rx="8" fill="#3b82f6" fillOpacity=".22" />
              <rect x="210" y="116" width="97" height="12" rx="6" fill="#94a3b8" fillOpacity=".45" />
              <rect x="210" y="143" width="73" height="9" rx="4" fill="#94a3b8" fillOpacity=".25" />
              <rect x="210" y="169" width="97" height="53" rx="8" fill="#a78bfa" fillOpacity=".16" />
            </svg>
          </div>
          {thumbnail && failedVersion !== thumbnail.version && (
            // This authenticated same-origin endpoint serves the untouched original from private R2.
            // eslint-disable-next-line @next/next/no-img-element
            <img key={thumbnail.version} src={imageURL(repo.full_name, thumbnail.slot, thumbnail.version)} alt={`Thumbnail aplikasi ${repo.full_name}`} loading="lazy" decoding="async" className="absolute inset-0 h-full w-full object-cover" onLoad={() => setFailedVersion(null)} onError={() => setFailedVersion(thumbnail.version)} />
          )}
        </div>
        <div ref={menuRef} className="absolute right-1.5 top-1.5 z-10" onBlur={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget)) setActionsOpen(false);
        }}>
          <button type="button" aria-label={`Aksi proyek ${repo.full_name}`} aria-expanded={actionsOpen} aria-haspopup="menu" onClick={() => setActionsOpen((open) => !open)} className="flex h-7 w-7 items-center justify-center rounded-lg border border-white/10 bg-base-900/35 text-slate-100 shadow-sm backdrop-blur-sm hover:bg-base-900/60">
            <MoreVertical size={15} />
          </button>
          {actionsOpen && (
            <div role="menu" aria-label={`Aksi proyek ${repo.full_name}`} className="absolute right-0 top-full z-30 mt-2 w-44 rounded-xl border border-base-border bg-base-900 p-1 text-xs shadow-2xl">
              <span aria-hidden="true" className="pointer-events-none absolute -top-[5px] right-2 h-2.5 w-2.5 rotate-45 border-l border-t border-base-border bg-base-900" />
              {/* Open, GitHub and Update live here (moved from the card body) to keep the card compact. */}
              {shownURL && <a href={shownURL} target="_blank" rel="noopener noreferrer" role="menuitem" onClick={() => setActionsOpen(false)} className={`${MENU_ITEM} font-medium text-accent-blue hover:bg-accent-blue/10`}><ExternalLink size={13} /> Buka aplikasi</a>}
              {repo.html_url && <a href={repo.html_url} target="_blank" rel="noopener noreferrer" role="menuitem" onClick={() => setActionsOpen(false)} className={`${MENU_ITEM} text-slate-200 hover:bg-base-800`}><Github size={13} /> GitHub</a>}
              {canUpdate && (
                // The app to change is the card being looked at, so the form opens
                // with this app fixed. One run per app at a time.
                <button type="button" role="menuitem" disabled={updating}
                  onClick={() => { setActionsOpen(false); openDeploy("update_app", { app: serviceName!, label: displayName || repo.name }); }}
                  aria-label={updating ? `${displayName || repo.name} sedang di-update` : `Update ${displayName || repo.name}`}
                  className={`${MENU_ITEM} text-slate-200 hover:bg-base-800 disabled:cursor-not-allowed disabled:text-slate-400 disabled:hover:bg-transparent`}>
                  {updating ? <><Loader2 size={13} className="animate-spin" /> Sedang di-update…</> : <><RefreshCw size={13} /> Update</>}
                </button>
              )}
              {(shownURL || repo.html_url || canUpdate) && <div className="my-1 border-t border-base-border" />}
              <button type="button" role="menuitem" onClick={() => { setActionsOpen(false); setDialog("images"); }} className={`${MENU_ITEM} text-slate-200 hover:bg-base-800`}>
                <ImageIcon size={13} /> Gambar aplikasi
              </button>
              <div className="my-1 border-t border-base-border" />
              {admin && <button type="button" role="menuitem" onClick={() => { setActionsOpen(false); setDialog("zip"); }} className={`${MENU_ITEM} text-slate-200 hover:bg-base-800`}><FileArchive size={13} /> Arsip ZIP</button>}
              <button type="button" role="menuitem" onClick={() => { setActionsOpen(false); setDialog("history"); }} className={`${MENU_ITEM} text-slate-200 hover:bg-base-800`}><History size={13} /> Riwayat update</button>
              {/* Audit uses the deployment record name, the key /api/app-audit knows. */}
              {admin && serviceName && shownURL && <Link href={`/audit?app=${encodeURIComponent(serviceName)}`} role="menuitem" onClick={() => setActionsOpen(false)} className={`${MENU_ITEM} text-slate-200 hover:bg-base-800`}><ShieldCheck size={13} /> Audit aplikasi</Link>}
              {admin && repairBroken && <>
                <button type="button" role="menuitem" onClick={() => { setActionsOpen(false); setRepairMode("repair"); setRepairDialog(true); }} className={`${MENU_ITEM} text-amber-300 hover:bg-amber-500/10`}><RotateCcw size={13} /> Perbaiki 404 Vercel</button>
                <button type="button" role="menuitem" onClick={() => { setActionsOpen(false); setRepairMode("reimport"); setRepairDialog(true); }} className={`${MENU_ITEM} text-amber-300 hover:bg-amber-500/10`}><RotateCcw size={13} /> Impor ulang web Vercel</button>
              </>}
              {admin && <>
              <div className="my-1 border-t border-base-border" />
              <button type="button" role="menuitem" disabled={uploading} onClick={() => { setActionsOpen(false); onDelete(); }} className={`${MENU_ITEM} text-red-400 hover:bg-red-500/10 disabled:opacity-50`}><Trash2 size={13} /> Hapus aplikasi</button>
              </>}
            </div>
          )}
        </div>
      </div>
      {dialog === "zip" && <ZipArchiveDialog repo={repo.full_name} images={images} onClose={() => setDialog(null)} />}
      {dialog === "history" && <UpdateHistoryDialog repo={repo.full_name} onClose={() => setDialog(null)} />}
      {dialog === "images" && <ProjectImagesDialog repo={repo.full_name} images={images} admin={admin}
        uploadingSlot={uploading ? uploadingSlot : null} uploadProgress={uploadProgress} error={imageError}
        onUpload={onUploadImage} onRemove={onRemoveImage} onClose={() => setDialog(null)} />}
      {repairDialog && createPortal(
        <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/70 p-4" onMouseDown={(event) => { if (event.target === event.currentTarget) setRepairDialog(false); }}>
          <section role="dialog" aria-modal="true" aria-label={`Pemulihan 404 ${repo.full_name}`} className="w-full max-w-md rounded-2xl border border-base-border bg-base-900 p-5 shadow-2xl">
            <div className="flex items-start justify-between gap-3"><h2 className="text-lg font-bold text-white">{repairMode === "reimport" ? "Impor ulang web Vercel" : "Perbaiki 404 Vercel"}</h2><button type="button" aria-label="Tutup" onClick={() => setRepairDialog(false)} className="rounded-lg p-1 text-slate-300 hover:bg-base-800"><X size={18} /></button></div>
            {repairMode === "reimport" ? <>
              <p className="mt-3 text-sm text-slate-300">Tautan {repo.full_name} terdeteksi sebagai 404 Vercel. DevControl akan membuat project web baru dari repo dan commit Git yang sudah ada, menyalin variabel Vercel, lalu memeriksa halaman penggantinya.</p>
              <p className="mt-2 text-xs text-amber-200">Setelah tautan baru sehat, project web lama dihapus. Project API (misalnya server/) dan repo GitHub tetap ada. Jika identitas project, variabel, atau domain khusus tidak aman untuk dipindah, penghapusan dibatalkan.</p>
              {!repairedURL && <label className="mt-4 block text-xs text-slate-300">Ketik {repo.full_name} untuk menyetujui penggantian project web
                <input value={reimportConfirm} onChange={(event) => setReimportConfirm(event.target.value)} autoComplete="off" spellCheck={false} disabled={repairBusy} className="mt-2 w-full rounded-lg border border-base-border bg-base-800 px-3 py-2 text-sm text-white outline-none focus:border-accent-blue disabled:opacity-50" />
              </label>}
              {!repairedURL && <label className="mt-3 block text-xs text-slate-300">URL project API jika perlu diganti (opsional)
                <input type="url" placeholder="https://alamat-api.vercel.app" value={reimportAPIURL} onChange={(event) => setReimportAPIURL(event.target.value)} autoComplete="url" disabled={repairBusy} className="mt-2 w-full rounded-lg border border-base-border bg-base-800 px-3 py-2 text-sm text-white outline-none focus:border-accent-blue disabled:opacity-50" />
                <span className="mt-1 block text-slate-400">Contoh repo web/ dan server/: isi URL API server/ jika API_URL pada project lama masih salah. Kosongkan untuk menyalin nilai lama.</span>
              </label>}
            </> : <>
              <p className="mt-3 text-sm text-slate-300">DevControl akan mencari import Git yang sudah sehat. Jika belum ada, pengaturan build dibetulkan dan commit Git yang sama dibangun ulang pada project terkait.</p>
              <p className="mt-2 text-xs text-slate-400">Project yang ada tidak dihapus. Tautan baru disimpan setelah halaman dan deployment terverifikasi.</p>
            </>}
            {repairMessage && <p role="status" className="mt-4 rounded-lg bg-base-800 p-3 text-sm text-slate-200">{repairMessage}</p>}
            {repairError && <p role="alert" className="mt-4 rounded-lg bg-red-500/10 p-3 text-sm text-red-300">{repairError}</p>}
            {repairedURL ? <a href={repairedURL} target="_blank" rel="noopener noreferrer" className="mt-4 inline-flex items-center gap-1 rounded-lg bg-emerald-600 px-4 py-2 text-sm text-white">Buka aplikasi <ExternalLink size={14} /></a> :
              <button type="button" disabled={repairBusy || (repairMode === "reimport" && reimportConfirm !== repo.full_name)} onClick={() => startRepair(repairMode)} className="mt-4 rounded-lg bg-accent-blue px-4 py-2 text-sm font-medium text-white disabled:opacity-50">{repairBusy ? "Memulihkan…" : repairMode === "reimport" ? "Buat pengganti dan impor ulang" : "Mulai pemulihan"}</button>}
            {!repairBusy && !repairedURL && <button type="button" onClick={() => { setRepairMode(repairMode === "reimport" ? "repair" : "reimport"); setRepairError(""); setRepairMessage(""); }} className="ml-3 mt-4 text-xs text-slate-300 underline">{repairMode === "reimport" ? "Coba perbaikan biasa" : "Impor ulang jika perbaikan belum berhasil"}</button>}
          </section>
        </div>, document.body,
      )}
      {uploading && <p role="status" className="text-[11px] text-slate-400">{uploadProgress === null ? "Menyiapkan gambar…" : uploadProgress < 100 ? `Mengunggah ${uploadProgress}%` : "Memverifikasi gambar…"}</p>}
      {imageError && <p role="alert" className="text-[11px] text-red-300">{imageError}</p>}
      <div className="flex flex-wrap items-start justify-between gap-1">
        <div className="min-w-0 max-w-full">
          <h3 className="truncate text-xs font-bold text-white sm:text-sm" title={displayName || name}>{displayName || name}</h3>
        </div>
        <div className="flex shrink-0 flex-wrap items-center gap-1">
          {source === "stored" && (
            <span className="rounded-full bg-base-800 px-1.5 py-0.5 text-[10px] text-slate-400">Tersimpan di aplikasi</span>
          )}
          {repo.private && (
            <span className="flex items-center gap-1 rounded-full bg-base-800 px-1.5 py-0.5 text-[10px] font-medium text-slate-400">
              <Lock size={10} /> Private
            </span>
          )}
          {repo.archived && (
            <span className="flex items-center gap-1 rounded-full bg-amber-400/15 px-1.5 py-0.5 text-[10px] font-medium text-amber-400">
              <Archive size={10} /> Archived
            </span>
          )}
        </div>
      </div>

      <p className="line-clamp-2 min-h-[2rem] text-[11px] leading-4 text-slate-400 sm:text-xs">
        {repo.description || "Tidak ada deskripsi."}
      </p>

      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-slate-500">
        {repo.language && (
          <span className="flex items-center gap-1.5">
            <span className={`h-2 w-2 rounded-full ${languageDot}`} />
            {repo.language}
          </span>
        )}
        <span className="flex items-center gap-1">
          <GitBranch size={12} />
          {repo.default_branch}
        </span>
        {!!repo.stargazers_count && (
          <span className="flex items-center gap-1">
            <Star size={12} />
            {repo.stargazers_count}
          </span>
        )}
        {!!repo.open_issues_count && (
          <span className="flex items-center gap-1">
            <AlertCircle size={12} />
            {repo.open_issues_count} issue{repo.open_issues_count === 1 ? "" : "s"}
          </span>
        )}
      </div>

      <div className="flex flex-wrap items-center justify-between gap-x-2 gap-y-1 border-t border-base-border/70 pt-1.5">
        <span className="text-[10px] text-slate-500">{pushed ? `Diperbarui ${pushed}` : linked ? "Tercatat di deployment" : "Repo GitHub"}</span>
        {updating
          ? <span role="status" className="flex items-center gap-1 text-[10px] font-medium text-purple-400"><Loader2 size={10} className="animate-spin" /> Sedang di-update…</span>
          : !shownURL && <span className="text-[10px] text-slate-500">{linked ? "URL aplikasi belum tersimpan" : "Belum terhubung ke deployment"}</span>}
      </div>
    </div>
  );
}
