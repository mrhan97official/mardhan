"use client";

import { useCallback, useEffect, useState } from "react";
import { Database, RefreshCw, ShieldCheck } from "lucide-react";
import AppShell from "@/components/AppShell";

interface TableInfo {
  name: string;
  columns: string[];
  missing: string[];
  mismatched: string[];
  extra: string[];
  exists: boolean;
}
interface StorageStatus {
  d1: { connected: boolean; ready: boolean; tables: TableInfo[]; missing_indexes: string[]; migrations: string[] };
  r2_configured: boolean;
  r2_exists: boolean;
  r2_error?: string;
  r2_created?: boolean;
}

export default function DatabasesPage() {
  const [status, setStatus] = useState<StorageStatus | null>(null);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      const response = await fetch("/api/databases", { cache: "no-store" });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || `HTTP ${response.status}`);
      setStatus(body as StorageStatus);
      setError("");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Pemeriksaan D1 gagal.");
    }
  }, []);
  useEffect(() => { void load(); }, [load]);

  async function prepare() {
    if (!window.confirm("Siapkan tabel dan kolom yang kurang serta periksa/buat satu bucket R2? Data lama tidak dihapus.")) return;
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const response = await fetch("/api/databases", { method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ action: "prepare" }), cache: "no-store" });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || `HTTP ${response.status}`);
      setMessage(body.r2_created ? "D1 siap. Bucket R2 dibuat dan berhasil diuji baca/tulis." : "D1 siap. Bucket R2 berhasil diuji baca/tulis.");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Penyiapan gagal.");
    } finally { setBusy(false); await load(); }
  }

  const missing = status?.d1.tables.filter((table) => !table.exists || table.missing.length || table.mismatched.length) || [];
  return (
    <AppShell title="Databases" subtitle="Pemeriksaan skema D1 dan penyimpanan R2">
      <section className="card space-y-4 p-4 sm:p-6">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div><h2 className="flex items-center gap-2 text-lg font-bold"><Database size={19} /> Kesiapan penyimpanan</h2>
            <p className="mt-1 text-sm text-slate-400">Kredensial tetap di server. Tindakan penyiapan hanya menambah struktur yang tercatat di kode.</p></div>
          <div className="flex gap-2">
            <button type="button" onClick={() => void load()} className="rounded-lg border border-base-border px-3 py-2 text-sm hover:bg-base-800"><RefreshCw size={16} /></button>
            <button type="button" disabled={busy || !status} onClick={() => void prepare()}
              className="rounded-lg bg-accent-blue px-3 py-2 text-sm font-semibold text-white disabled:opacity-50">{busy ? "Menyiapkan…" : "Siapkan D1 + R2"}</button>
          </div>
        </div>
        {error && <p role="alert" className="rounded-lg bg-red-500/10 p-3 text-sm text-red-300">{error}</p>}
        {message && <p role="status" className="rounded-lg bg-emerald-500/10 p-3 text-sm text-emerald-300">{message}</p>}
        {!status && !error && <p className="text-sm text-slate-400">Membaca database…</p>}
        {status && <div className="grid gap-3 sm:grid-cols-2">
          <div className="rounded-xl border border-base-border bg-base-850 p-4">
            <p className="text-xs text-slate-400">Cloudflare D1</p><p className="mt-1 font-semibold">{status.d1.ready ? "Skema siap" : `${missing.length} tabel perlu diperiksa`}</p>
            <p className="mt-1 text-xs text-slate-400">{status.d1.migrations.length} migrasi tercatat · {status.d1.missing_indexes.length} indeks kurang</p>
          </div>
          <div className="rounded-xl border border-base-border bg-base-850 p-4">
            <p className="text-xs text-slate-400">Arsip Cloudflare R2</p>
            <p className="mt-1 font-semibold">{status.r2_exists ? "Bucket tersedia" : "Bucket belum siap"}</p>
            <p className="mt-1 text-xs text-slate-400">{status.r2_error || "Uji baca/tulis dilakukan saat penyiapan atau awal deployment."}</p>
          </div>
        </div>}
      </section>
      {status && <section className="card p-4 sm:p-6">
        <h2 className="flex items-center gap-2 text-base font-semibold"><ShieldCheck size={18} /> Tabel aplikasi</h2>
        <p className="mt-1 text-xs text-slate-400">Kolom tambahan pada database lama ditampilkan, tidak dihapus. Perubahan akan ditinjau sebelum tombol penyiapan dijalankan.</p>
        <div className="mt-4 grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {status.d1.tables.map((table) => <div key={table.name} className="min-w-0 rounded-xl border border-base-border bg-base-850 p-4">
            <div className="flex items-center justify-between gap-2"><span className="truncate font-mono text-xs text-slate-200">{table.name}</span>
              <span className={`text-xs ${table.exists && !table.missing.length && !table.mismatched.length ? "text-emerald-400" : "text-amber-400"}`}>
                {table.exists ? table.missing.length || table.mismatched.length ? "Perlu diperiksa" : "Siap" : "Belum ada"}</span></div>
            <p className="mt-2 text-xs text-slate-400">{table.columns.length} kolom ditemukan</p>
            {table.missing.length > 0 && <p className="mt-2 break-words text-xs text-amber-300">Perlu: {table.exists ? table.missing.join(", ") : "buat tabel"}</p>}
            {table.mismatched.length > 0 && <p className="mt-1 break-words text-xs text-amber-300">Tipe berbeda: {table.mismatched.join(", ")}. Perbaiki manual setelah meninjau data.</p>}
            {table.extra.length > 0 && <p className="mt-1 break-words text-xs text-slate-400">Kolom tambahan: {table.extra.join(", ")}</p>}
          </div>)}
        </div>
        {status.d1.missing_indexes.length > 0 && <p className="mt-4 text-xs text-amber-300">Indeks yang perlu dibuat: {status.d1.missing_indexes.join(", ")}</p>}
      </section>}
    </AppShell>
  );
}
