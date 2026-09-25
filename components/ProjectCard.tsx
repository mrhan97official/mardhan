"use client";

import { useEffect, useRef, useState } from "react";
import { AlertCircle, Archive, ExternalLink, GitBranch, ImagePlus, Lock, MoreVertical, Star, Trash2, X } from "lucide-react";
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

export default function ProjectCard({ repo, appUrl, linked = false, source = "github", thumbnailVersion, uploading = false, uploadProgress = null, imageError, onUpload, onRemoveThumbnail, onDelete }: {
  repo: GithubRepo;
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
  const [failedVersion, setFailedVersion] = useState<string | null>(null);
  const [owner, name] = repo.full_name.split("/");
  const languageDot = repo.language ? LANGUAGE_COLORS[repo.language] ?? "bg-slate-400" : null;
  const pushed = timeAgo(repo.pushed_at);

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
    <div className={`card relative flex min-w-0 flex-col gap-3 p-4 sm:p-5 ${actionsOpen ? "z-30" : "z-0"}`}>
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
          <span className="project-thumb-caption absolute bottom-3 left-3 right-3 truncate rounded-lg bg-black/70 px-2.5 py-1.5 text-xs font-semibold text-white backdrop-blur-sm">{name}</span>
        </div>
        <div ref={menuRef} className="absolute right-2 top-2 z-10" onBlur={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget)) setActionsOpen(false);
        }}>
          <button type="button" aria-label={`Aksi proyek ${repo.full_name}`} aria-expanded={actionsOpen} aria-haspopup="menu" onClick={() => setActionsOpen((open) => !open)} className="flex h-8 w-8 items-center justify-center rounded-lg border border-base-border bg-base-900/90 text-slate-200 shadow-lg backdrop-blur hover:bg-base-800">
            <MoreVertical size={17} />
          </button>
          {actionsOpen && (
            <div role="menu" aria-label={`Aksi proyek ${repo.full_name}`} className="absolute right-0 top-full z-30 mt-2 w-44 rounded-xl border border-base-border bg-base-900 p-1.5 text-sm shadow-2xl">
              <span aria-hidden="true" className="pointer-events-none absolute -top-[5px] right-2.5 h-2.5 w-2.5 rotate-45 border-l border-t border-base-border bg-base-900" />
              <button type="button" role="menuitem" disabled={uploading} onClick={() => { setActionsOpen(false); inputRef.current?.click(); }} className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-xs text-slate-200 hover:bg-base-800 disabled:opacity-50">
                <ImagePlus size={14} /> {thumbnailVersion ? "Ganti thumbnail" : "Tambah thumbnail"}
              </button>
              {thumbnailVersion && <button type="button" role="menuitem" disabled={uploading} onClick={() => { setActionsOpen(false); onRemoveThumbnail(); }} className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-xs text-slate-300 hover:bg-base-800 disabled:opacity-50"><X size={14} /> Hapus gambar</button>}
              <div className="my-1 border-t border-base-border" />
              <button type="button" role="menuitem" disabled={uploading} onClick={() => { setActionsOpen(false); onDelete(); }} className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-xs text-red-400 hover:bg-red-500/10 disabled:opacity-50"><Trash2 size={14} /> Hapus aplikasi</button>
            </div>
          )}
        </div>
      </div>
      <input ref={inputRef} type="file" accept="image/jpeg,image/png" className="sr-only" disabled={uploading} aria-label={`Unggah thumbnail ${repo.full_name}`} onChange={(event) => {
          const file = event.currentTarget.files?.[0];
          if (file) onUpload(file);
          event.currentTarget.value = "";
        }} />
      {uploading && <p role="status" className="text-xs text-slate-400">{uploadProgress === null ? "Menyiapkan gambar…" : uploadProgress < 100 ? `Mengunggah ${uploadProgress}%` : "Memverifikasi gambar…"}</p>}
      {imageError && <p role="alert" className="text-xs text-red-300">{imageError}</p>}
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="truncate text-xs text-slate-500">{owner}</p>
          <h3 className="truncate text-sm font-bold text-white sm:text-base">{name}</h3>
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

      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-xs text-slate-500">
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

      <div className="mt-1 flex flex-wrap items-center justify-between gap-2 border-t border-base-border/70 pt-3">
        <span className="text-[11px] text-slate-500">{pushed ? `Diperbarui ${pushed}` : linked ? "Tercatat di deployment" : "Repo GitHub"}</span>
        {repo.html_url && (
          <a href={repo.html_url} target="_blank" rel="noopener noreferrer" className="flex items-center gap-1 text-xs text-slate-300 hover:underline">
            GitHub <ExternalLink size={12} />
          </a>
        )}
        {appUrl ? (
          <a
            href={appUrl}
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
