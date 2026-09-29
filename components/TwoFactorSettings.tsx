"use client";

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Copy, ExternalLink, KeyRound, Loader2, ShieldAlert, ShieldCheck } from "lucide-react";

type Status = { enabled: boolean; reset_active?: boolean };
type Setup = { secret: string; uri: string };

async function post(body: Record<string, string>) {
  const response = await fetch("/api/two-factor", {
    method: "POST", headers: { "Content-Type": "application/json" }, credentials: "same-origin", cache: "no-store", body: JSON.stringify(body),
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data?.error || `HTTP ${response.status}`);
  return data;
}

// Owner sign-in with a second factor (TOTP: Google Authenticator, Microsoft
// Authenticator, 1Password, Bitwarden …). Shown only to the owner.
export default function TwoFactorSettings() {
  const [status, setStatus] = useState<Status | null>(null);
  const [setup, setSetup] = useState<Setup | null>(null);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [disabling, setDisabling] = useState(false);

  const load = useCallback(async () => {
    try {
      const response = await fetch("/api/two-factor", { credentials: "same-origin", cache: "no-store" });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(data?.error || `HTTP ${response.status}`);
      setStatus({ enabled: !!data.enabled, reset_active: !!data.reset_active });
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Status 2FA tidak dapat dibaca."); }
  }, []);

  useEffect(() => { void load(); }, [load]);

  async function run(action: () => Promise<void>) {
    setBusy(true);
    setError("");
    setMessage("");
    try { await action(); } catch (reason) { setError(reason instanceof Error ? reason.message : "Permintaan gagal."); }
    finally { setBusy(false); }
  }

  const begin = () => run(async () => {
    const data = await post({ action: "begin" });
    setSetup({ secret: data.secret, uri: data.uri });
    setCode("");
  });

  const enable = (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); void run(async () => {
    await post({ action: "enable", code: code.trim() });
    setSetup(null);
    setCode("");
    setMessage("2FA aktif. Mulai sekarang login owner meminta kode dari aplikasi authenticator.");
    await load();
  }); };

  const disable = (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); void run(async () => {
    await post({ action: "disable", code: code.trim() });
    setDisabling(false);
    setCode("");
    setMessage("2FA dimatikan. Login owner kembali hanya memakai kata sandi.");
    await load();
  }); };

  const grouped = setup?.secret.replace(/(.{4})/g, "$1 ").trim() ?? "";

  return (
    <section className="card max-w-3xl space-y-2 p-2" aria-labelledby="two-factor-title">
      <div className="flex items-start gap-2">
        <div className={`rounded-xl p-2 ${status?.enabled ? "bg-emerald-400/15 text-emerald-400" : "bg-amber-400/15 text-amber-400"}`}>
          {status?.enabled ? <ShieldCheck size={19} /> : <ShieldAlert size={19} />}
        </div>
        <div className="min-w-0">
          <h3 id="two-factor-title" className="text-lg font-semibold text-white">Keamanan akun · 2FA owner</h3>
          <p className="mt-1 text-sm text-slate-400">Login owner meminta kode 6 digit dari aplikasi authenticator selain kata sandi, sehingga kata sandi yang bocor saja tidak cukup untuk masuk.</p>
        </div>
      </div>

      {status === null && !error && <p role="status" className="text-xs text-slate-400">Memeriksa status 2FA…</p>}
      {status?.reset_active && <p role="alert" className="rounded-lg bg-amber-400/10 p-2 text-xs text-amber-200">DEVCONTROL_TOTP_RESET=1 sedang aktif, jadi 2FA diabaikan. Hapus variabel itu di Vercel lalu redeploy.</p>}

      {status && !status.enabled && !setup && (
        <button type="button" disabled={busy} onClick={() => void begin()} className="inline-flex items-center gap-2 rounded-xl bg-accent-blue px-4 py-2.5 text-sm font-semibold text-white disabled:opacity-50">
          {busy ? <Loader2 size={16} className="animate-spin" /> : <KeyRound size={16} />} Aktifkan 2FA
        </button>
      )}

      {setup && (
        <form onSubmit={enable} className="space-y-2 rounded-xl border border-base-border bg-base-800/40 p-2">
          <p className="text-sm text-slate-300">1. Di ponsel, buka aplikasi authenticator lalu tambahkan akun baru. Di ponsel ini Anda bisa langsung menekan tombol di bawah; di perangkat lain, masukkan kunci secara manual.</p>
          <div className="flex flex-wrap items-center gap-2">
            <a href={setup.uri} className="inline-flex items-center gap-1.5 rounded-lg border border-base-border px-3 py-2 text-xs font-medium text-slate-200 hover:bg-base-800"><ExternalLink size={14} /> Buka di aplikasi authenticator</a>
            <button type="button" onClick={() => { void navigator.clipboard?.writeText(setup.secret).then(() => setMessage("Kunci disalin.")).catch(() => {}); }}
              className="inline-flex items-center gap-1.5 rounded-lg border border-base-border px-3 py-2 text-xs font-medium text-slate-200 hover:bg-base-800"><Copy size={14} /> Salin kunci</button>
          </div>
          <p className="break-all rounded-lg bg-base-950/60 p-2 font-mono text-sm tracking-wider text-slate-100">{grouped}</p>
          <p className="text-[11px] text-slate-500">Nama akun: DevControl (owner) · jenis berbasis waktu · 6 digit · 30 detik.</p>
          <label className="block text-sm text-slate-300">2. Masukkan kode 6 digit yang muncul
            <input inputMode="numeric" autoComplete="one-time-code" pattern="[0-9 ]*" maxLength={7} required value={code} onChange={(event) => setCode(event.target.value)}
              className="mt-1.5 w-full max-w-[12rem] rounded-lg border border-base-border bg-base-850 px-3 py-2 text-center font-mono text-lg tracking-[0.3em] text-slate-100" />
          </label>
          <div className="flex flex-wrap gap-2">
            <button type="submit" disabled={busy} className="rounded-lg bg-accent-blue px-4 py-2 text-sm font-semibold text-white disabled:opacity-50">{busy ? "Memeriksa…" : "Aktifkan"}</button>
            <button type="button" disabled={busy} onClick={() => { setSetup(null); setCode(""); }} className="rounded-lg border border-base-border px-4 py-2 text-sm text-slate-300 hover:bg-base-800">Batal</button>
          </div>
        </form>
      )}

      {status?.enabled && !disabling && (
        <div className="flex flex-wrap items-center gap-2">
          <span className="rounded-full bg-emerald-400/15 px-2.5 py-1 text-xs font-semibold text-emerald-400">2FA aktif</span>
          <button type="button" onClick={() => { setDisabling(true); setCode(""); }} className="rounded-lg border border-base-border px-3 py-1.5 text-xs text-slate-300 hover:bg-base-800">Matikan 2FA</button>
        </div>
      )}
      {status?.enabled && disabling && (
        <form onSubmit={disable} className="flex flex-wrap items-end gap-2">
          <label className="block text-xs text-slate-300">Kode 2FA saat ini
            <input inputMode="numeric" autoComplete="one-time-code" pattern="[0-9 ]*" maxLength={7} required value={code} onChange={(event) => setCode(event.target.value)}
              className="mt-1 w-40 rounded-lg border border-base-border bg-base-850 px-3 py-2 text-center font-mono tracking-[0.3em] text-slate-100" />
          </label>
          <button type="submit" disabled={busy} className="rounded-lg bg-red-600 px-3 py-2 text-xs font-semibold text-white disabled:opacity-50">Matikan</button>
          <button type="button" onClick={() => setDisabling(false)} className="rounded-lg border border-base-border px-3 py-2 text-xs text-slate-300">Batal</button>
        </form>
      )}

      {error && <p role="alert" className="rounded-lg bg-red-500/10 p-2 text-sm text-red-300">{error}</p>}
      {message && <p role="status" className="rounded-lg bg-emerald-500/10 p-2 text-sm text-emerald-300">{message}</p>}
      <p className="text-[11px] text-slate-500">Ponsel hilang? Isi DEVCONTROL_TOTP_RESET=1 di Environment Variables Vercel lalu redeploy untuk mematikan 2FA sementara, masuk dengan kata sandi, aktifkan ulang 2FA, lalu hapus variabel itu.</p>
    </section>
  );
}
