"use client";

import { useCallback, useEffect, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { CheckCircle2, ChevronDown, Download, FileArchive, History, KeyRound, Loader2, RefreshCw, X, XCircle } from "lucide-react";

const archiveKeyStorage = "devcontrol-zip-archive-key";

function formatSize(bytes: number): string {
  if (!bytes) return "0 KB";
  return bytes >= 1024 * 1024 ? `${(bytes / 1024 / 1024).toFixed(2)} MB` : `${Math.max(1, Math.round(bytes / 1024))} KB`;
}

function formatDate(value: string): string {
  const parsed = Date.parse(value.includes("T") ? value : `${value.replace(" ", "T")}Z`);
  if (!Number.isFinite(parsed)) return value;
  return new Date(parsed).toLocaleString("id-ID", { dateStyle: "medium", timeStyle: "short" });
}

// Rendered into <body> so no card's stacking context can cover it.
function DialogShell({ title, icon, subtitle, onClose, children }: { title: string; icon: ReactNode; subtitle: string; onClose: () => void; children: ReactNode }) {
  const [mounted, setMounted] = useState(false);
  useEffect(() => {
    setMounted(true);
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);
  if (!mounted) return null;
  return createPortal(
    <div className="fixed inset-0 z-[100] flex items-end justify-center p-2 sm:items-center" role="dialog" aria-modal="true" aria-label={title}>
      <button type="button" aria-label="Tutup" onClick={onClose} className="absolute inset-0 bg-black/70 backdrop-blur-sm" />
      <div className="card relative flex max-h-[88vh] w-full max-w-lg flex-col p-2">
        <div className="flex items-start justify-between gap-2 border-b border-base-border pb-2">
          <div className="flex min-w-0 items-center gap-2">
            <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-accent-blue/15 text-accent-blue">{icon}</span>
            <div className="min-w-0">
              <h2 className="text-sm font-bold text-white">{title}</h2>
              <p className="truncate font-mono text-xs text-slate-400">{subtitle}</p>
            </div>
          </div>
          <button type="button" aria-label="Tutup" onClick={onClose} className="rounded-lg p-1.5 text-slate-400 hover:bg-base-800"><X size={17} /></button>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto pt-2">{children}</div>
      </div>
    </div>,
    document.body,
  );
}

interface ZipRecord {
  id: string;
  scope: "app" | "self";
  target: string;
  filename: string;
  size_bytes: number;
  sha256: string;
  status: "current" | "previous" | "pending" | "failed";
  created_at: string;
}

export function ZipArchiveDialog({ repo, onClose }: { repo: string; onClose: () => void }) {
  const [key, setKey] = useState("");
  const [items, setItems] = useState<ZipRecord[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [downloading, setDownloading] = useState<string | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(async (secret: string) => {
    if (!secret.trim()) { setError("Masukkan kunci arsip terlebih dahulu."); return; }
    setLoading(true);
    setError("");
    try {
      const response = await fetch(`/api/zip-archives?repo=${encodeURIComponent(repo)}`, { headers: { Authorization: `Bearer ${secret.trim()}` }, cache: "no-store" });
      const body = await response.json().catch(() => null);
      if (!response.ok) throw new Error(body?.error || `HTTP ${response.status}`);
      setItems(Array.isArray(body?.items) ? body.items : []);
      try { sessionStorage.setItem(archiveKeyStorage, secret.trim()); } catch { /* storage disabled */ }
    } catch (reason) {
      setItems(null);
      setError(reason instanceof Error ? reason.message : "Arsip gagal dimuat.");
    } finally { setLoading(false); }
  }, [repo]);

  useEffect(() => {
    let saved = "";
    try { saved = sessionStorage.getItem(archiveKeyStorage) || ""; } catch { /* storage disabled */ }
    if (saved) { setKey(saved); void load(saved); }
  }, [load]);

  async function download(item: ZipRecord) {
    setDownloading(item.id);
    setError("");
    try {
      const response = await fetch(`/api/zip-archives?id=${item.id}`, { headers: { Authorization: `Bearer ${key.trim()}` }, cache: "no-store" });
      if (!response.ok) {
        const body = await response.json().catch(() => null);
        throw new Error(body?.error || `HTTP ${response.status}`);
      }
      const url = URL.createObjectURL(await response.blob());
      const link = document.createElement("a");
      link.href = url;
      link.download = item.filename;
      document.body.append(link);
      link.click();
      link.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 30000);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "ZIP gagal diunduh.");
    } finally { setDownloading(null); }
  }

  const current = items?.filter((item) => item.status === "current") ?? [];
  const pending = items?.filter((item) => item.status === "pending") ?? [];

  return (
    <DialogShell title="Arsip ZIP" icon={<FileArchive size={18} />} subtitle={repo} onClose={onClose}>
      <div className="space-y-2">
        {items === null && (
          <form onSubmit={(event) => { event.preventDefault(); void load(key); }} className="space-y-2 rounded-xl border border-base-border bg-base-850 p-2">
            <label htmlFor="zip-key" className="flex items-center gap-1.5 text-xs font-medium text-slate-300"><KeyRound size={13} /> Kunci arsip</label>
            <input id="zip-key" type="password" autoComplete="current-password" value={key} onChange={(event) => setKey(event.target.value)}
              placeholder="ZIP_ARCHIVE_ACCESS_TOKEN (bawaan: kata sandi admin)" className="w-full rounded-lg border border-base-border bg-base-900 px-3 py-2 text-sm" />
            <button type="submit" disabled={loading} className="inline-flex items-center gap-1.5 rounded-lg bg-accent-blue px-3 py-2 text-xs font-semibold text-white disabled:opacity-50">
              {loading && <Loader2 size={13} className="animate-spin" />} Buka arsip
            </button>
          </form>
        )}

        {items !== null && current.length === 0 && pending.length === 0 && (
          <p className="rounded-xl border border-base-border bg-base-850 p-2 text-sm text-slate-400">Belum ada ZIP tersimpan untuk aplikasi ini. ZIP disimpan otomatis setelah deploy atau update berikutnya berhasil.</p>
        )}

        {current.map((item) => (
          <div key={item.id} className="space-y-2 rounded-xl border border-emerald-400/30 bg-emerald-400/5 p-2">
            <p className="flex items-center gap-1.5 text-xs font-semibold text-emerald-300"><CheckCircle2 size={14} /> ZIP terakhir yang berhasil{item.scope === "self" ? " · Update Diri" : ""}</p>
            <div className="min-w-0">
              <p className="truncate font-mono text-sm text-slate-100">{item.filename}</p>
              <p className="text-xs text-slate-400">{formatSize(item.size_bytes)} · {formatDate(item.created_at)}</p>
              {item.sha256 && <p className="truncate font-mono text-[11px] text-slate-500">SHA-256 {item.sha256.slice(0, 16)}…</p>}
            </div>
            <button type="button" disabled={downloading !== null} onClick={() => void download(item)}
              className="inline-flex items-center gap-1.5 rounded-lg bg-accent-blue px-3 py-2 text-xs font-semibold text-white disabled:opacity-50">
              {downloading === item.id ? <Loader2 size={13} className="animate-spin" /> : <Download size={13} />} Unduh ZIP
            </button>
          </div>
        ))}

        {pending.map((item) => (
          <p key={item.id} className="flex items-center gap-1.5 rounded-xl border border-purple-400/30 bg-purple-400/5 p-2 text-xs text-purple-300">
            <Loader2 size={13} className="animate-spin" /> Update sedang diproses ({item.filename}). ZIP ini hanya disimpan jika berhasil.
          </p>
        ))}

        {error && <p role="alert" className="rounded-lg bg-red-500/10 p-2 text-xs text-red-300">{error}</p>}
        {items !== null && (
          <div className="flex items-center justify-between gap-2">
            <p className="text-[11px] text-slate-500">Hanya ZIP terbaru yang berhasil online yang disimpan.</p>
            <button type="button" onClick={() => void load(key)} disabled={loading} aria-label="Muat ulang" className="rounded-lg p-1.5 text-slate-400 hover:bg-base-800">
              <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            </button>
          </div>
        )}
      </div>
    </DialogShell>
  );
}

interface Changes {
  first: boolean;
  files: number;
  added_count: number;
  modified_count: number;
  removed_count: number;
  added: string[];
  modified: string[];
  removed: string[];
  unreadable?: boolean;
}

interface HistoryEntry {
  id: string;
  kind: "new_app" | "update_app" | "self_update";
  status: "Success" | "Failed" | "Interrupted";
  file_name: string;
  size_bytes: number;
  message: string;
  created_at: string;
  is_current: boolean;
  changes?: Changes;
}

const KIND_LABEL: Record<HistoryEntry["kind"], string> = { new_app: "Rilis pertama", update_app: "Update aplikasi", self_update: "Update diri" };

function ChangeSummary({ changes }: { changes?: Changes }) {
  const [open, setOpen] = useState(false);
  if (!changes || changes.unreadable) return <p className="text-xs text-slate-500">Rincian perubahan tidak tersedia.</p>;
  if (changes.first) return <p className="text-xs text-slate-400">{changes.files} file diunggah.</p>;
  const total = changes.added_count + changes.modified_count + changes.removed_count;
  if (total === 0) return <p className="text-xs text-slate-400">Isi sama dengan versi sebelumnya ({changes.files} file).</p>;
  const groups: [string, string[], number, string][] = [
    ["Ditambah", changes.added, changes.added_count, "text-emerald-300"],
    ["Diubah", changes.modified, changes.modified_count, "text-amber-300"],
    ["Dihapus", changes.removed, changes.removed_count, "text-red-300"],
  ];
  return (
    <div className="space-y-1">
      <button type="button" onClick={() => setOpen((value) => !value)} className="inline-flex flex-wrap items-center gap-2 text-xs">
        <span className="text-emerald-300">+{changes.added_count}</span>
        <span className="text-amber-300">~{changes.modified_count}</span>
        <span className="text-red-300">−{changes.removed_count}</span>
        <span className="inline-flex items-center gap-0.5 text-slate-400">file <ChevronDown size={13} className={open ? "rotate-180" : ""} /></span>
      </button>
      {open && (
        <div className="space-y-1 rounded-lg bg-base-950/60 p-2">
          {groups.filter(([, , count]) => count > 0).map(([label, list, count, color]) => (
            <div key={label}>
              <p className={`text-[11px] font-semibold ${color}`}>{label} ({count})</p>
              <ul className="font-mono text-[11px] text-slate-300">
                {list.map((file) => <li key={file} className="break-all">{file}</li>)}
                {count > list.length && <li className="text-slate-500">…dan {count - list.length} file lainnya</li>}
              </ul>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export function UpdateHistoryDialog({ repo, onClose }: { repo: string; onClose: () => void }) {
  const [data, setData] = useState<{ entries: HistoryEntry[]; success_count: number; update_count: number } | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await fetch(`/api/deploy-history?repo=${encodeURIComponent(repo)}`, { cache: "no-store", credentials: "same-origin" });
      const body = await response.json().catch(() => null);
      if (!response.ok) throw new Error(body?.error || `HTTP ${response.status}`);
      setData(body);
      setError("");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Riwayat gagal dimuat.");
    } finally { setLoading(false); }
  }, [repo]);

  useEffect(() => { void load(); }, [load]);

  const failures = data ? data.entries.filter((entry) => entry.status !== "Success").length : 0;

  return (
    <DialogShell title="Riwayat update" icon={<History size={18} />} subtitle={repo} onClose={onClose}>
      <div className="space-y-2">
        {loading && !data && <p className="flex items-center gap-2 text-sm text-slate-400"><Loader2 size={14} className="animate-spin" /> Memuat riwayat…</p>}
        {error && <p role="alert" className="rounded-lg bg-red-500/10 p-2 text-xs text-red-300">{error}</p>}
        {data && (
          <div className="grid grid-cols-3 gap-2 text-center">
            <div className="rounded-xl border border-base-border bg-base-850 p-2"><p className="text-lg font-bold text-white">{data.update_count}</p><p className="text-[11px] text-slate-400">Update berhasil</p></div>
            <div className="rounded-xl border border-base-border bg-base-850 p-2"><p className="text-lg font-bold text-white">{data.success_count}</p><p className="text-[11px] text-slate-400">Total rilis</p></div>
            <div className="rounded-xl border border-base-border bg-base-850 p-2"><p className="text-lg font-bold text-white">{failures}</p><p className="text-[11px] text-slate-400">Gagal</p></div>
          </div>
        )}
        {data && data.entries.length === 0 && (
          <p className="rounded-xl border border-base-border bg-base-850 p-2 text-sm text-slate-400">Belum ada riwayat. Riwayat mulai tercatat sejak versi ini untuk setiap deploy dan update berikutnya.</p>
        )}
        {data && data.entries.length > 0 && (
          <ol className="space-y-2">
            {data.entries.map((entry) => {
              const success = entry.status === "Success";
              return (
                <li key={entry.id} className={`space-y-1 rounded-xl border p-2 ${success ? "border-base-border bg-base-850" : "border-red-400/30 bg-red-500/5"}`}>
                  <div className="flex flex-wrap items-center gap-1.5">
                    {success ? <CheckCircle2 size={14} className="text-emerald-400" /> : <XCircle size={14} className="text-red-400" />}
                    <span className="text-xs font-semibold text-slate-100">{KIND_LABEL[entry.kind] ?? entry.kind}</span>
                    <span className={`text-xs ${success ? "text-emerald-300" : entry.status === "Interrupted" ? "text-amber-300" : "text-red-300"}`}>
                      {success ? "Berhasil" : entry.status === "Interrupted" ? "Terputus" : "Gagal"}
                    </span>
                    {entry.is_current && <span className="rounded-full bg-emerald-400/15 px-2 py-0.5 text-[10px] font-semibold text-emerald-300">ZIP aktif</span>}
                    <span className="ml-auto text-[11px] text-slate-500">{formatDate(entry.created_at)}</span>
                  </div>
                  {entry.file_name && <p className="truncate font-mono text-[11px] text-slate-400">{entry.file_name} · {formatSize(entry.size_bytes)}</p>}
                  <ChangeSummary changes={entry.changes} />
                  {entry.message && <p className={`break-words text-xs ${success ? "text-slate-400" : "text-red-300"}`}>{entry.message}</p>}
                  {!success && <p className="text-[11px] text-slate-500">ZIP versi ini tidak disimpan; ZIP terakhir yang berhasil tetap aktif.</p>}
                </li>
              );
            })}
          </ol>
        )}
      </div>
    </DialogShell>
  );
}
