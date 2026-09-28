"use client";

import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Archive, ExternalLink, GitBranch, GitFork, Github, Link2, Loader2, Lock, RefreshCw, Search, Trash2, Triangle, Unlink } from "lucide-react";
import { ConnectGitDialog, DeleteProjectDialog, type InventoryProject, type InventoryRepo, type InventoryStatus } from "@/components/InventoryProjectActions";

type Status = InventoryStatus;
interface Source { configured: boolean; ok: boolean; error?: string; truncated?: boolean }
interface Inventory { github: Source; vercel: Source; repos: InventoryRepo[]; projects: InventoryProject[]; checked_at: string }

type Tab = "projects" | "repos";
type ProjectFilter = "all" | Status;
type RepoFilter = "all" | "with" | "without";

const STATUS_LABEL: Record<Status, string> = {
  connected: "Terhubung",
  outside: "Repo di luar token",
  "other-git": "Git lain",
  unconnected: "Belum connect Git",
};
const STATUS_STYLE: Record<Status, string> = {
  connected: "bg-accent-green/10 text-emerald-300",
  outside: "bg-accent-purple/15 text-accent-purple",
  "other-git": "bg-accent-cyan/15 text-accent-cyan",
  unconnected: "bg-accent-amber/10 text-amber-300",
};

function formatDate(value?: string | number): string {
  if (!value) return "";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "" : date.toLocaleDateString("id-ID", { day: "numeric", month: "short", year: "numeric" });
}

function Chip({ active, onClick, children }: { active: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button type="button" onClick={onClick} aria-pressed={active}
      className={`rounded-full border px-2.5 py-1 text-xs transition-colors ${active ? "border-accent-blue bg-accent-blue/15 text-slate-100" : "border-base-border text-slate-400 hover:bg-base-800"}`}>
      {children}
    </button>
  );
}

export default function RepoProjectInventory() {
  const [data, setData] = useState<Inventory | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [tab, setTab] = useState<Tab>("projects");
  const [projectFilter, setProjectFilter] = useState<ProjectFilter>("all");
  const [repoFilter, setRepoFilter] = useState<RepoFilter>("all");
  const [search, setSearch] = useState("");
  const [connecting, setConnecting] = useState<InventoryProject | null>(null);
  const [deleting, setDeleting] = useState<InventoryProject | null>(null);
  const [notice, setNotice] = useState("");
  const focusedOnce = useRef(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await fetch("/api/databases?view=inventory", { cache: "no-store", credentials: "same-origin" });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error((body as { error?: string }).error || `HTTP ${response.status}`);
      const inventory = body as Inventory;
      setData(inventory);
      setError("");
      // First load: jump straight to the projects that still need a Git repo.
      if (!focusedOnce.current) {
        focusedOnce.current = true;
        if (inventory.projects.some((project) => project.status === "unconnected")) setProjectFilter("unconnected");
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Data repo dan project gagal dimuat.");
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => { void load(); }, [load]);

  const counts = useMemo(() => {
    const projects = data?.projects || [];
    const repos = data?.repos || [];
    const byStatus = { connected: 0, outside: 0, "other-git": 0, unconnected: 0 } as Record<Status, number>;
    for (const project of projects) byStatus[project.status] = (byStatus[project.status] || 0) + 1;
    const matched = repos.filter((repo) => repo.projects.length > 0).length;
    return {
      projects: projects.length, repos: repos.length, byStatus, matched, withoutProject: repos.length - matched,
      privateRepos: repos.filter((repo) => repo.private).length, archivedRepos: repos.filter((repo) => repo.archived).length,
    };
  }, [data]);

  const needle = search.trim().toLowerCase();
  const visibleProjects = useMemo(() => (data?.projects || []).filter((project) =>
    (projectFilter === "all" || project.status === projectFilter) &&
    (!needle || project.name.toLowerCase().includes(needle) || (project.repo || "").toLowerCase().includes(needle) ||
      (project.suggested_repo || "").toLowerCase().includes(needle))), [data, projectFilter, needle]);
  const visibleRepos = useMemo(() => (data?.repos || []).filter((repo) =>
    (repoFilter === "all" || (repoFilter === "with" ? repo.projects.length > 0 : repo.projects.length === 0)) &&
    (!needle || repo.full_name.toLowerCase().includes(needle) || repo.projects.some((name) => name.toLowerCase().includes(needle)))),
  [data, repoFilter, needle]);

  function showUnconnected() { setTab("projects"); setProjectFilter("unconnected"); setSearch(""); }
  function showReposWithout() { setTab("repos"); setRepoFilter("without"); setSearch(""); }

  return (
    <section className="card space-y-2 p-2">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <h2 className="flex items-center gap-2 text-base font-semibold"><GitBranch size={18} /> Repo GitHub &amp; project Vercel</h2>
          <p className="mt-1 text-xs text-slate-400">Mencocokkan repo yang terlihat oleh GITHUB_TOKEN dengan project di akun/team VERCEL_TOKEN. Hanya membaca, tidak mengubah apa pun.</p>
        </div>
        <button type="button" onClick={() => void load()} disabled={loading} aria-label="Muat ulang data repo dan project"
          className="rounded-lg border border-base-border px-3 py-2 text-sm hover:bg-base-800 disabled:opacity-50">
          {loading ? <Loader2 size={16} className="animate-spin" /> : <RefreshCw size={16} />}
        </button>
      </div>

      {error && <p role="alert" className="rounded-lg bg-accent-red/10 p-2 text-sm text-red-300">{error}</p>}
      {notice && <p role="status" className="flex items-start justify-between gap-2 rounded-lg bg-accent-green/10 p-2 text-sm text-emerald-300">
        <span>{notice}</span><button type="button" onClick={() => setNotice("")} className="text-xs text-slate-400 hover:text-slate-200">Tutup</button></p>}
      {!data && !error && <p className="flex items-center gap-2 text-sm text-slate-400"><Loader2 size={14} className="animate-spin" /> Membaca GitHub dan Vercel…</p>}

      {data && <>
        <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
          <div className="rounded-xl border border-base-border bg-base-850 p-2">
            <p className="flex items-center gap-1.5 text-xs text-slate-400"><Github size={13} /> Repo GitHub</p>
            {data.github.ok ? <>
              <p className="mt-1 text-2xl font-bold tabular-nums">{counts.repos}</p>
              <p className="text-xs text-slate-400">{counts.privateRepos} privat · {counts.archivedRepos} diarsipkan{data.github.truncated ? " · daftar terpotong" : ""}</p>
            </> : <p className="mt-1 break-words text-xs text-amber-300">{data.github.error || "Tidak dapat dibaca."}</p>}
          </div>
          <div className="rounded-xl border border-base-border bg-base-850 p-2">
            <p className="flex items-center gap-1.5 text-xs text-slate-400"><Triangle size={12} /> Project Vercel</p>
            {data.vercel.ok ? <>
              <p className="mt-1 text-2xl font-bold tabular-nums">{counts.projects}</p>
              <p className="text-xs text-slate-400">{counts.byStatus.connected + counts.byStatus.outside + counts.byStatus["other-git"]} terhubung Git{data.vercel.truncated ? " · daftar terpotong" : ""}</p>
            </> : <p className="mt-1 break-words text-xs text-amber-300">{data.vercel.error || "Tidak dapat dibaca."}</p>}
          </div>
          <button type="button" onClick={showReposWithout} disabled={!data.github.ok}
            className="rounded-xl border border-base-border bg-base-850 p-2 text-left hover:bg-base-800 disabled:cursor-default disabled:hover:bg-base-850">
            <p className="flex items-center gap-1.5 text-xs text-slate-400"><GitBranch size={13} /> Repo ↔ project cocok</p>
            <p className="mt-1 text-2xl font-bold tabular-nums">{data.github.ok && data.vercel.ok ? counts.matched : "–"}</p>
            <p className="text-xs text-slate-400">{data.github.ok && data.vercel.ok ? `${counts.withoutProject} repo belum punya project Vercel` : "Butuh kedua token"}</p>
          </button>
          <button type="button" onClick={showUnconnected} disabled={!data.vercel.ok}
            className={`rounded-xl border p-2 text-left disabled:cursor-default ${counts.byStatus.unconnected > 0 ? "border-accent-amber/40 bg-accent-amber/10 hover:bg-accent-amber/15" : "border-base-border bg-base-850 hover:bg-base-800"}`}>
            <p className="flex items-center gap-1.5 text-xs text-slate-400"><Unlink size={13} /> Belum connect Git</p>
            <p className={`mt-1 text-2xl font-bold tabular-nums ${counts.byStatus.unconnected > 0 ? "text-amber-300" : "text-emerald-300"}`}>{data.vercel.ok ? counts.byStatus.unconnected : "–"}</p>
            <p className="text-xs text-slate-400">{counts.byStatus.unconnected > 0 ? "Klik untuk melihat daftarnya" : data.vercel.ok ? "Semua project sudah terhubung" : "Vercel tidak terbaca"}</p>
          </button>
        </div>

        {counts.byStatus.outside > 0 && <p className="rounded-lg bg-accent-purple/10 p-2 text-xs text-accent-purple">
          {counts.byStatus.outside} project terhubung ke repo yang tidak terlihat oleh GITHUB_TOKEN (akun/organisasi lain atau repo sudah dihapus/diganti nama).</p>}

        <div className="flex flex-wrap items-center gap-2 border-b border-base-border pb-2">
          <div role="tablist" className="flex rounded-lg border border-base-border p-0.5">
            <button type="button" role="tab" aria-selected={tab === "projects"} onClick={() => setTab("projects")}
              className={`rounded-md px-3 py-1.5 text-sm ${tab === "projects" ? "bg-base-800 font-semibold text-slate-100" : "text-slate-400"}`}>Project Vercel ({counts.projects})</button>
            <button type="button" role="tab" aria-selected={tab === "repos"} onClick={() => setTab("repos")}
              className={`rounded-md px-3 py-1.5 text-sm ${tab === "repos" ? "bg-base-800 font-semibold text-slate-100" : "text-slate-400"}`}>Repo GitHub ({counts.repos})</button>
          </div>
          <label className="relative ml-auto w-full sm:w-64">
            <span className="sr-only">Cari repo atau project</span>
            <Search size={14} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-slate-500" />
            <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Cari nama…"
              className="w-full rounded-lg border border-base-border bg-base-850 py-1.5 pl-8 pr-2 text-sm outline-none focus:border-accent-blue" />
          </label>
        </div>

        {tab === "projects" ? <>
          <div className="flex flex-wrap gap-1.5">
            <Chip active={projectFilter === "all"} onClick={() => setProjectFilter("all")}>Semua {counts.projects}</Chip>
            {(["unconnected", "connected", "outside", "other-git"] as Status[]).filter((status) => status === "unconnected" || status === "connected" || counts.byStatus[status] > 0)
              .map((status) => <Chip key={status} active={projectFilter === status} onClick={() => setProjectFilter(status)}>{STATUS_LABEL[status]} {counts.byStatus[status]}</Chip>)}
          </div>
          {!data.vercel.ok ? <p className="text-sm text-slate-400">Daftar project Vercel tidak tersedia.</p>
            : visibleProjects.length === 0 ? <p className="py-3 text-center text-sm text-slate-400">{projectFilter === "unconnected" && !needle ? "Semua project Vercel sudah terhubung ke repo Git." : "Tidak ada project yang cocok."}</p>
            : <ul className="max-h-[30rem] space-y-1.5 overflow-y-auto pr-0.5">
              {visibleProjects.map((project) => {
                const unconnected = project.status === "unconnected";
                return (
                  <li key={project.id} className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-base-border bg-base-850 px-2 py-2">
                    <div className="min-w-0 flex-1">
                      <div className="flex min-w-0 items-center gap-2">
                        <span className="truncate text-sm font-medium text-slate-100">{project.name}</span>
                        {project.framework && <span className="shrink-0 text-xs text-slate-500">{project.framework}</span>}
                      </div>
                      {unconnected ? (
                        <p className="mt-0.5 break-words text-xs text-slate-400">
                          {project.suggested_repo ? <>Kemungkinan repo: <span className="text-slate-200">{project.suggested_repo}</span></> : "Tidak ada repo GitHub dengan nama serupa."}
                          {project.managed_repo && <span className="text-accent-cyan"> · dipakai aplikasi DevControl {project.managed_repo}</span>}
                          {project.self && <span className="text-accent-cyan"> · project aplikasi ini sendiri</span>}
                        </p>
                      ) : (
                        <p className="mt-0.5 flex min-w-0 items-center gap-1 text-xs text-slate-400">
                          <GitBranch size={12} className="shrink-0" />
                          {project.git_provider === "github" && project.repo
                            ? <a href={`https://github.com/${project.repo}`} target="_blank" rel="noopener noreferrer" className="truncate hover:text-slate-200 hover:underline">{project.repo}</a>
                            : <span className="truncate">{project.repo || project.git_provider}</span>}
                          {project.production_branch && <span className="shrink-0 text-slate-500">({project.production_branch})</span>}
                        </p>
                      )}
                    </div>
                    <div className="flex shrink-0 flex-wrap items-center justify-end gap-1.5">
                      <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_STYLE[project.status]}`}>{STATUS_LABEL[project.status]}</span>
                      {unconnected && !project.self && <>
                        <button type="button" onClick={() => { setNotice(""); setConnecting(project); }} disabled={!data.github.ok}
                          title={data.github.ok ? "Hubungkan ke repo GitHub dan impor kodenya" : "Butuh GITHUB_TOKEN"}
                          className="inline-flex items-center gap-1 rounded-md bg-accent-blue px-2 py-1 text-xs font-semibold text-white hover:opacity-90 disabled:opacity-50">
                          <Link2 size={12} /> Connect Git</button>
                        <button type="button" onClick={() => { setNotice(""); setDeleting(project); }} disabled={Boolean(project.managed_repo)}
                          title={project.managed_repo ? `Dipakai aplikasi DevControl ${project.managed_repo}; hapus lewat halaman Projects` : "Hapus project ini dari Vercel"}
                          aria-label={`Hapus project ${project.name}`}
                          className="inline-flex items-center gap-1 rounded-md border border-accent-red/40 px-2 py-1 text-xs text-red-300 hover:bg-accent-red/10 disabled:opacity-40">
                          <Trash2 size={12} /> Hapus</button>
                      </>}
                      {project.url && <a href={project.url} target="_blank" rel="noopener noreferrer" title="Buka di Vercel" aria-label={`Buka ${project.name} di Vercel`}
                        className="inline-flex items-center rounded-md border border-base-border p-1.5 text-slate-300 hover:bg-base-800"><ExternalLink size={12} /></a>}
                    </div>
                  </li>
                );
              })}
            </ul>}
        </> : <>
          <div className="flex flex-wrap gap-1.5">
            <Chip active={repoFilter === "all"} onClick={() => setRepoFilter("all")}>Semua {counts.repos}</Chip>
            <Chip active={repoFilter === "with"} onClick={() => setRepoFilter("with")}>Punya project {counts.matched}</Chip>
            <Chip active={repoFilter === "without"} onClick={() => setRepoFilter("without")}>Tanpa project {counts.withoutProject}</Chip>
          </div>
          {!data.github.ok ? <p className="text-sm text-slate-400">Daftar repo GitHub tidak tersedia.</p>
            : visibleRepos.length === 0 ? <p className="py-3 text-center text-sm text-slate-400">Tidak ada repo yang cocok.</p>
            : <ul className="max-h-[30rem] space-y-1.5 overflow-y-auto pr-0.5">
              {visibleRepos.map((repo) => (
                <li key={repo.full_name} className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-base-border bg-base-850 px-2 py-2">
                  <div className="min-w-0 flex-1">
                    <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                      <a href={repo.html_url || `https://github.com/${repo.full_name}`} target="_blank" rel="noopener noreferrer"
                        className="truncate text-sm font-medium text-slate-100 hover:underline">{repo.full_name}</a>
                      {repo.private && <span title="Privat" className="text-slate-500"><Lock size={12} /></span>}
                      {repo.archived && <span title="Diarsipkan" className="text-slate-500"><Archive size={12} /></span>}
                      {repo.fork && <span title="Fork" className="text-slate-500"><GitFork size={12} /></span>}
                    </div>
                    <p className="mt-0.5 text-xs text-slate-500">{repo.default_branch}{repo.pushed_at ? ` · push terakhir ${formatDate(repo.pushed_at)}` : ""}</p>
                  </div>
                  <div className="flex min-w-0 flex-wrap justify-end gap-1">
                    {repo.projects.length > 0
                      ? repo.projects.map((name) => <span key={name} className="inline-flex items-center gap-1 rounded-full bg-accent-green/10 px-2 py-0.5 text-xs text-emerald-300"><Triangle size={9} />{name}</span>)
                      : <span className="rounded-full bg-base-800 px-2 py-0.5 text-xs text-slate-400">{data.vercel.ok ? "Belum ada project Vercel" : "Vercel tidak terbaca"}</span>}
                  </div>
                </li>
              ))}
            </ul>}
        </>}
        <p className="text-right text-[11px] text-slate-500">Diperiksa {new Date(data.checked_at).toLocaleString("id-ID")}</p>
      </>}
      {connecting && data && <ConnectGitDialog project={connecting} repos={data.repos}
        onClose={() => setConnecting(null)} onChanged={() => void load()} />}
      {deleting && <DeleteProjectDialog project={deleting} onClose={() => setDeleting(null)}
        onDeleted={(name) => { setDeleting(null); setNotice(`Project ${name} sudah dihapus dari Vercel.`); void load(); }} />}
    </section>
  );
}
