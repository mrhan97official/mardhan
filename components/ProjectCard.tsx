"use client";

import { useEffect, useId, useState } from "react";
import { AlertCircle, Archive, ExternalLink, GitBranch, ImagePlus, Lock, Star, Trash2, X } from "lucide-react";
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
  const inputId = useId();
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

  return (
    <div className="card flex min-w-0 flex-col gap-3 p-4 sm:p-5">
      <div className="relative aspect-[4/3] overflow-hidden rounded-xl border border-base-border bg-base-800">
        <div className="absolute inset-0 bg-gradient-to-br from-blue-500/25 via-base-800 to-violet-500/20" aria-hidden="true">
          <svg viewBox="0 0 400 300" preserveAspectRatio="xMidYMid slice" className="h-full w-full text-blue-300/20" fill="none">
            <path d="M0 72h400M0 144h400M0 216h400M80 0v300M160 0v300M240 0v300M320 0v300" stroke="currentColor" strokeWidth="1" />
            <rect x="70" y="55" width="260" height="190" rx="14" fill="#17243c" stroke="#5189da" strokeOpacity=".35" />
            <path d="M70 92h260" stroke="#5189da" strokeOpacity=".35" />
            <circle cx="90" cy="74" r="5" fill="#60a5fa" fillOpacity=".75" />
            <circle cx="108" cy="74" r="5" fill="#a78bfa" fillOpacity=".6" />
            <rect x="92" y="116" width="102" height="106" rx="8" fill="#3b82f6" fillOpacity=".22" />
            <rect x="210" y="116" width="97" height="12" rx="6" fill="#94a3b8" fillOpacity=".45" />
            <rect x="210" y="143" width="73" height="9" rx="4" fill="#94a3b8" fillOpacity=".25" />
            <rect x="210" y="169" width="97" height="53" rx="8" fill="#a78bfa" fillOpacity=".16" />
          </svg>
        </div>
        <span className="absolute bottom-3 left-3 right-3 truncate rounded-lg bg-base-950/80 px-2.5 py-1.5 text-xs font-semibold text-white backdrop-blur-sm">{name}</span>
        {thumbnailVersion && failedVersion !== thumbnailVersion && (
          // This authenticated same-origin image endpoint serves JPG/PNG from private R2.
          // eslint-disable-next-line @next/next/no-img-element
          <img key={thumbnailVersion} src={`/api/project-thumbnails?repo=${encodeURIComponent(repo.full_name)}&v=${encodeURIComponent(thumbnailVersion)}`} alt={`Thumbnail aplikasi ${repo.full_name}`} loading="lazy" className="absolute inset-0 h-full w-full object-cover" onLoad={() => setFailedVersion(null)} onError={() => setFailedVersion(thumbnailVersion)} />
        )}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <label htmlFor={inputId} aria-disabled={uploading} className={`inline-flex items-center gap-1.5 rounded-lg border border-base-border px-2.5 py-1.5 text-xs font-medium text-slate-300 hover:bg-base-800 ${uploading ? "pointer-events-none opacity-50" : "cursor-pointer"}`}>
          <ImagePlus size={13} /> {uploading ? uploadProgress === null ? "Menyiapkan gambar..." : uploadProgress < 100 ? `Mengunggah ${uploadProgress}%` : "Memverifikasi gambar..." : thumbnailVersion ? "Ganti thumbnail" : "Tambah thumbnail"}
        </label>
        <input id={inputId} type="file" accept="image/jpeg,image/png" className="sr-only" disabled={uploading} onChange={(event) => {
          const file = event.currentTarget.files?.[0];
          if (file) onUpload(file);
          event.currentTarget.value = "";
        }} />
        {thumbnailVersion && <button type="button" disabled={uploading} onClick={onRemoveThumbnail} className="inline-flex items-center gap-1 text-xs text-slate-400 hover:text-white disabled:opacity-50"><X size={12} /> Hapus gambar</button>}
        <span className="text-[11px] text-slate-500">JPG/PNG asli · tanpa kompresi · tampil 4:3</span>
      </div>
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
      <button type="button" disabled={uploading} onClick={onDelete} className="flex w-fit items-center gap-1.5 rounded-lg border border-red-500/30 px-2.5 py-1.5 text-xs font-medium text-red-300 hover:bg-red-500/10 disabled:opacity-50" aria-label={`Hapus ${repo.full_name} secara permanen`}>
        <Trash2 size={13} /> Hapus aplikasi
      </button>
    </div>
  );
}
