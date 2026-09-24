"use client";

import { AlertCircle, Archive, ExternalLink, GitBranch, Lock, Star } from "lucide-react";
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

export default function ProjectCard({ repo, appUrl, linked = false, source = "github" }: {
  repo: GithubRepo;
  appUrl?: string | null;
  linked?: boolean;
  source?: "github" | "stored";
}) {
  const [owner, name] = repo.full_name.split("/");
  const languageDot = repo.language ? LANGUAGE_COLORS[repo.language] ?? "bg-slate-400" : null;
  const pushed = timeAgo(repo.pushed_at);

  return (
    <div className="card flex flex-col gap-3 p-4 sm:p-5">
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
