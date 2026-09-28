"use client";

import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { AlertCircle, Archive, ExternalLink, FileArchive, GitBranch, History, ImagePlus, Lock, MoreVertical, RotateCcw, Star, Trash2, X } from "lucide-react";
import { UpdateHistoryDialog, ZipArchiveDialog } from "@/components/ProjectDialogs";
import { notifyDataChanged } from "@/lib/liveUpdates";
import { isAdminRole, useSession } from "@/lib/session";
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

export default function ProjectCard({ repo, displayName, appUrl, linked = false, source = "github", thumbnailVersion, uploading = false, uploadProgress = null, imageError, onUpload, onRemoveThumbnail, onDelete }: {
  repo: GithubRepo;
  displayName?: string;
  appUrl?: string | null;
  linked?: boolean;
  source?: "github" | "stored";
  thumbnailVersion?: string;
  uploading?: boolean;
  uploadProgress?: number | null;
  imageError?: string;
  onUpload: (file: File) => void;
  onRemoveThumbnail: () => void;
  onDelete: () => void;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const [actionsOpen, setActionsOpen] = useState(false);
  const [dialog, setDialog] = useState<"zip" | "history" | null>(null);
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
  const { role } = useSession();
  const admin = isAdminRole(role);
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
    <div className={`card relative flex min-w-0 flex-col gap-2 p-2 ${actionsOpen ? "z-30" : "z-0"}`}>
      <div className="relative aspect-[4/3] rounded-xl border border-base-border bg-base-800">
        <div className="absolute inset-0 overflow-hidden rounded-xl">
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
          {thumbnailVersion && failedVersion !== thumbnailVersion && (
            // This authenticated same-origin image endpoint serves JPG/PNG from private R2.
            // eslint-disable-next-line @next/next/no-img-element
            <img key={thumbnailVersion} src={`/api/project-thumbnails?repo=${encodeURIComponent(repo.full_name)}&v=${encodeURIComponent(thumbnailVersion)}`} alt={`Thumbnail aplikasi ${repo.full_name}`} loading="lazy" className="absolute inset-0 h-full w-full object-cover" onLoad={() => setFailedVersion(null)} onError={() => setFailedVersion(thumbnailVersion)} />
          )}
        </div>
        <div ref={menuRef} className="absolute right-2 top-2 z-10" onBlur={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget)) setActionsOpen(false);
        }}>
          <button type="button" aria-label={`Aksi proyek ${repo.full_name}`} aria-expanded={actionsOpen} aria-haspopup="menu" onClick={() => setActionsOpen((open) => !open)} className="flex h-8 w-8 items-center justify-center rounded-lg border border-white/10 bg-base-900/35 text-slate-100 shadow-sm backdrop-blur-sm hover:bg-base-900/60">
            <MoreVertical size={17} />
          </button>
          {actionsOpen && (
            <div role="menu" aria-label={`Aksi proyek ${repo.full_name}`} className="absolute right-0 top-full z-30 mt-2 w-52 rounded-xl border border-base-border bg-base-900 p-1.5 text-sm shadow-2xl">
              <span aria-hidden="true" className="pointer-events-none absolute -top-[5px] right-2.5 h-2.5 w-2.5 rotate-45 border-l border-t border-base-border bg-base-900" />
              {admin && <>
              <button type="button" role="menuitem" disabled={uploading} onClick={() => { setActionsOpen(false); inputRef.current?.click(); }} className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-xs text-slate-200 hover:bg-base-800 disabled:opacity-50">
                <ImagePlus size={14} /> {thumbnailVersion ? "Ganti thumbnail" : "Tambah thumbnail"}
              </button>
              {thumbnailVersion && <button type="button" role="menuitem" disabled={uploading} onClick={() => { setActionsOpen(false); onRemoveThumbnail(); }} className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-xs text-slate-300 hover:bg-base-800 disabled:opacity-50"><X size={14} /> Hapus gambar</button>}
              <div className="my-1 border-t border-base-border" />
              </>}
              {admin && <button type="button" role="menuitem" onClick={() => { setActionsOpen(false); setDialog("zip"); }} className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-xs text-slate-200 hover:bg-base-800"><FileArchive size={14} /> Arsip ZIP</button>}
              <button type="button" role="menuitem" onClick={() => { setActionsOpen(false); setDialog("history"); }} className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-xs text-slate-200 hover:bg-base-800"><History size={14} /> Riwayat update</button>
              {admin && repairBroken && <>
                <button type="button" role="menuitem" onClick={() => { setActionsOpen(false); setRepairMode("repair"); setRepairDialog(true); }} className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-xs text-amber-300 hover:bg-amber-500/10"><RotateCcw size={14} /> Perbaiki 404 Vercel</button>
                <button type="button" role="menuitem" onClick={() => { setActionsOpen(false); setRepairMode("reimport"); setRepairDialog(true); }} className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-xs text-amber-300 hover:bg-amber-500/10"><RotateCcw size={14} /> Impor ulang web Vercel</button>
              </>}
              {admin && <>
              <div className="my-1 border-t border-base-border" />
              <button type="button" role="menuitem" disabled={uploading} onClick={() => { setActionsOpen(false); onDelete(); }} className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-xs text-red-400 hover:bg-red-500/10 disabled:opacity-50"><Trash2 size={14} /> Hapus aplikasi</button>
              </>}
            </div>
          )}
        </div>
      </div>
      {dialog === "zip" && <ZipArchiveDialog repo={repo.full_name} onClose={() => setDialog(null)} />}
      {dialog === "history" && <UpdateHistoryDialog repo={repo.full_name} onClose={() => setDialog(null)} />}
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
      <input ref={inputRef} type="file" accept="image/jpeg,image/png,image/webp" className="sr-only" disabled={uploading} aria-label={`Unggah thumbnail ${repo.full_name}`} onChange={(event) => {
          const file = event.currentTarget.files?.[0];
          if (file) onUpload(file);
          event.currentTarget.value = "";
        }} />
      {uploading && <p role="status" className="text-xs text-slate-400">{uploadProgress === null ? "Menyiapkan gambar…" : uploadProgress < 100 ? `Mengunggah ${uploadProgress}%` : "Memverifikasi gambar…"}</p>}
      {imageError && <p role="alert" className="text-xs text-red-300">{imageError}</p>}
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h3 className="truncate text-sm font-bold text-white sm:text-base" title={displayName || name}>{displayName || name}</h3>
          {displayName && displayName !== name && <p className="truncate text-xs text-slate-500" title={repo.full_name}>Repo: {repo.full_name}</p>}
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {source === "stored" && (
            <span className="rounded-full bg-base-800 px-2 py-1 text-[11px] text-slate-400">Tersimpan di aplikasi</span>
          )}
          {repo.private && (
            <span className="flex items-center gap-1 rounded-full bg-base-800 px-2 py-1 text-[11px] font-medium text-slate-400">
              <Lock size={11} /> Private
            </span>
          )}
          {repo.archived && (
            <span className="flex items-center gap-1 rounded-full bg-amber-400/15 px-2 py-1 text-[11px] font-medium text-amber-400">
              <Archive size={11} /> Archived
            </span>
          )}
        </div>
      </div>

      <p className="line-clamp-2 min-h-[2.5rem] text-xs text-slate-400 sm:text-sm">
        {repo.description || "Tidak ada deskripsi."}
      </p>

      <div className="flex flex-wrap items-center gap-x-2 gap-y-1.5 text-xs text-slate-500">
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

      <div className="mt-1 flex flex-wrap items-center justify-between gap-2 border-t border-base-border/70 pt-2">
        <span className="text-[11px] text-slate-500">{pushed ? `Diperbarui ${pushed}` : linked ? "Tercatat di deployment" : "Repo GitHub"}</span>
        {repo.html_url && (
          <a href={repo.html_url} target="_blank" rel="noopener noreferrer" className="flex items-center gap-1 text-xs text-slate-300 hover:underline">
            GitHub <ExternalLink size={12} />
          </a>
        )}
        {shownURL ? (
          <a
            href={shownURL}
            target="_blank"
            rel="noopener noreferrer"
            className="flex items-center gap-1 text-xs font-medium text-accent-blue hover:underline"
          >
            Buka aplikasi <ExternalLink size={12} />
          </a>
        ) : <span className="text-xs text-slate-500">{linked ? "URL aplikasi belum tersimpan" : "Belum terhubung ke deployment"}</span>}
      </div>
    </div>
  );
}
