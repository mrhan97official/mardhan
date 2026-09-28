"use client";

import { useEffect, useMemo, useState } from "react";
import { AlertTriangle, CheckCircle2, ExternalLink, Loader2, Search, Trash2 } from "lucide-react";
import Modal from "@/components/deployment/Modal";

export type InventoryStatus = "connected" | "outside" | "other-git" | "unconnected";
export interface InventoryRepo {
  full_name: string; name: string; private: boolean; archived: boolean; fork: boolean;
  html_url: string; pushed_at: string; default_branch: string; projects: string[];
}
export interface InventoryProject {
  id: string; name: string; framework?: string; updated_at?: number; git_provider?: string; repo?: string;
  production_branch?: string; status: InventoryStatus; suggested_repo?: string; url?: string;
  managed_repo?: string; self?: boolean;
}

async function inventoryAction<T>(payload: Record<string, unknown>): Promise<T> {
  const response = await fetch("/api/databases?view=inventory", {
    method: "POST", headers: { "Content-Type": "application/json" }, credentials: "same-origin", cache: "no-store",
    body: JSON.stringify(payload),
  });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error((body as { error?: string }).error || `HTTP ${response.status}`);
  return body as T;
}

interface LinkResult { repo: string; branch: string; deployment_url?: string; deploy_error?: string }

export function ConnectGitDialog({ project, repos, onClose, onChanged }: {
  project: InventoryProject; repos: InventoryRepo[]; onClose: () => void; onChanged: () => void;
}) {
  const initial = project.suggested_repo || project.managed_repo || "";
  const [repo, setRepo] = useState(initial);
  const [filter, setFilter] = useState("");
  const [deploy, setDeploy] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<LinkResult | null>(null);

  const options = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    return repos
      .filter((item) => !needle || item.full_name.toLowerCase().includes(needle) || item.full_name === repo)
      .sort((a, b) => Number(a.archived) - Number(b.archived) || Number(b.full_name === initial) - Number(a.full_name === initial));
  }, [repos, filter, repo, initial]);
  const selected = repos.find((item) => item.full_name === repo);

  async function submit() {
    if (!repo) return;
    setBusy(true);
    setError("");
    try {
      const body = await inventoryAction<LinkResult>({ action: "link-project", project_id: project.id, repo, deploy });
      setResult(body);
      onChanged();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Gagal menghubungkan repo.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal title="Connect Git" onClose={busy ? () => undefined : onClose} widthClassName="max-w-lg">
      {result ? (
        <div className="space-y-3 text-sm">
          <p className="flex items-start gap-2 rounded-lg bg-accent-green/10 p-2 text-emerald-300">
            <CheckCircle2 size={17} className="mt-0.5 shrink-0" />
            <span><b>{project.name}</b> sekarang terhubung ke <b>{result.repo}</b>. Setiap push ke <b>{result.branch}</b> akan dideploy otomatis oleh Vercel.</span>
          </p>
          {result.deploy_error
            ? <p className="rounded-lg bg-accent-amber/10 p-2 text-xs text-amber-300">{result.deploy_error}</p>
            : result.deployment_url
              ? <p className="text-xs text-slate-400">Import dari GitHub sudah dimulai. Build berjalan di Vercel dan biasanya selesai dalam beberapa menit.</p>
              : null}
          <div className="flex flex-wrap justify-end gap-2">
            {project.url && <a href={`${project.url}/deployments`} target="_blank" rel="noopener noreferrer"
              className="inline-flex items-center gap-1 rounded-lg border border-base-border px-3 py-2 text-xs hover:bg-base-800">Lihat deployment <ExternalLink size={12} /></a>}
            <button type="button" onClick={onClose} className="rounded-lg bg-accent-blue px-3 py-2 text-sm font-semibold text-white">Selesai</button>
          </div>
        </div>
      ) : (
        <div className="space-y-3 text-sm">
          <p className="text-slate-400">Hubungkan project <b className="text-slate-100">{project.name}</b> ke repo GitHub, lalu impor kodenya langsung dari branch utama repo tersebut.</p>
          <label className="relative block">
            <span className="sr-only">Cari repo</span>
            <Search size={14} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-slate-500" />
            <input value={filter} onChange={(event) => setFilter(event.target.value)} placeholder="Cari repo…"
              className="w-full rounded-lg border border-base-border bg-base-850 py-2 pl-8 pr-2 text-sm outline-none focus:border-accent-blue" />
          </label>
          <label className="block">
            <span className="mb-1 block text-xs text-slate-400">Repo GitHub</span>
            <select value={repo} onChange={(event) => setRepo(event.target.value)}
              className="w-full rounded-lg border border-base-border bg-base-850 p-2 text-sm outline-none focus:border-accent-blue">
              <option value="">— Pilih repo —</option>
              {options.map((item) => (
                <option key={item.full_name} value={item.full_name}>
                  {item.full_name}{item.full_name === initial ? " (disarankan)" : ""}{item.archived ? " (diarsipkan)" : ""}{item.private ? " 🔒" : ""}
                </option>
              ))}
            </select>
          </label>
          {selected && <p className="text-xs text-slate-400">Branch utama: <b className="text-slate-200">{selected.default_branch}</b>
            {selected.projects.length > 0 && <span className="text-amber-300"> · repo ini sudah dipakai project {selected.projects.join(", ")}</span>}</p>}
          <label className="flex items-start gap-2 text-sm">
            <input type="checkbox" checked={deploy} onChange={(event) => setDeploy(event.target.checked)} className="mt-1" />
            <span>Impor &amp; deploy dari GitHub sekarang<span className="block text-xs text-slate-400">Jika dimatikan, deployment baru terjadi pada push berikutnya.</span></span>
          </label>
          <p className="text-xs text-slate-500">Vercel harus punya akses ke repo ini (GitHub → Settings → Applications → Vercel → Repository access). Pengaturan build project tidak diubah.</p>
          {error && <p role="alert" className="rounded-lg bg-accent-red/10 p-2 text-xs text-red-300">{error}</p>}
          <div className="flex justify-end gap-2">
            <button type="button" onClick={onClose} disabled={busy} className="rounded-lg border border-base-border px-3 py-2 text-sm hover:bg-base-800 disabled:opacity-50">Batal</button>
            <button type="button" onClick={() => void submit()} disabled={busy || !repo}
              className="inline-flex items-center gap-1.5 rounded-lg bg-accent-blue px-3 py-2 text-sm font-semibold text-white disabled:opacity-50">
              {busy && <Loader2 size={15} className="animate-spin" />}{busy ? "Menghubungkan…" : "Connect & impor"}
            </button>
          </div>
        </div>
      )}
    </Modal>
  );
}

interface DeletePreview { managed_repo?: string; domains: string[]; domains_error?: string }

export function DeleteProjectDialog({ project, onClose, onDeleted }: {
  project: InventoryProject; onClose: () => void; onDeleted: (name: string) => void;
}) {
  const [preview, setPreview] = useState<DeletePreview | null>(null);
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let active = true;
    inventoryAction<DeletePreview>({ action: "delete-preview", project_id: project.id })
      .then((body) => { if (active) setPreview(body); })
      .catch((cause) => { if (active) setError(cause instanceof Error ? cause.message : "Gagal memeriksa project."); });
    return () => { active = false; };
  }, [project.id]);

  async function remove() {
    setBusy(true);
    setError("");
    try {
      await inventoryAction({ action: "delete-project", project_id: project.id, confirm });
      onDeleted(project.name);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Penghapusan gagal.");
      setBusy(false);
    }
  }

  const blocked = Boolean(preview?.managed_repo);
  return (
    <Modal title="Hapus project Vercel" onClose={busy ? () => undefined : onClose} widthClassName="max-w-lg">
      <div className="space-y-3 text-sm">
        <p className="flex items-start gap-2 rounded-lg bg-accent-red/10 p-2 text-red-300">
          <AlertTriangle size={17} className="mt-0.5 shrink-0" />
          <span>Project <b>{project.name}</b> akan dihapus permanen dari Vercel beserta semua deployment, URL .vercel.app, domain yang terpasang, environment variable, dan log-nya. Tindakan ini tidak bisa dibatalkan.</span>
        </p>
        {!preview && !error && <p className="flex items-center gap-2 text-xs text-slate-400"><Loader2 size={14} className="animate-spin" /> Memeriksa project…</p>}
        {preview && blocked && <p className="rounded-lg bg-accent-amber/10 p-2 text-xs text-amber-300">
          Project ini dipakai aplikasi DevControl <b>{preview.managed_repo}</b>. Hapus aplikasinya lewat halaman Projects agar data D1, arsip ZIP, dan thumbnail ikut dibersihkan, atau pilih Connect Git.</p>}
        {preview && !blocked && <>
          {preview.domains.length > 0 && <div className="rounded-lg border border-base-border bg-base-850 p-2 text-xs">
            <p className="text-slate-400">Domain custom yang ikut dilepas:</p>
            <p className="mt-1 break-words text-slate-200">{preview.domains.join(", ")}</p>
          </div>}
          {preview.domains_error && <p className="text-xs text-amber-300">Daftar domain tidak terbaca lengkap: {preview.domains_error}</p>}
          <label className="block">
            <span className="mb-1 block text-xs text-slate-400">Ketik <b className="font-mono text-slate-200">{project.name}</b> untuk mengonfirmasi</span>
            <input value={confirm} onChange={(event) => setConfirm(event.target.value)} autoComplete="off" spellCheck={false}
              className="w-full rounded-lg border border-base-border bg-base-850 p-2 font-mono text-sm outline-none focus:border-accent-red" />
          </label>
        </>}
        {error && <p role="alert" className="rounded-lg bg-accent-red/10 p-2 text-xs text-red-300">{error}</p>}
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} disabled={busy} className="rounded-lg border border-base-border px-3 py-2 text-sm hover:bg-base-800 disabled:opacity-50">Batal</button>
          <button type="button" onClick={() => void remove()} disabled={busy || !preview || blocked || confirm.trim() !== project.name}
            className="inline-flex items-center gap-1.5 rounded-lg bg-accent-red px-3 py-2 text-sm font-semibold text-white disabled:opacity-50">
            {busy ? <Loader2 size={15} className="animate-spin" /> : <Trash2 size={15} />}{busy ? "Menghapus…" : "Hapus permanen"}
          </button>
        </div>
      </div>
    </Modal>
  );
}
