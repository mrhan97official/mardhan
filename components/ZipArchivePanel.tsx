"use client";

import { useEffect, useState } from "react";
import { Download, RefreshCw } from "lucide-react";

interface ZipRecord {
  id: string;
  scope: "app" | "self";
  target: string;
  filename: string;
  size_bytes: number;
  status: "current" | "previous" | "pending" | "failed";
  source: "upload" | "github_snapshot";
  created_at: string;
}

const sessionKey = "devcontrol-zip-archive-key";

export default function ZipArchivePanel() {
  const [key, setKey] = useState("");
  const [items, setItems] = useState<ZipRecord[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(false);
  const [downloadID, setDownloadID] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [unlocked, setUnlocked] = useState(false);

  async function load(secret: string, offset = 0) {
    if (!secret.trim()) { setError("Masukkan kunci arsip terlebih dahulu."); return; }
    setLoading(true);
    setError(null);
    try {
      const response = await fetch(`/api/zip-archives?offset=${offset}`, {
        headers: { Authorization: `Bearer ${secret.trim()}` }, cache: "no-store",
      });
      const body = await response.json().catch(() => null);
      if (!response.ok) throw new Error(body?.error || `Gagal memuat arsip (HTTP ${response.status})`);
      if (!Array.isArray(body?.items)) throw new Error("Daftar arsip tidak valid.");
      setItems((old) => offset ? [...old, ...body.items] : body.items);
      setHasMore(body.has_more === true);
      setUnlocked(true);
      sessionStorage.setItem(sessionKey, secret.trim());
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Gagal memuat arsip.");
      if (offset === 0) { setUnlocked(false); setItems([]); }
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    const saved = sessionStorage.getItem(sessionKey);
    if (saved) { setKey(saved); void load(saved); }
  }, []);

  async function download(item: ZipRecord) {
    setDownloadID(item.id);
    setError(null);
    try {
      const response = await fetch(`/api/zip-archives?id=${item.id}`, {
        headers: { Authorization: `Bearer ${key.trim()}` }, cache: "no-store",
      });
      if (!response.ok) {
        const body = await response.json().catch(() => null);
        throw new Error(body?.error || `Gagal mengunduh ZIP (HTTP ${response.status})`);
      }
      const blob = await response.blob();
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = item.filename;
      document.body.append(link);
      link.click();
      link.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 30000);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Gagal mengunduh ZIP.");
    } finally {
      setDownloadID(null);
    }
  }

  function label(item: ZipRecord) {
    switch (item.status) {
      case "current": return item.scope === "self" ? "Terakhir dikirim ke GitHub" : "Versi online terakhir";
      case "previous": return "Versi sebelumnya";
      case "failed": return "Gagal · akan dihapus otomatis";
      default: return "Sedang diproses · disimpan hanya jika berhasil";
    }
  }

  return (
    <section id="zip-archives" className="card space-y-2 p-2">
      <div>
        <h2 className="text-base font-semibold text-white">Arsip ZIP aplikasi</h2>
        <p className="mt-1 text-xs text-slate-400">Hanya ZIP terbaru yang berhasil online yang disimpan per aplikasi. ZIP yang gagal dibuang, dan versi sukses sebelumnya tetap aktif.</p>
      </div>
      <form onSubmit={(event) => { event.preventDefault(); void load(key); }} className="flex flex-col gap-2 sm:flex-row">
        <input type="password" autoComplete="off" aria-label="Kunci arsip" placeholder="Kunci arsip" value={key}
          onChange={(event) => setKey(event.target.value)}
          className="min-w-0 flex-1 rounded-xl border border-base-border bg-base-850 px-3 py-2 text-sm text-slate-200" />
        <button type="submit" disabled={loading} className="flex items-center justify-center gap-2 rounded-xl border border-base-border px-3 py-2 text-sm text-slate-200 hover:bg-base-800 disabled:opacity-60">
          <RefreshCw size={15} className={loading ? "animate-spin" : ""} /> {unlocked ? "Segarkan arsip" : "Buka arsip"}
        </button>
      </form>
      {error && <p role="alert" className="text-xs text-red-400">{error}</p>}
      {unlocked && (items.length ? (
        <>
          <div className="space-y-2">
            {items.map((item) => (
              <div key={item.id} className="flex flex-col gap-2 rounded-xl border border-base-border bg-base-850/60 p-2 sm:flex-row sm:items-center sm:justify-between">
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-slate-200">{item.filename}</p>
                  <p className="mt-1 text-xs text-slate-400">{item.scope === "self" ? "Update Diri" : "Aplikasi"} · {item.target} · {label(item)}</p>
                  <p className="mt-1 text-[11px] text-slate-500">
                    {item.source === "github_snapshot" ? "Snapshot repo sebelum update · " : ""}
                    {(item.size_bytes / 1024 / 1024).toFixed(2)} MiB · {item.created_at}
                  </p>
                </div>
                <button type="button" disabled={downloadID === item.id} onClick={() => void download(item)}
                  className="flex shrink-0 items-center justify-center gap-1.5 rounded-lg border border-base-border px-3 py-2 text-xs font-medium text-accent-blue hover:bg-base-800 disabled:opacity-60">
                  <Download size={14} /> {downloadID === item.id ? "Mengunduh..." : "Unduh ZIP"}
                </button>
              </div>
            ))}
          </div>
          {hasMore && <button type="button" disabled={loading} onClick={() => void load(key, items.length)} className="text-xs font-medium text-accent-blue hover:underline disabled:opacity-60">Muat arsip berikutnya</button>}
        </>
      ) : <p className="text-xs text-slate-400">Belum ada ZIP tersimpan.</p>)}
    </section>
  );
}
