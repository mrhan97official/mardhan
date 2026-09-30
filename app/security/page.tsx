"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { AlertOctagon, Bell, CheckCircle2, CircleDot, ExternalLink, Loader2, LogOut, Radar, ShieldCheck, ShieldOff, Smartphone } from "lucide-react";
import AppShell from "@/components/AppShell";
import { answerAlert, LEVEL_STYLE, type SecurityAlert } from "@/components/SecurityAlertCenter";
import { useSession } from "@/lib/session";

type SessionRow = { id: string; name: string; role: string; ip: string; device: string; created_at: string; expires_at: string; current: boolean };
type EventRow = { action: string; target: string; created_at: string };
type ChecklistItem = { env: string; label: string; where: string; link: string; changed: boolean; missing: boolean };
type Overview = {
  alerts: SecurityAlert[];
  events: EventRow[];
  sessions: SessionRow[];
  owner: boolean;
  lockdown: { active: boolean; since: string };
  patrol: { last_run: string };
  watchdog: { configured: boolean; last_seen: string };
  telegram: boolean;
  checklist?: ChecklistItem[];
};

const ACTION_LABEL: Record<string, string> = {
  login_owner: "Owner masuk", login_member: "Member masuk", login_failed: "Login gagal", session_confirm: "Konfirmasi identitas",
  session_revoke: "Perangkat dikeluarkan", sessions_revoke_all: "Semua sesi dicabut", lockdown_start: "Mode Darurat aktif", lockdown_end: "Mode Darurat dimatikan",
  totp_enabled: "2FA diaktifkan", totp_disabled: "2FA dimatikan", member_create: "Member dibuat", member_delete: "Member dihapus",
  member_rotate: "Token member dirotasi", member_revoke: "Member dicabut", member_restore: "Member dipulihkan", member_role: "Role member diubah",
  member_ip: "IP allowlist diubah", member_unlock_ip: "Kunci IP dibuka", create_key: "API key dibuat", revoke_key: "API key dicabut", rotate_key: "API key dirotasi",
  vercel_env_set: "Env Vercel ditambah", vercel_env_update: "Env Vercel diubah", vercel_env_delete: "Env Vercel dihapus", vercel_env_redeploy: "Redeploy env",
  delete_vercel_project: "Project Vercel dihapus", prepare_storage: "Penyimpanan disiapkan",
};

// Stored times are UTC "YYYY-MM-DD HH:MM:SS".
function when(value: string) {
  if (!value) return "belum pernah";
  const parsed = new Date(`${value.replace(" ", "T")}${value.includes("Z") || value.includes("+") ? "" : "Z"}`);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleString("id-ID", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
}

function fresh(value: string, minutes: number) {
  if (!value) return false;
  const parsed = new Date(`${value.replace(" ", "T")}Z`).getTime();
  return !Number.isNaN(parsed) && Date.now() - parsed < minutes * 60 * 1000;
}

async function post(body: Record<string, string>) {
  const response = await fetch("/api/security", {
    method: "POST", headers: { "Content-Type": "application/json" }, credentials: "same-origin", cache: "no-store", body: JSON.stringify(body),
  });
  const data = await response.json().catch(() => null);
  if (!response.ok) throw new Error(data?.error || `HTTP ${response.status}`);
  return data;
}

function Status({ ok, label, detail }: { ok: boolean; label: string; detail: string }) {
  return (
    <div className="flex items-start gap-2 rounded-xl bg-base-900 p-2">
      <CircleDot size={15} className={`mt-0.5 shrink-0 ${ok ? "text-emerald-400" : "text-amber-400"}`} />
      <div className="min-w-0"><p className="text-sm font-semibold text-white">{label}</p><p className="text-[11px] text-slate-400">{detail}</p></div>
    </div>
  );
}

export default function SecurityPage() {
  const { role } = useSession();
  const [data, setData] = useState<Overview | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [confirmLockdown, setConfirmLockdown] = useState(false);

  const load = useCallback(async () => {
    try {
      const response = await fetch("/api/security", { credentials: "same-origin", cache: "no-store" });
      const body = await response.json().catch(() => null);
      if (!response.ok) throw new Error(body?.error || `HTTP ${response.status}`);
      setData(body as Overview);
      setError("");
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Pusat Keamanan tidak dapat dimuat."); }
  }, []);

  useEffect(() => {
    void load();
    const interval = window.setInterval(() => { if (!document.hidden) void load(); }, 30 * 1000);
    const refresh = () => { void load(); };
    window.addEventListener("devcontrol:security-changed", refresh);
    return () => { window.clearInterval(interval); window.removeEventListener("devcontrol:security-changed", refresh); };
  }, [load]);

  async function run(key: string, action: () => Promise<void>) {
    setBusy(key);
    setError("");
    setNotice("");
    try { await action(); await load(); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Permintaan gagal."); }
    finally { setBusy(null); }
  }

  const open = useMemo(() => (data?.alerts ?? []).filter((item) => !item.acknowledged_at), [data]);
  const answered = useMemo(() => (data?.alerts ?? []).filter((item) => item.acknowledged_at).slice(0, 30), [data]);
  const events = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return (data?.events ?? []).filter((item) => !needle || `${item.action} ${ACTION_LABEL[item.action] ?? ""} ${item.target}`.toLowerCase().includes(needle));
  }, [data, query]);
  const owner = role === "owner";
  const checklistDone = (data?.checklist ?? []).filter((item) => item.changed).length;

  return (
    <AppShell title="Pusat Keamanan" subtitle="Penjaga gerbang, patroli, CCTV, dan alarm DevControl">
      {error && <p role="alert" className="rounded-xl border border-red-500/30 bg-red-500/10 p-2 text-sm text-red-300">{error}</p>}
      {notice && <p role="status" className="rounded-xl border border-emerald-400/30 bg-emerald-400/10 p-2 text-sm text-emerald-300">{notice}</p>}
      {!data && !error && <div className="card h-32 animate-pulse bg-base-800/40" />}

      {data && (
        <>
          <section className="grid grid-cols-1 gap-2 sm:grid-cols-2 xl:grid-cols-4">
            <Status ok={!data.lockdown.active && open.length === 0} label={data.lockdown.active ? "Mode Darurat aktif" : open.length ? `${open.length} peringatan menunggu` : "Aman"} detail={data.lockdown.active ? `Sejak ${when(data.lockdown.since)}` : "Status peringatan saat ini"} />
            <Status ok={fresh(data.patrol.last_run, 60)} label="Patroli 15 menit" detail={`Terakhir: ${when(data.patrol.last_run)}`} />
            <Status ok={data.watchdog.configured && fresh(data.watchdog.last_seen, 60)} label="Penjaga independen" detail={data.watchdog.configured ? `Lapor terakhir: ${when(data.watchdog.last_seen)}` : "Belum dipasang (lihat README)"} />
            <div className="flex items-start gap-2 rounded-xl bg-base-900 p-2">
              <Bell size={15} className={`mt-0.5 shrink-0 ${data.telegram ? "text-emerald-400" : "text-amber-400"}`} />
              <div className="min-w-0 flex-1">
                <p className="text-sm font-semibold text-white">Alarm di luar DevControl</p>
                <p className="text-[11px] text-slate-400">{data.telegram ? "Telegram aktif" : "Telegram belum diatur"}</p>
                {owner && <button type="button" disabled={busy !== null} onClick={() => void run("test", async () => { const result = await post({ action: "test_alert" }); setNotice(result.telegram ? "Uji alarm terkirim ke perangkat dan Telegram." : `Uji push terkirim. Telegram: ${result.error ?? "gagal"}`); })}
                  className="mt-1 text-[11px] font-medium text-accent-blue hover:underline">{busy === "test" ? "Mengirim…" : "Uji alarm"}</button>}
              </div>
            </div>
          </section>

          <section className={`card space-y-2 p-2 ${data.lockdown.active ? "border-red-500/60" : ""}`}>
            <div className="flex flex-wrap items-start gap-2">
              <div className={`rounded-xl p-2 ${data.lockdown.active ? "bg-red-600/20 text-red-400" : "bg-base-800 text-slate-300"}`}>{data.lockdown.active ? <AlertOctagon size={19} /> : <ShieldOff size={19} />}</div>
              <div className="min-w-0 flex-1">
                <h2 className="font-semibold text-white">Mode Darurat</h2>
                <p className="text-xs text-slate-400">Mengeluarkan semua perangkat lain dan semua member, membekukan API key, dan mengunci semua perubahan (deploy, Update Diri, env, member) sampai dimatikan. Setelah itu kunci dari luar lewat checklist di bawah.</p>
              </div>
              {owner && !data.lockdown.active && !confirmLockdown && (
                <button type="button" onClick={() => setConfirmLockdown(true)} className="rounded-xl bg-red-600 px-3 py-2 text-sm font-bold text-white hover:bg-red-500">Aktifkan Mode Darurat</button>
              )}
              {owner && data.lockdown.active && (
                <button type="button" disabled={busy !== null} onClick={() => void run("unlock", async () => { await post({ action: "unlock" }); setNotice("Mode Darurat dimatikan."); })}
                  className="rounded-xl border border-base-border px-3 py-2 text-sm font-semibold text-slate-100 hover:bg-base-800 disabled:opacity-50">{busy === "unlock" ? "Mematikan…" : "Matikan Mode Darurat"}</button>
              )}
            </div>
            {confirmLockdown && !data.lockdown.active && (
              <div className="rounded-xl border border-red-500/40 bg-red-600/10 p-2 text-sm text-slate-200">
                <p>Semua perangkat lain dan semua member akan keluar sekarang. Lanjutkan?</p>
                <div className="mt-2 flex gap-2">
                  <button type="button" disabled={busy !== null} onClick={() => void run("lockdown", async () => { await post({ action: "lockdown" }); setConfirmLockdown(false); setNotice("Mode Darurat aktif. Kerjakan checklist kunci dari luar."); })}
                    className="inline-flex items-center gap-1.5 rounded-lg bg-red-600 px-3 py-2 text-xs font-bold text-white disabled:opacity-50">{busy === "lockdown" && <Loader2 size={13} className="animate-spin" />} Ya, aktifkan</button>
                  <button type="button" onClick={() => setConfirmLockdown(false)} className="rounded-lg border border-base-border px-3 py-2 text-xs text-slate-300">Batal</button>
                </div>
              </div>
            )}
            {data.checklist && (
              <div className="space-y-1.5">
                <p className="text-xs font-semibold text-slate-300">Checklist kunci dari luar ({checklistDone}/{data.checklist.length} sudah berganti). Ganti di penyedianya, redeploy DevControl, lalu halaman ini memeriksa sendiri apakah nilainya sudah berubah.</p>
                {data.checklist.map((item) => (
                  <div key={item.env} className="flex flex-wrap items-center gap-2 rounded-xl bg-base-900 p-2">
                    {item.changed ? <CheckCircle2 size={16} className="text-emerald-400" /> : <CircleDot size={16} className="text-red-400" />}
                    <div className="min-w-0 flex-1">
                      <p className="text-sm font-semibold text-white">{item.label} <code className="text-[11px] text-slate-500">{item.env}</code></p>
                      <p className="text-[11px] text-slate-400">{item.changed ? "Sudah berganti." : item.missing ? "Belum diisi." : item.where}</p>
                    </div>
                    <a href={item.link} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 text-xs text-accent-blue hover:underline">Buka <ExternalLink size={12} /></a>
                  </div>
                ))}
                <p className="text-[11px] text-slate-500">Lalu periksa juga: riwayat commit repo DevControl dan aplikasi lain, GitHub → Settings → Security log, Cloudflare → Audit Log, serta tabel members/api_keys. Setelah semua bersih, aktifkan ulang 2FA dan matikan Mode Darurat.</p>
              </div>
            )}
          </section>

          <section className="card space-y-1.5 p-2">
            <h2 className="flex items-center gap-2 font-semibold text-white"><Radar size={17} className="text-accent-blue" /> Peringatan</h2>
            {open.length === 0 && <p className="flex items-center gap-2 text-sm text-emerald-300"><ShieldCheck size={15} /> Tidak ada peringatan yang menunggu jawaban.</p>}
            {open.map((item) => {
              const style = LEVEL_STYLE[item.level];
              return (
                <article key={item.id} className={`rounded-xl border p-2 ${style.box}`}>
                  <p className={`text-[11px] font-bold uppercase tracking-wider ${style.text}`}>{style.label} · {when(item.created_at)}</p>
                  <h3 className="mt-0.5 text-sm font-semibold text-white">{item.title}</h3>
                  <p className="mt-0.5 break-words text-xs text-slate-300">{item.detail}</p>
                  <div className="mt-1.5 flex flex-wrap gap-1.5">
                    <button type="button" disabled={busy !== null} onClick={() => void run(`ack-${item.id}`, () => answerAlert(item.id, item.level === "waspada" ? "dicatat" : "saya"))} className="rounded-lg border border-base-border px-2.5 py-1 text-xs font-medium text-slate-100 hover:bg-base-800">Ini saya / dikenali</button>
                    <button type="button" disabled={busy !== null} onClick={() => void run(`ack-${item.id}`, () => answerAlert(item.id, "bukan"))} className="rounded-lg bg-red-600 px-2.5 py-1 text-xs font-semibold text-white">Bukan saya</button>
                  </div>
                </article>
              );
            })}
            {answered.length > 0 && (
              <details className="rounded-xl border border-base-border p-2 text-xs text-slate-400">
                <summary className="cursor-pointer font-medium text-slate-300">Sudah dijawab ({answered.length})</summary>
                <ul className="mt-1.5 space-y-1">{answered.map((item) => <li key={item.id}><span className={LEVEL_STYLE[item.level].text}>{LEVEL_STYLE[item.level].label}</span> · {item.title} — <span className="text-slate-500">{item.answer} · {when(item.acknowledged_at)}</span></li>)}</ul>
              </details>
            )}
          </section>

          <section className="card space-y-1.5 p-2">
            <h2 className="flex items-center gap-2 font-semibold text-white"><Smartphone size={17} className="text-accent-blue" /> Perangkat yang sedang masuk</h2>
            {data.sessions.length === 0 && <p className="text-xs text-slate-400">Belum ada data perangkat (tercatat mulai versi ini).</p>}
            {data.sessions.map((item) => (
              <div key={item.id} className="flex flex-wrap items-center gap-2 rounded-xl bg-base-900 p-2">
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-semibold text-white">{item.name} <span className="text-[11px] font-normal text-slate-500">({item.role})</span>{item.current && <span className="ml-1.5 rounded-full bg-emerald-400/15 px-1.5 py-0.5 text-[10px] text-emerald-400">perangkat ini</span>}</p>
                  <p className="text-[11px] text-slate-400">{item.device} · IP {item.ip} · masuk {when(item.created_at)}</p>
                </div>
                {owner && !item.current && (
                  <button type="button" disabled={busy !== null} onClick={() => void run(`revoke-${item.id}`, async () => { await post({ action: "revoke_session", id: item.id }); setNotice(`${item.name} di ${item.device} dikeluarkan.`); })}
                    className="inline-flex items-center gap-1 rounded-lg border border-base-border px-2.5 py-1 text-xs text-slate-200 hover:bg-base-800"><LogOut size={12} /> Keluarkan</button>
                )}
              </div>
            ))}
          </section>

          <section className="card space-y-1.5 p-2">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h2 className="font-semibold text-white">Riwayat Keamanan</h2>
              <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Cari kejadian, IP, nama…" className="w-full max-w-xs rounded-lg border border-base-border bg-base-850 px-3 py-1.5 text-xs text-slate-100" />
            </div>
            <div className="max-h-[28rem] overflow-y-auto rounded-xl bg-base-900">
              {events.length === 0 && <p className="p-2 text-xs text-slate-400">Tidak ada kejadian.</p>}
              {events.map((item, index) => (
                <div key={`${item.created_at}-${index}`} className="flex flex-wrap items-baseline gap-x-2 border-b border-base-border/60 px-2 py-1.5 text-xs last:border-0">
                  <span className="w-28 shrink-0 text-slate-500">{when(item.created_at)}</span>
                  <span className={`font-semibold ${item.action === "login_failed" || item.action.startsWith("lockdown") ? "text-amber-400" : "text-slate-200"}`}>{ACTION_LABEL[item.action] ?? item.action}</span>
                  <span className="min-w-0 break-all text-slate-400">{item.target}</span>
                </div>
              ))}
            </div>
            <p className="text-[11px] text-slate-500">Riwayat ini disimpan di database DevControl. Salinan peringatan penting dikirim ke Telegram, sehingga tetap ada walaupun database diubah.</p>
          </section>
        </>
      )}
    </AppShell>
  );
}
