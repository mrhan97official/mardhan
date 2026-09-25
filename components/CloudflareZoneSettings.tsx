"use client";

import { useEffect, useState } from "react";
import { Check, CloudCog, RefreshCw } from "lucide-react";
import { notifyDataChanged } from "@/lib/liveUpdates";

type Zone = { id: string; name: string; matches_domain: boolean };
type Approval = { zone_id: string; zone_name: string; project_id: string; vercel_synced: boolean; approved_at: string };
type State = {
  zones: Zone[];
  approved: Approval | null;
  active_zone_id: string;
  project: { id: string; name: string; domain: string };
  project_error: string;
  zone_error: string;
  mode: "api" | "cloudflare" | "";
};

export default function CloudflareZoneSettings() {
  const [state, setState] = useState<State | null>(null);
  const [selected, setSelected] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  async function refresh(signal?: AbortSignal) {
    setLoading(true);
    setError("");
    try {
      const response = await fetch("/api/zone-approval", { credentials: "same-origin", cache: "no-store", signal });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || "Gagal mengambil daftar zona Cloudflare.");
      if (!signal?.aborted) setState(body as State);
    } catch (reason) {
      if (!signal?.aborted) setError(reason instanceof Error ? reason.message : "Gagal mengambil daftar zona Cloudflare.");
    } finally { if (!signal?.aborted) setLoading(false); }
  }

  useEffect(() => {
    const controller = new AbortController();
    void refresh(controller.signal);
    return () => controller.abort();
  }, []);

  async function approve() {
    if (!state || !selected || saving) return;
    setSaving(true);
    setMessage("");
    setError("");
    try {
      const response = await fetch("/api/zone-approval", {
        method: "POST", credentials: "same-origin", cache: "no-store",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ zone_id: selected }),
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || "Gagal menyetujui zona.");
      setState({ ...state, mode: "cloudflare", approved: body.approved as Approval, active_zone_id: selected });
      setMessage(body.message || "Zona disetujui.");
      notifyDataChanged();
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Gagal menyetujui zona."); }
    finally { setSaving(false); }
  }

  async function activateAutomatically() {
    if (!state || saving) return;
    setSaving(true);
    setError("");
    setMessage("");
    try {
      const response = await fetch("/api/zone-approval", {
        method: "POST", credentials: "same-origin", cache: "no-store",
        headers: { "Content-Type": "application/json" }, body: JSON.stringify({ action: "auto" }),
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || "Gagal mengaktifkan monitoring otomatis.");
      setState({ ...state, mode: body.mode, approved: body.approved ?? state.approved, active_zone_id: body.approved?.zone_id ?? state.active_zone_id });
      setMessage(body.message || "Monitoring aktif.");
      notifyDataChanged();
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Gagal mengaktifkan monitoring otomatis."); }
    finally { setSaving(false); }
  }

  const choice = state?.zones.find((zone) => zone.id === selected);
  return (
    <section className="card max-w-3xl space-y-4 p-5 sm:p-6" aria-labelledby="zone-approval-title">
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-start gap-3">
          <CloudCog className="mt-1 shrink-0 text-accent-blue" size={21} />
          <div>
            <h3 id="zone-approval-title" className="text-lg font-semibold text-white">Monitoring otomatis</h3>
            <p className="mt-1 text-sm text-slate-400">Satu klik untuk mengaktifkan metrik nyata di Overview. Backend memakai zona Cloudflare yang cocok jika tersedia; untuk alamat vercel.app tanpa zona, backend mencatat trafik API DevControl.</p>
          </div>
        </div>
        <button type="button" aria-label="Segarkan daftar zona" title="Segarkan" disabled={loading || saving} onClick={() => void refresh()} className="rounded-lg border border-base-border p-2 text-slate-300 hover:bg-base-800 disabled:opacity-50"><RefreshCw size={16} /></button>
      </div>

      {state?.mode === "api" && <p className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 p-3 text-sm text-emerald-200"><Check size={15} className="mr-1 inline-block" /> Aktif: monitoring API DevControl. Network menunjukkan byte respons API dan Requests menunjukkan jumlah panggilan API, bukan seluruh trafik CDN Vercel.</p>}
      {state?.approved && state.mode !== "api" && (
        <p className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 p-3 text-sm text-emerald-200">
          <Check size={15} className="mr-1 inline-block" /> Aktif: <strong>{state.approved.zone_name}</strong> · Vercel {state.approved.vercel_synced ? "tersimpan" : "belum tersinkron; setujui kembali untuk mencoba"}
        </p>
      )}
      {!state?.approved && state?.mode !== "api" && state?.active_zone_id && <p className="rounded-lg border border-base-border bg-base-800/50 p-3 text-sm text-slate-300">Zona dari environment Vercel saat ini: <code>{state.active_zone_id}</code></p>}

      <button type="button" disabled={!state || loading || saving} onClick={() => void activateAutomatically()} className="rounded-xl bg-accent-blue px-4 py-2.5 text-sm font-semibold text-white hover:bg-blue-500 disabled:cursor-not-allowed disabled:opacity-50">{saving ? "Mengecek sumber dan mengaktifkan…" : state?.mode ? "Periksa ulang sumber otomatis" : "Aktifkan monitoring satu klik"}</button>
      {state && !loading && state.zones.length === 0 && <p className="text-xs text-slate-400">{state.project.domain || "Domain Vercel"} belum memiliki zona Cloudflare yang tersedia. Tombol di atas tetap bisa mengaktifkan pemantauan Go API tanpa domain tambahan.</p>}
      {error && <p role="alert" className="rounded-lg bg-red-500/10 p-3 text-sm text-red-300">{error}</p>}
      {message && <p role="status" className="rounded-lg bg-emerald-500/10 p-3 text-sm text-emerald-200">{message}</p>}

      <details className="rounded-xl border border-base-border p-3 sm:p-4">
        <summary className="cursor-pointer text-sm font-medium text-slate-200">Pilih zona Cloudflare secara manual (opsional)</summary>
        <div className="mt-4 space-y-3">
      {state?.project_error && <p role="status" className="rounded-lg border border-amber-500/30 bg-amber-500/10 p-3 text-sm text-amber-200">Vercel: {state.project_error}</p>}
      {state?.zone_error && <p role="status" className="rounded-lg border border-amber-500/30 bg-amber-500/10 p-3 text-sm text-amber-200">Cloudflare: {state.zone_error}</p>}
      {state?.project.id && <p className="text-xs text-slate-400">Project tujuan: <strong className="text-slate-200">{state.project.name}</strong>{state.project.domain && <> · domain produksi {state.project.domain}</>}</p>}
      {loading && <p className="text-sm text-slate-400">Mencari zona dari akun Cloudflare…</p>}
      {state && !loading && state.zones.length === 0 && <p className="text-sm text-slate-400">Belum ada zona untuk dipilih. Domain vercel.app tidak bisa diaktifkan sebagai zona Cloudflare milik Anda.</p>}
      {state && state.zones.length > 0 && (
        <fieldset className="space-y-2" disabled={loading || saving}>
          <legend className="mb-2 text-sm font-medium text-slate-200">Pilih zona yang ingin dipantau</legend>
          {state.zones.map((zone) => (
            <label key={zone.id} className={`flex cursor-pointer items-center justify-between gap-3 rounded-xl border p-3 text-sm transition-colors ${selected === zone.id ? "border-accent-blue bg-accent-blue/10" : "border-base-border hover:bg-base-800/60"}`}>
              <span className="flex min-w-0 items-center gap-3">
                <input type="radio" name="cloudflare-zone" value={zone.id} checked={selected === zone.id} onChange={() => { setSelected(zone.id); setMessage(""); }} className="accent-blue-500" />
                <span className="min-w-0"><strong className="block break-all text-slate-100">{zone.name}</strong><span className="break-all text-xs text-slate-500">{zone.id}</span></span>
              </span>
              {zone.matches_domain && <span className="shrink-0 rounded-lg bg-emerald-500/10 px-2 py-1 text-xs text-emerald-300">Cocok dengan domain</span>}
            </label>
          ))}
        </fieldset>
      )}
      {choice && !choice.matches_domain && <p className="text-xs text-amber-200">Zona ini tidak cocok dengan domain project yang diketahui. Metriknya mencakup seluruh hostname pada zona pilihan, bukan trafik domain vercel.app.</p>}
      {state && state.zones.length > 0 && <button type="button" disabled={!selected || saving || loading || !!state.project_error} onClick={() => void approve()} className="rounded-xl bg-accent-blue px-4 py-2.5 text-sm font-semibold text-white hover:bg-blue-500 disabled:cursor-not-allowed disabled:opacity-50">{saving ? "Memeriksa analitik dan menyimpan…" : "Setujui zona dan simpan ke Vercel"}</button>}
      <p className="text-xs text-slate-500">Token Cloudflare membutuhkan Zone:Zone:Read untuk pencarian dan Account Analytics Read untuk metrik. Aplikasi menguji akses analitik sebelum mengaktifkan zona. Perubahan environment Vercel berlaku untuk deployment berikutnya; persetujuan di D1 membuat metrik langsung aktif saat ini.</p>
        </div>
      </details>
    </section>
  );
}
