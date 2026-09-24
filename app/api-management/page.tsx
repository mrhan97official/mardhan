"use client";

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { KeyRound, Plus, RefreshCw, ShieldCheck } from "lucide-react";
import Link from "next/link";
import AppShell from "@/components/AppShell";
import ApiPerformancePanel from "@/components/ApiPerformancePanel";
import { fallbackApiPerformance } from "@/lib/fallbackData";
import type { ApiPerformance } from "@/lib/types";

interface ManagedAPI { id: string; name: string; project: string; path: string; method: "GET" | "HEAD"; environment: string; enabled: number }
interface KeyInfo { id: string; name: string; key_prefix: string; scopes: string; created_at: string; revoked_at: string | null }
interface Check { api_id: string; status_code: number; latency_ms: number; checked_at: string }
interface Snapshot {
  apis: ManagedAPI[]; keys: KeyInfo[]; projects: { name: string; app_url: string }[];
  scopes: string[]; checks: Check[]; audit: { action: string; target: string; created_at: string }[];
  performance: ApiPerformance;
}
const field = "w-full rounded-xl border border-base-border bg-base-850 px-3 py-2 text-sm text-slate-100";

export default function ApiManagementPage() {
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null);
  const [range, setRange] = useState<"24h" | "7d" | "30d">("24h");
  const [name, setName] = useState("");
  const [project, setProject] = useState("");
  const [path, setPath] = useState("/health");
  const [method, setMethod] = useState<"GET" | "HEAD">("GET");
  const [environment, setEnvironment] = useState("Production");
  const [keyName, setKeyName] = useState("");
  const [scopes, setScopes] = useState<string[]>([]);
  const [newKey, setNewKey] = useState("");
  const [internalChecks, setInternalChecks] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      const response = await fetch(`/api/api-management?range=${range}`, { cache: "no-store" });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || `HTTP ${response.status}`);
      setSnapshot(body as Snapshot);
      setError("");
    } catch (cause) { setError(cause instanceof Error ? cause.message : "Tidak dapat membaca API."); }
  }, [range]);
  useEffect(() => { void load(); }, [load]);

  async function run(action: string, values: Record<string, unknown>): Promise<boolean> {
    setBusy(true);
    setError(""); setMessage("");
    try {
      const response = await fetch("/api/api-management", { method: "POST", cache: "no-store",
        headers: { "Content-Type": "application/json" }, body: JSON.stringify({ action, ...values }) });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || `HTTP ${response.status}`);
      if (action === "create_key" || action === "rotate_key") { setNewKey(body.key); setKeyName(""); setMessage(body.warning || "API key dibuat. Salin sekarang; nilainya hanya ditampilkan sekali."); }
      else if (action === "test_api") setMessage(`Uji selesai: ${body.status_code || "gagal terhubung"} · ${body.latency_ms} ms${body.error ? ` · ${body.error}` : ""}`);
      else setMessage("Perubahan tersimpan.");
      await load();
      return true;
    } catch (cause) { setError(cause instanceof Error ? cause.message : "Aksi gagal."); return false; }
    finally { setBusy(false); }
  }

  function submitAPI(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    void run("create_api", { name, project, path, method, environment }).then((ok) => { if (ok) setName(""); });
  }
  function submitKey(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!scopes.length) { setError("Pilih sedikitnya satu hak baca."); return; }
    void run("create_key", { name: keyName, scopes });
  }

  async function testInternal(scope: string) {
    const resource = scope.slice(5);
    setInternalChecks((old) => ({ ...old, [scope]: "Menguji…" }));
    const start = performance.now();
    try {
      const response = await fetch(`/api/${resource}`, { cache: "no-store" });
      setInternalChecks((old) => ({ ...old, [scope]: `HTTP ${response.status} · ${Math.round(performance.now() - start)} ms` }));
    } catch {
      setInternalChecks((old) => ({ ...old, [scope]: "Tidak terhubung" }));
    }
  }

  return (
    <AppShell title="API Management" subtitle="Endpoint, API key baca, dan uji respons nyata">
      {error && <p role="alert" className="rounded-xl bg-red-500/10 p-3 text-sm text-red-300">{error} {!snapshot && <Link className="underline" href="/databases">Periksa Databases</Link>}</p>}
      {message && <p role="status" className="rounded-xl bg-emerald-500/10 p-3 text-sm text-emerald-300">{message}</p>}
      {!snapshot && !error && <p className="text-sm text-slate-400">Memuat API…</p>}
      {snapshot && <>
        <ApiPerformancePanel perf={snapshot.performance || fallbackApiPerformance} range={range} onRangeChange={setRange} />
        <section className="card space-y-4 p-4 sm:p-6">
          <div className="flex items-center justify-between"><h2 className="text-base font-bold">API aplikasi yang didaftarkan</h2>
            <button type="button" onClick={() => void load()} className="rounded-lg border border-base-border p-2" title="Segarkan"><RefreshCw size={15} /></button></div>
          <p className="text-sm text-slate-400">Tombol aktif/jeda mengatur pemeriksaan DevControl. Aplikasi eksternal tetap berjalan di platformnya. Hanya path GET/HEAD pada proyek yang sudah memiliki URL deployment yang dapat diuji.</p>
          <form onSubmit={submitAPI} className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
            <label className="text-xs text-slate-400">Nama API<input required minLength={2} maxLength={80} value={name} onChange={(e) => setName(e.target.value)} className={`mt-1 ${field}`} placeholder="Status aplikasi" /></label>
            <label className="text-xs text-slate-400">Proyek<select required value={project} onChange={(e) => setProject(e.target.value)} className={`mt-1 ${field}`}><option value="">Pilih proyek</option>{snapshot.projects.map((item) => <option key={item.name} value={item.name}>{item.name}</option>)}</select></label>
            <label className="text-xs text-slate-400">Path<input required value={path} onChange={(e) => setPath(e.target.value)} className={`mt-1 ${field}`} placeholder="/health" /></label>
            <label className="text-xs text-slate-400">Metode<select value={method} onChange={(e) => setMethod(e.target.value as "GET" | "HEAD")} className={`mt-1 ${field}`}><option>GET</option><option>HEAD</option></select></label>
            <label className="text-xs text-slate-400">Lingkungan<input required value={environment} onChange={(e) => setEnvironment(e.target.value)} className={`mt-1 ${field}`} /></label>
            <button type="submit" disabled={busy || !snapshot.projects.length} className="flex items-center justify-center gap-2 rounded-lg bg-accent-blue px-4 py-2 text-sm font-semibold text-white disabled:opacity-50"><Plus size={16} /> Daftarkan API</button>
          </form>
          {!snapshot.projects.length && <p className="text-sm text-amber-300">Belum ada proyek dengan URL deployment di D1. Deploy aplikasi dahulu agar endpoint dapat didaftarkan.</p>}
          {!snapshot.apis.length && <p className="text-sm text-slate-400">Belum ada endpoint aplikasi yang didaftarkan.</p>}
          <div className="grid gap-3 lg:grid-cols-2">{snapshot.apis.map((api) => {
            const check = snapshot.checks.find((item) => item.api_id === api.id);
            return <div key={api.id} className="rounded-xl border border-base-border bg-base-850 p-4">
              <div className="flex flex-wrap items-center justify-between gap-2"><div><h3 className="font-semibold">{api.name}</h3><p className="mt-1 break-all text-xs text-slate-400">{api.project} · {api.environment} · {api.method} {api.path}</p></div>
                <span className={api.enabled ? "text-xs text-emerald-400" : "text-xs text-amber-400"}>{api.enabled ? "Pemantauan aktif" : "Dijeda"}</span></div>
              <p className="mt-3 text-xs text-slate-400">{check ? `Uji terakhir: ${check.status_code || "gagal"} · ${check.latency_ms} ms · ${check.checked_at}` : "Belum diuji"}</p>
              <div className="mt-3 flex gap-2"><button type="button" disabled={busy || !api.enabled} onClick={() => void run("test_api", { id: api.id })} className="rounded-lg border border-base-border px-3 py-2 text-xs disabled:opacity-50">Uji sekarang</button>
                <button type="button" disabled={busy} onClick={() => void run("toggle_api", { id: api.id, enabled: !api.enabled })} className="rounded-lg border border-base-border px-3 py-2 text-xs disabled:opacity-50">{api.enabled ? "Jeda pemeriksaan" : "Aktifkan pemeriksaan"}</button></div>
            </div>;
          })}</div>
        </section>
        <section className="card space-y-4 p-4 sm:p-6">
          <div><h2 className="flex items-center gap-2 text-base font-bold"><ShieldCheck size={18} /> Endpoint DevControl</h2>
            <p className="mt-1 text-xs text-slate-400">Endpoint baca yang nyata. API key hanya berlaku untuk hak baca yang dipilih; deployment dan pengaturan tetap memerlukan sesi admin.</p></div>
          <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">{snapshot.scopes.map((scope) => <div key={scope} className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-base-border bg-base-850 px-3 py-2 text-xs">
            <span className="font-mono">GET /api/{scope.slice(5)}</span>
            <button type="button" onClick={() => void testInternal(scope)} className="text-accent-blue hover:underline">Uji</button>
            {internalChecks[scope] && <span className="w-full text-slate-400">{internalChecks[scope]}</span>}
          </div>)}</div>
        </section>
        <section className="card space-y-4 p-4 sm:p-6">
          <h2 className="flex items-center gap-2 text-base font-bold"><KeyRound size={18} /> API key klien</h2>
          <p className="text-sm text-slate-400">Kunci baru dapat dipakai pada header Authorization: Bearer untuk endpoint GET yang diizinkan. Rahasia disimpan sebagai hash dan dapat dicabut kapan saja.</p>
          <form onSubmit={submitKey} className="space-y-3"><input required minLength={2} maxLength={80} value={keyName} onChange={(e) => setKeyName(e.target.value)} className={field} placeholder="Nama klien, mis. dashboard tim" />
            <div className="flex flex-wrap gap-2">{snapshot.scopes.map((scope) => <label key={scope} className="flex items-center gap-2 rounded-lg border border-base-border px-3 py-2 text-xs">
              <input type="checkbox" checked={scopes.includes(scope)} onChange={() => setScopes((old) => old.includes(scope) ? old.filter((item) => item !== scope) : [...old, scope])} />{scope}</label>)}</div>
            <button type="submit" disabled={busy} className="rounded-lg bg-accent-blue px-4 py-2 text-sm font-semibold text-white disabled:opacity-50">Buat API key</button></form>
          {newKey && <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 p-3"><p className="text-xs text-amber-300">Salin sekali lalu simpan di tempat aman:</p>
            <code className="mt-2 block break-all text-sm text-white">{newKey}</code><button type="button" onClick={() => setNewKey("")} className="mt-2 text-xs underline">Tutup</button></div>}
          <div className="space-y-2">{snapshot.keys.map((key) => <div key={key.id} className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-base-border p-3 text-xs">
            <div><p className="font-semibold text-slate-200">{key.name} · {key.key_prefix}…</p><p className="mt-1 text-slate-400">{key.scopes} · {key.created_at} {key.revoked_at && "· Dicabut"}</p></div>
            {!key.revoked_at && <div className="flex gap-2"><button type="button" disabled={busy} onClick={() => { if (window.confirm(`Rotasi API key ${key.name}? Kunci pengganti hanya tampil sekali.`)) void run("rotate_key", { id: key.id }); }} className="rounded-lg border border-base-border px-3 py-2">Rotasi</button>
              <button type="button" disabled={busy} onClick={() => { if (window.confirm(`Cabut API key ${key.name}?`)) void run("revoke_key", { id: key.id }); }} className="rounded-lg border border-red-500/30 px-3 py-2 text-red-300">Cabut</button></div>}
          </div>)}{!snapshot.keys.length && <p className="text-sm text-slate-400">Belum ada API key.</p>}</div>
        </section>
        <section className="card p-4 sm:p-6"><h2 className="text-base font-bold">Riwayat tindakan admin</h2>
          <div className="mt-3 space-y-2 text-xs text-slate-400">{snapshot.audit.map((item, index) => <p key={`${item.created_at}-${index}`}>{item.created_at} · {item.action} · {item.target}</p>)}
            {!snapshot.audit.length && <p>Belum ada tindakan tersimpan.</p>}</div></section>
      </>}
    </AppShell>
  );
}
