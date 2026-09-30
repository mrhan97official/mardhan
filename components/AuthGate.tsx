"use client";

import { useEffect, useState, type FormEvent } from "react";
import { CheckCircle2, AlertCircle, LockKeyhole, RefreshCw } from "lucide-react";
import { cacheClear } from "@/lib/db";
import { SessionContext, type Role, type Session } from "@/lib/session";
import ReauthDialog from "@/components/ReauthDialog";
import SecurityAlertCenter from "@/components/SecurityAlertCenter";

type State = "checking" | "authenticated" | "login";
type CoreVariable = { key: string; set: boolean; required: boolean };

const corePurpose: Record<string, string> = {
  CF_API_TOKEN: "Token Cloudflare: akun, database D1, dan bucket R2 dideteksi/dibuat otomatis",
  DEVCONTROL_ADMIN_PASSWORD: "Kata sandi masuk (minimal 16 karakter)",
  GITHUB_TOKEN: "Opsional: fitur Aplikasi Baru / Update",
  VERCEL_TOKEN: "Opsional: fitur deployment & metrik zona",
};

async function clearPrivateCaches() {
  await cacheClear().catch(() => {});
  if (typeof caches !== "undefined") {
    const keys = await caches.keys().catch(() => []);
    await Promise.all(keys.filter((key) => key.includes("api-cache")).map((key) => caches.delete(key)));
  }
}

export default function AuthGate({ children }: { children: React.ReactNode }) {
  const [state, setState] = useState<State>("checking");
  const [password, setPassword] = useState("");
  // Owner 2FA: the server asks for the 6-digit code after a correct password.
  const [code, setCode] = useState("");
  const [needsCode, setNeedsCode] = useState(false);
  const [session, setSession] = useState<Session>({ role: "viewer", name: "" });
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [core, setCore] = useState<CoreVariable[]>([]);

  async function check() {
    try {
      const response = await fetch("/api/session", { cache: "no-store", credentials: "same-origin" });
      const body = await response.json().catch(() => ({}));
      if (response.status === 503) {
        // Admin not configured yet: show which core variables are still empty.
        const setup = await fetch("/api/auto-setup", { cache: "no-store", credentials: "same-origin" }).then((r) => r.json()).catch(() => ({}));
        setCore(Array.isArray(setup.core) ? setup.core : []);
      } else setCore([]);
      if (!response.ok) throw new Error(body.error || `HTTP ${response.status}`);
      if (body.authenticated) setSession({ role: (body.role as Role) || "viewer", name: body.name || "" });
      setState(body.authenticated ? "authenticated" : "login");
      if (!body.authenticated) void clearPrivateCaches();
      setError("");
    } catch (reason) {
      setState("login");
      void clearPrivateCaches();
      setError(reason instanceof Error ? reason.message : "Tidak dapat memeriksa sesi admin.");
    }
  }

  useEffect(() => {
    void check();
    const interval = setInterval(() => { void check(); }, 5 * 60 * 1000);
    const onLogout = () => { setState("login"); setPassword(""); setCode(""); setNeedsCode(false); void clearPrivateCaches(); };
    window.addEventListener("devcontrol:logout", onLogout);
    return () => { clearInterval(interval); window.removeEventListener("devcontrol:logout", onLogout); };
  }, []);

  async function login(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const response = await fetch("/api/session", {
        method: "POST", headers: { "Content-Type": "application/json" }, credentials: "same-origin",
        body: JSON.stringify({ credential: password.trim(), code: code.trim() }), cache: "no-store",
      });
      const body = await response.json().catch(() => ({}));
      if (body.totp_required) { setNeedsCode(true); throw new Error(body.error || "Masukkan kode 2FA."); }
      if (!response.ok) throw new Error(body.error || `HTTP ${response.status}`);
      await clearPrivateCaches();
      setPassword("");
      setCode("");
      setNeedsCode(false);
      setSession({ role: (body.role as Role) || "viewer", name: body.name || "" });
      setState("authenticated");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Login gagal.");
    } finally { setBusy(false); }
  }

  if (state === "authenticated") return <SessionContext.Provider value={session}>{children}<ReauthDialog /><SecurityAlertCenter /></SessionContext.Provider>;
  return (
    <main className="flex min-h-screen items-center justify-center p-4">
      <div className="card w-full max-w-md space-y-5 p-6 sm:p-8">
        <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-accent-blue/15 text-accent-blue"><LockKeyhole size={23} /></div>
        <div>
          <h1 className="text-xl font-bold">Masuk ke DevControl</h1>
          <p className="mt-1 text-sm text-slate-400">Owner memakai kata sandi admin. Member memakai token akses (dcm_…) yang diberikan owner.</p>
        </div>
        {state === "checking" ? <p role="status" className="text-sm text-slate-400">Memeriksa sesi…</p> : (
          <form onSubmit={(event) => void login(event)} className="space-y-4">
            <label className="block text-sm text-slate-300">Kata sandi admin atau token akses
              <input type="password" autoComplete="current-password" required value={password} maxLength={512} spellCheck={false}
                onChange={(event) => { setPassword(event.target.value); if (needsCode) { setNeedsCode(false); setCode(""); } }}
                className="mt-2 w-full rounded-xl border border-base-border bg-base-850 p-3 text-slate-100" />
            </label>
            {needsCode && (
              <label className="block text-sm text-slate-300">Kode 2FA dari aplikasi authenticator
                <input inputMode="numeric" autoComplete="one-time-code" pattern="[0-9 ]*" maxLength={7} required autoFocus value={code}
                  onChange={(event) => setCode(event.target.value)}
                  className="mt-2 w-full rounded-xl border border-base-border bg-base-850 p-3 text-center font-mono text-xl tracking-[0.35em] text-slate-100" />
              </label>
            )}
            <button type="submit" disabled={busy} className="w-full rounded-xl bg-accent-blue px-4 py-3 text-sm font-semibold text-white disabled:opacity-50">
              {busy ? "Memeriksa…" : "Masuk"}
            </button>
          </form>
        )}
        {error && <p role="alert" className="rounded-lg bg-red-500/10 p-3 text-sm text-red-300">{error}</p>}
        {core.length > 0 && (
          <div className="space-y-2 rounded-xl border border-base-border p-3">
            <p className="text-xs font-semibold text-slate-300">Cukup isi variabel inti ini di Vercel → Settings → Environment Variables, lalu Redeploy. Sisanya disiapkan otomatis.</p>
            {core.map((item) => (
              <div key={item.key} className="flex items-start gap-2 text-xs">
                {item.set ? <CheckCircle2 size={15} className="mt-0.5 shrink-0 text-emerald-400" /> : <AlertCircle size={15} className={`mt-0.5 shrink-0 ${item.required ? "text-red-400" : "text-slate-500"}`} />}
                <span><code className="font-mono text-slate-100">{item.key}</code><span className="block text-slate-400">{corePurpose[item.key] ?? ""}</span></span>
              </div>
            ))}
          </div>
        )}
        <button type="button" onClick={() => { setState("checking"); void check(); }} className="inline-flex items-center gap-2 text-xs text-slate-400 hover:text-white">
          <RefreshCw size={13} /> Periksa ulang koneksi
        </button>
      </div>
    </main>
  );
}
