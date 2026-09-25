"use client";

import { useEffect, useState } from "react";
import { FolderKanban, RefreshCw, Trash2, X } from "lucide-react";
import AppShell from "@/components/AppShell";
import ComingSoon from "@/components/ComingSoon";
import ProjectCard from "@/components/ProjectCard";
import AppsPromoBanner from "@/components/AppsPromoBanner";
import ZipArchivePanel from "@/components/ZipArchivePanel";
import { useOfflineData } from "@/lib/useOfflineData";
import { fallbackGithubRepos, fallbackServices } from "@/lib/fallbackData";
import { mergeProjects } from "@/lib/projectRepos";
import { cacheSet } from "@/lib/db";
import { notifyDataChanged } from "@/lib/liveUpdates";
import type { GithubRepo, Service } from "@/lib/types";

interface Thumbnail { repo: string; version: string }

function uploadOriginal(file: File, url: string, contentType: string, progress: (value: number) => void): Promise<void> {
  return new Promise((resolve, reject) => {
    const request = new XMLHttpRequest();
    request.open("PUT", url);
    request.setRequestHeader("Content-Type", contentType);
    request.upload.onprogress = (event) => {
      if (event.lengthComputable) progress(Math.round(event.loaded / event.total * 100));
    };
    request.onload = () => request.status >= 200 && request.status < 300
      ? resolve()
      : reject(new Error(request.status === 403
        ? "R2 menolak unggahan (403). Pastikan CF_API_TOKEN memiliki izin R2 Object Read & Write pada bucket ini, atau gunakan kredensial R2 S3 khusus bucket di Vercel."
        : `R2 menolak gambar asli (HTTP ${request.status}).`));
    request.onerror = () => reject(new Error("Gagal mengunggah langsung ke R2. Periksa koneksi dan aturan CORS bucket untuk domain aplikasi ini."));
    request.onabort = () => reject(new Error("Unggahan dibatalkan."));
    request.send(file);
  });
}

export default function ProjectsPage() {
  const repos = useOfflineData<GithubRepo[]>("github-repos-v16", "/api/github-repos", fallbackGithubRepos, 15000);
  const services = useOfflineData<Service[]>("services-v16", "/api/services", fallbackServices, 10000);
  const thumbnails = useOfflineData<Thumbnail[]>("project-thumbnails-v17", "/api/project-thumbnails", [], 10000);
  const [target, setTarget] = useState<string | null>(null);
  const [confirmation, setConfirmation] = useState("");
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [deleted, setDeleted] = useState<string[]>([]);
  const [archiveRefresh, setArchiveRefresh] = useState(0);
  const [uploading, setUploading] = useState<string | null>(null);
  const [uploadProgress, setUploadProgress] = useState<number | null>(null);
  const [imageError, setImageError] = useState<{ repo: string; message: string } | null>(null);
  const thumbnailByRepo = new Map(thumbnails.data.map((item) => [item.repo.toLowerCase(), item.version]));
  const apps = mergeProjects(repos.data, services.data).filter(({ repo }) => !deleted.includes(repo.full_name.toLowerCase()));
  const loading = repos.loading || services.loading;

  // Remove local deletion markers once both remote lists confirm removal.
  // A newly created repo with the same name can then appear again normally.
  useEffect(() => {
    if (repos.error || services.error) return;
    setDeleted((current) => current.filter((key) => repos.data.some((repo) => repo.full_name.toLowerCase() === key) || services.data.some((service) => service.repo?.toLowerCase() === key)));
  }, [repos.data, services.data, repos.error, services.error]);

  async function changeThumbnail(repo: string, file?: File) {
    if (uploading) return;
    setImageError(null);
    const contentType = file?.type || (file && /\.jpe?g$/i.test(file.name) ? "image/jpeg" : file && /\.png$/i.test(file.name) ? "image/png" : "");
    if (file && (!file.size || file.size > 5 * 1024 ** 3 || !["image/jpeg", "image/png"].includes(contentType))) {
      setImageError({ repo, message: "Pilih JPG/PNG asli. R2 membatasi satu unggahan hingga 5 GiB." });
      return;
    }
    setUploading(repo);
    setUploadProgress(null);
    let uploadID: string | null = null;
    try {
      if (file) {
        const begin = await fetch("/api/project-thumbnails", {
          method: "POST", credentials: "same-origin", cache: "no-store",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ action: "begin", repo, content_type: contentType, size_bytes: file.size }),
        });
        const ticket = await begin.json().catch(() => null);
        if (!begin.ok) throw new Error(ticket?.error || `Gagal menyiapkan unggahan (HTTP ${begin.status})`);
        if (!ticket?.upload_id || !ticket?.upload_url) throw new Error("Tiket unggahan tidak valid.");
        uploadID = ticket.upload_id;
        await uploadOriginal(file, ticket.upload_url, contentType, setUploadProgress);
        const finish = await fetch("/api/project-thumbnails", {
          method: "POST", credentials: "same-origin", cache: "no-store",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ action: "finish", upload_id: uploadID }),
        });
        const result = await finish.json().catch(() => null);
        if (!finish.ok) throw new Error(result?.error || `Gagal memverifikasi unggahan (HTTP ${finish.status})`);
      } else {
        const response = await fetch(`/api/project-thumbnails?repo=${encodeURIComponent(repo)}`, {
          method: "DELETE", credentials: "same-origin", cache: "no-store",
        });
        if (!response.ok) {
          const result = await response.json().catch(() => null);
          throw new Error(result?.error || `Gagal menghapus thumbnail (HTTP ${response.status})`);
        }
      }
      notifyDataChanged();
    } catch (cause) {
      if (uploadID) {
        void fetch(`/api/project-thumbnails?upload_id=${encodeURIComponent(uploadID)}`, {
          method: "DELETE", credentials: "same-origin", cache: "no-store",
        }).catch(() => {});
      }
      setImageError({ repo, message: cause instanceof Error ? cause.message : "Gagal mengubah thumbnail." });
    } finally {
      setUploading(null);
      setUploadProgress(null);
    }
  }

  function openDelete(repo: string) {
    setTarget(repo);
    setConfirmation("");
    setDeleteError(null);
  }

  async function deleteProject() {
    if (!target || confirmation !== target || deleting) return;
    setDeleting(true);
    setDeleteError(null);
    try {
      const response = await fetch("/api/project", {
        method: "DELETE",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ repo: target, confirmation }),
        cache: "no-store",
      });
      const body = await response.json().catch(() => null);
      if (!response.ok) throw new Error(body?.error || `Gagal menghapus (HTTP ${response.status})`);
      const key = target.toLowerCase();
      setDeleted((current) => [...current, key]);
      await Promise.all([
        cacheSet("github-repos-v16", repos.data.filter((repo) => repo.full_name.toLowerCase() !== key)),
        cacheSet("services-v16", services.data.filter((service) => service.repo?.toLowerCase() !== key)),
      ]).catch(() => {});
      setArchiveRefresh((current) => current + 1);
      setTarget(null);
      notifyDataChanged();
    } catch (cause) {
      setDeleteError(cause instanceof Error ? cause.message : "Penghapusan gagal. Coba lagi untuk melanjutkan.");
    } finally {
      setDeleting(false);
    }
  }

  return (
    <AppShell
      title="Projects"
      subtitle="Repository GitHub yang dapat diakses dan aplikasi yang tersimpan"
      isOffline={repos.isOffline || services.isOffline}
    >
      <AppsPromoBanner />

      <div className="flex items-center justify-between">
        <p className="text-sm text-slate-400">
          {loading && apps.length === 0 ? "Memuat repo GitHub..." : `${apps.length} repository ditemukan`}
        </p>
        <button
          type="button"
          onClick={() => { repos.reload(); services.reload(); thumbnails.reload(); }}
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
      {thumbnails.error && (
        <p role="alert" className="text-xs text-amber-300">Thumbnail tersimpan belum dapat dimuat: {thumbnails.error}. Gambar bawaan tetap tersedia.</p>
      )}

      {loading && apps.length === 0 ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 md:grid-cols-3 md:gap-3 xl:gap-4">
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
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 md:grid-cols-3 md:gap-3 xl:gap-4">
          {apps.map(({ service, repo, source }) => (
            <ProjectCard key={repo.full_name.toLowerCase()} repo={repo} appUrl={service?.app_url} linked={!!service} source={source}
              thumbnailVersion={thumbnailByRepo.get(repo.full_name.toLowerCase())} uploading={uploading === repo.full_name}
              uploadProgress={uploading === repo.full_name ? uploadProgress : null}
              imageError={imageError?.repo === repo.full_name ? imageError.message : undefined}
              onUpload={(file) => { void changeThumbnail(repo.full_name, file); }}
              onRemoveThumbnail={() => { void changeThumbnail(repo.full_name); }} onDelete={() => openDelete(repo.full_name)} />
          ))}
        </div>
      )}
      <ZipArchivePanel key={archiveRefresh} />
      {target && (
        <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/75 p-4" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget && !deleting) setTarget(null); }}>
          <div role="dialog" aria-modal="true" aria-labelledby="delete-project-title" className="w-full max-w-md rounded-2xl border border-red-500/30 bg-base-900 p-5 shadow-2xl">
            <div className="flex items-start justify-between gap-3">
              <h2 id="delete-project-title" className="flex items-center gap-2 text-base font-bold text-white"><Trash2 size={18} className="text-red-400" /> Hapus aplikasi</h2>
              <button type="button" aria-label="Tutup" disabled={deleting} onClick={() => setTarget(null)} className="text-slate-400 hover:text-white disabled:opacity-50"><X size={18} /></button>
            </div>
            <p className="mt-3 break-all text-sm font-medium text-white">{target}</p>
            <p className="mt-2 text-sm text-slate-400">Tindakan ini menghapus repo GitHub, project Vercel beserta deployment dan domainnya, arsip ZIP aplikasi, serta data terkait di DevControl. Tidak dapat dibatalkan.</p>
            <label htmlFor="confirm-project-delete" className="mt-5 block text-xs font-medium text-slate-300">Ketik <span className="select-all font-bold text-white">{target}</span> untuk mengonfirmasi</label>
            <input id="confirm-project-delete" autoFocus type="text" autoComplete="off" spellCheck={false} value={confirmation} disabled={deleting} onChange={(event) => setConfirmation(event.target.value)} className="mt-2 w-full rounded-xl border border-base-border bg-base-850 px-3 py-2 text-sm text-white focus:border-red-400 focus:outline-none" />
            {deleteError && <p role="alert" className="mt-3 text-xs text-red-300">{deleteError}</p>}
            <div className="mt-5 flex justify-end gap-2">
              <button type="button" disabled={deleting} onClick={() => setTarget(null)} className="rounded-xl border border-base-border px-4 py-2 text-sm text-slate-300 disabled:opacity-50">Batal</button>
              <button type="button" disabled={deleting || confirmation !== target} onClick={() => void deleteProject()} className="rounded-xl bg-red-600 px-4 py-2 text-sm font-semibold text-white hover:bg-red-500 disabled:cursor-not-allowed disabled:opacity-50">{deleting ? "Menghapus..." : "Hapus permanen"}</button>
            </div>
          </div>
        </div>
      )}
    </AppShell>
  );
}
