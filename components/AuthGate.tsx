"use client";

import { useEffect, useState, type FormEvent } from "react";
import { LockKeyhole, RefreshCw } from "lucide-react";
import { cacheClear } from "@/lib/db";

type State = "checking" | "authenticated" | "login";

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
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function check() {
    try {
      const response = await fetch("/api/session", { cache: "no-store", credentials: "same-origin" });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || `HTTP ${response.status}`);
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
    const onLogout = () => { setState("login"); setPassword(""); void clearPrivateCaches(); };
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
        body: JSON.stringify({ password }), cache: "no-store",
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || `HTTP ${response.status}`);
      await clearPrivateCaches();
      setPassword("");
      setState("authenticated");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Login gagal.");
    } finally { setBusy(false); }
  }

  if (state === "authenticated") return <>{children}</>;
  return (
    <main className="flex min-h-screen items-center justify-center p-4">
      <div className="card w-full max-w-md space-y-5 p-6 sm:p-8">
        <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-accent-blue/15 text-accent-blue"><LockKeyhole size={23} /></div>
        <div>
          <h1 className="text-xl font-bold">Masuk ke DevControl</h1>
          <p className="mt-1 text-sm text-slate-400">Aksi deployment, database, dan API memerlukan sesi admin.</p>
        </div>
        {state === "checking" ? <p role="status" className="text-sm text-slate-400">Memeriksa sesi…</p> : (
          <form onSubmit={(event) => void login(event)} className="space-y-4">
            <label className="block text-sm text-slate-300">Kata sandi admin
              <input type="password" autoComplete="current-password" required value={password}
                onChange={(event) => setPassword(event.target.value)}
                className="mt-2 w-full rounded-xl border border-base-border bg-base-850 p-3 text-slate-100" />
            </label>
            <button type="submit" disabled={busy} className="w-full rounded-xl bg-accent-blue px-4 py-3 text-sm font-semibold text-white disabled:opacity-50">
              {busy ? "Memeriksa…" : "Masuk"}
            </button>
          </form>
        )}
        {error && <p role="alert" className="rounded-lg bg-red-500/10 p-3 text-sm text-red-300">{error}</p>}
        <button type="button" onClick={() => { setState("checking"); void check(); }} className="inline-flex items-center gap-2 text-xs text-slate-400 hover:text-white">
          <RefreshCw size={13} /> Periksa ulang koneksi
        </button>
      </div>
    </main>
  );
}
