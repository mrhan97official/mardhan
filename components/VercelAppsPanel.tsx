"use client";

import { useMemo, useState, type ReactNode } from "react";
import { ExternalLink, GitBranch, Globe, Link2, Loader2, RefreshCw, Trash2, Unlink } from "lucide-react";
import { ConnectGitDialog, DeleteProjectDialog, type InventoryProject, type InventoryRepo } from "@/components/InventoryProjectActions";
import { useOfflineData } from "@/lib/useOfflineData";
import { notifyDataChanged } from "@/lib/liveUpdates";
import { isAdminRole, useSession } from "@/lib/session";

interface Source { configured: boolean; ok: boolean; error?: string; truncated?: boolean }
interface Inventory { github: Source; vercel: Source; repos: InventoryRepo[]; projects: InventoryProject[]; checked_at: string }

const EMPTY: Inventory = {
  github: { configured: false, ok: false }, vercel: { configured: false, ok: false },
  repos: [], projects: [], checked_at: "",
};

type Filter = "all" | "online" | "unconnected" | "problem" | "temporary";

function stateOf(project: InventoryProject): { label: string; dot: string; text: string } {
  switch (project.production_state) {
    case "READY": return { label: "Online", dot: "bg-emerald-400", text: "text-emerald-300" };
    case "ERROR": return { label: "Build gagal", dot: "bg-red-400", text: "text-red-300" };
    case "BUILDING": case "QUEUED": case "INITIALIZING": return { label: "Sedang build", dot: "bg-purple-400 animate-pulse", text: "text-purple-400" };
    case "CANCELED": return { label: "Dibatalkan", dot: "bg-slate-500", text: "text-slate-400" };
    case "": case undefined: return { label: "Belum ada deployment production", dot: "bg-slate-500", text: "text-slate-400" };
    default: return { label: project.production_state || "Tidak diketahui", dot: "bg-amber-400", text: "text-amber-300" };
  }
}

function hostOf(url?: string): string {
  if (!url) return "";
  try { return new URL(url).host; } catch { return url; }
}

function Chip({ active, onClick, children }: { active: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button type="button" onClick={onClick} aria-pressed={active}
      className={`rounded-full border px-2.5 py-1 text-xs transition-colors ${active ? "border-accent-blue bg-accent-blue/15 text-slate-100" : "border-base-border text-slate-400 hover:bg-base-800"}`}>
      {children}
    </button>
  );
}

// Vercel is what is actually online, so it is the count of record here.
// GitHub repositories are shown per project as its source (or its absence).
export default function VercelAppsPanel() {
  const inventory = useOfflineData<Inventory>("vercel-projects-v1", "/api/vercel-projects", EMPTY, 60000);
  const { role } = useSession();
  const admin = isAdminRole(role);
  const [filter, setFilter] = useState<Filter>("all");
  const [connecting, setConnecting] = useState<InventoryProject | null>(null);
  const [deleting, setDeleting] = useState<InventoryProject | null>(null);
  const [notice, setNotice] = useState("");
  const data = inventory.data;

  const counts = useMemo(() => {
    const projects = data.projects;
    return {
      total: projects.length,
      online: projects.filter((project) => project.production_state === "READY").length,
      unconnected: projects.filter((project) => project.status === "unconnected").length,
      problem: projects.filter((project) => project.status === "outside" || project.status === "other-git").length,
      temporary: projects.filter((project) => project.temporary).length,
      repos: data.repos.length,
    };
  }, [data]);

  const visible = useMemo(() => data.projects.filter((project) => {
    switch (filter) {
      case "online": return project.production_state === "READY";
      case "unconnected": return project.status === "unconnected";
      case "problem": return project.status === "outside" || project.status === "other-git";
      case "temporary": return Boolean(project.temporary);
      default: return true;
    }
  }), [data.projects, filter]);

  function changed() { inventory.reload(); notifyDataChanged(); }

  const firstLoad = inventory.loading && data.projects.length === 0;
  return (
    <section className="card space-y-2 p-2" aria-labelledby="vercel-apps-title">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 id="vercel-apps-title" className="flex items-center gap-2 text-base font-bold text-white sm:text-lg"><Globe size={18} /> Aplikasi di Vercel</h2>
          <p className="mt-0.5 text-sm text-slate-400">
            {firstLoad ? "Membaca project Vercel…" : data.vercel.ok
              ? <><b className="text-slate-100">{counts.total} aplikasi</b> di Vercel · {counts.online} online · {counts.unconnected} belum connect Git · {counts.repos} repo GitHub</>
              : "Project Vercel belum dapat dibaca."}
          </p>
        </div>
        <button type="button" onClick={() => inventory.reload()} disabled={inventory.loading}
          className="flex items-center gap-1.5 rounded-xl border border-base-border px-3 py-1.5 text-xs font-medium text-slate-300 hover:bg-base-800 disabled:opacity-60">
          <RefreshCw size={13} className={inventory.loading ? "animate-spin" : ""} /> Segarkan
        </button>
      </div>

      {(inventory.error || (!firstLoad && data.vercel.error)) && <p role="alert" className="rounded-lg bg-accent-amber/10 p-2 text-xs text-amber-300">
        {inventory.error || data.vercel.error}{data.projects.length > 0 ? " Menampilkan data terakhir yang tersimpan." : ""}</p>}
      {!firstLoad && data.vercel.ok && !data.github.ok && data.github.error && <p className="rounded-lg bg-accent-amber/10 p-2 text-xs text-amber-300">
        Repo GitHub tidak terbaca ({data.github.error}); status repo per project mungkin tidak lengkap.</p>}
      {notice && <p role="status" className="flex items-start justify-between gap-2 rounded-lg bg-accent-green/10 p-2 text-sm text-emerald-300">
        <span>{notice}</span><button type="button" onClick={() => setNotice("")} className="text-xs text-slate-400 hover:text-slate-200">Tutup</button></p>}

      {firstLoad ? (
        <p className="flex items-center gap-2 py-2 text-sm text-slate-400"><Loader2 size={14} className="animate-spin" /> Memuat…</p>
      ) : data.projects.length > 0 && <>
        <div className="flex flex-wrap gap-1.5">
          <Chip active={filter === "all"} onClick={() => setFilter("all")}>Semua {counts.total}</Chip>
          <Chip active={filter === "online"} onClick={() => setFilter("online")}>Online {counts.online}</Chip>
          <Chip active={filter === "unconnected"} onClick={() => setFilter("unconnected")}>Belum connect Git {counts.unconnected}</Chip>
          {counts.problem > 0 && <Chip active={filter === "problem"} onClick={() => setFilter("problem")}>Repo di luar token / Git lain {counts.problem}</Chip>}
          {counts.temporary > 0 && <Chip active={filter === "temporary"} onClick={() => setFilter("temporary")}>Sisa uji update diri {counts.temporary}</Chip>}
        </div>
        {visible.length === 0 ? <p className="py-3 text-center text-sm text-slate-400">Tidak ada aplikasi pada filter ini.</p> : (
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {visible.map((project) => {
              const state = stateOf(project);
              const unconnected = project.status === "unconnected";
              return (
                <article key={project.id} className="flex min-w-0 flex-col gap-2 rounded-xl border border-base-border bg-base-850 p-2">
                  <div className="flex min-w-0 items-start justify-between gap-2">
                    <div className="min-w-0">
                      <h3 className="truncate text-sm font-semibold text-slate-100" title={project.name}>{project.name}</h3>
                      <p className={`mt-0.5 flex items-center gap-1.5 text-xs ${state.text}`}><span className={`h-1.5 w-1.5 shrink-0 rounded-full ${state.dot}`} />{state.label}</p>
                    </div>
                    {project.url && <a href={project.url} target="_blank" rel="noopener noreferrer" title="Buka di Vercel" aria-label={`Buka ${project.name} di Vercel`}
                      className="shrink-0 rounded-md border border-base-border p-1.5 text-slate-300 hover:bg-base-800"><ExternalLink size={12} /></a>}
                  </div>
                  {project.production_url && <a href={project.production_url} target="_blank" rel="noopener noreferrer"
                    className="truncate text-xs text-accent-blue hover:underline">{hostOf(project.production_url)}</a>}
                  <p className="flex min-w-0 items-center gap-1 text-xs text-slate-400">
                    {unconnected ? <><Unlink size={12} className="shrink-0 text-amber-300" /><span className="text-amber-300">Belum connect Git</span>
                      {project.suggested_repo && <span className="truncate"> · mungkin {project.suggested_repo}</span>}</>
                      : <><GitBranch size={12} className="shrink-0" />
                        {project.git_provider === "github" && project.repo
                          ? <a href={`https://github.com/${project.repo}`} target="_blank" rel="noopener noreferrer" className="truncate hover:text-slate-200 hover:underline">{project.repo}</a>
                          : <span className="truncate">{project.repo || project.git_provider}</span>}
                        {project.status === "outside" && <span className="shrink-0 text-accent-purple"> · di luar token</span>}</>}
                  </p>
                  {(project.temporary || project.managed_repo || project.self) && <div className="flex flex-wrap gap-1">
                    {project.self && <span className="rounded-full bg-accent-cyan/15 px-2 py-0.5 text-[11px] text-accent-cyan">Aplikasi ini</span>}
                    {project.managed_repo && <span className="rounded-full bg-accent-cyan/15 px-2 py-0.5 text-[11px] text-accent-cyan">Dikelola DevControl</span>}
                    {project.temporary && <span className="rounded-full bg-accent-amber/10 px-2 py-0.5 text-[11px] text-amber-300">Sisa uji update diri, aman dihapus</span>}
                  </div>}
                  {admin && (unconnected || project.status === "outside") && !project.self && <div className="mt-auto flex flex-wrap justify-end gap-1.5 border-t border-base-border pt-2">
                    {unconnected && <button type="button" onClick={() => { setNotice(""); setConnecting(project); }} disabled={!data.github.ok}
                      className="inline-flex items-center gap-1 rounded-md bg-accent-blue px-2 py-1 text-xs font-semibold text-white hover:opacity-90 disabled:opacity-50">
                      <Link2 size={12} /> Connect Git</button>}
                    <button type="button" onClick={() => { setNotice(""); setDeleting(project); }} disabled={Boolean(project.managed_repo)}
                      title={project.managed_repo ? `Dipakai aplikasi DevControl ${project.managed_repo}; hapus lewat kartu repo di bawah` : undefined}
                      className="inline-flex items-center gap-1 rounded-md border border-accent-red/40 px-2 py-1 text-xs text-red-300 hover:bg-accent-red/10 disabled:opacity-40">
                      <Trash2 size={12} /> Hapus</button>
                  </div>}
                </article>
              );
            })}
          </div>
        )}
      </>}

      {connecting && <ConnectGitDialog project={connecting} repos={data.repos} onClose={() => setConnecting(null)} onChanged={changed} />}
      {deleting && <DeleteProjectDialog project={deleting} onClose={() => setDeleting(null)}
        onDeleted={(name) => { setDeleting(null); setNotice(`Project ${name} sudah dihapus dari Vercel.`); changed(); }} />}
    </section>
  );
}
