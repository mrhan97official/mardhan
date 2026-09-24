"use client";

import { FolderKanban, RefreshCw } from "lucide-react";
import AppShell from "@/components/AppShell";
import ComingSoon from "@/components/ComingSoon";
import ProjectCard from "@/components/ProjectCard";
import ZipArchivePanel from "@/components/ZipArchivePanel";
import { useOfflineData } from "@/lib/useOfflineData";
import { fallbackGithubRepos, fallbackServices } from "@/lib/fallbackData";
import { mergeProjects } from "@/lib/projectRepos";
import type { GithubRepo, Service } from "@/lib/types";

export default function ProjectsPage() {
  const repos = useOfflineData<GithubRepo[]>("github-repos", "/api/github-repos", fallbackGithubRepos);
  const services = useOfflineData<Service[]>("services", "/api/services", fallbackServices);
  const apps = mergeProjects(repos.data, services.data);
  const loading = repos.loading || services.loading;

  return (
    <AppShell
      title="Projects"
      subtitle="Repository GitHub yang dapat diakses dan aplikasi yang tersimpan"
      isOffline={repos.isOffline || services.isOffline}
    >
      <div className="flex items-center justify-between">
        <p className="text-sm text-slate-400">
          {loading && apps.length === 0 ? "Memuat repo GitHub..." : `${apps.length} repository ditemukan`}
        </p>
        <button
          type="button"
          onClick={() => { repos.reload(); services.reload(); }}
          disabled={repos.loading || services.loading}
          className="flex items-center gap-1.5 rounded-xl border border-base-border px-3 py-1.5 text-xs font-medium text-slate-300 hover:bg-base-800 disabled:opacity-60"
        >
          <RefreshCw size={13} className={repos.loading || services.loading ? "animate-spin" : ""} />
          Segarkan
        </button>
      </div>

      {repos.error && (
        <div role="alert" className="rounded-xl border border-amber-500/30 bg-amber-500/10 px-4 py-3 text-sm text-amber-200">
          Gagal membaca repo GitHub: {repos.error}. Periksa GITHUB_TOKEN di Vercel dan akses token ke repo tersebut.
          {repos.data.length > 0 && " Menampilkan daftar yang tersimpan sebelumnya."}
        </div>
      )}
      {services.error && (
        <div role="alert" className="rounded-xl border border-amber-500/30 bg-amber-500/10 px-4 py-3 text-sm text-amber-200">
          Data deployment D1 belum tersedia: {services.error}. Repo GitHub tetap ditampilkan tanpa URL aplikasi.
        </div>
      )}

      {loading && apps.length === 0 ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="card h-44 animate-pulse bg-base-800/40" />
          ))}
        </div>
      ) : apps.length === 0 ? (
        <ComingSoon
          icon={FolderKanban}
          title={repos.error ? "Repo GitHub belum dapat dibaca" : "Belum ada repository yang dapat ditampilkan"}
          description={
            repos.error
              ? "Periksa token GitHub, hak akses repo, lalu coba Segarkan."
              : "Pastikan token GitHub memiliki akses ke repo yang ingin ditampilkan."
          }
        />
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {apps.map(({ service, repo, source }) => (
            <ProjectCard key={repo.full_name.toLowerCase()} repo={repo} appUrl={service?.app_url} linked={!!service} source={source} />
          ))}
        </div>
      )}
      <ZipArchivePanel />
    </AppShell>
  );
}
