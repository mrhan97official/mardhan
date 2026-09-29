"use client";

import { useEffect, useRef, useState, type FormEvent } from "react";
import { createPortal } from "react-dom";
import { CheckCircle2, ShieldCheck, X } from "lucide-react";
import { useSession } from "@/lib/session";

// Dangerous actions (Update Diri, secrets, members, ZIP, delete) need the
// credential typed again within 15 minutes. The server answers such a
// request with 403 {"reauth": true}; this watches every API response for
// that answer and asks for the password/token (and 2FA code) once, after
// which the person repeats the action.
export default function ReauthDialog() {
  const { role } = useSession();
  const [open, setOpen] = useState(false);
  const [credential, setCredential] = useState("");
  const [code, setCode] = useState("");
  const [needsCode, setNeedsCode] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const owner = role === "owner";

  useEffect(() => {
    const original = window.fetch;
    const watched: typeof window.fetch = async (input, init) => {
      const response = await original(input, init);
      if (response.status === 403) {
        response.clone().json()
          .then((body) => { if (body && body.reauth === true) window.dispatchEvent(new Event("devcontrol:reauth")); })
          .catch(() => {});
      }
      return response;
    };
    window.fetch = watched;
    const onReauth = () => { setOpen(true); setDone(false); setError(""); };
    window.addEventListener("devcontrol:reauth", onReauth);
    return () => { window.fetch = original; window.removeEventListener("devcontrol:reauth", onReauth); };
  }, []);

  useEffect(() => { if (open && !done) window.setTimeout(() => inputRef.current?.focus(), 50); }, [open, done]);

  function close() {
    setOpen(false);
    setCredential("");
    setCode("");
    setNeedsCode(false);
    setError("");
  }

  async function confirm(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const response = await fetch("/api/session", {
        method: "POST", headers: { "Content-Type": "application/json" }, credentials: "same-origin", cache: "no-store",
        body: JSON.stringify({ action: "confirm", credential: credential.trim(), code: code.trim() }),
      });
      const body = await response.json().catch(() => ({}));
      if (body?.totp_required) { setNeedsCode(true); setError(body.error || "Masukkan kode 2FA."); return; }
      if (!response.ok) throw new Error(body?.error || `HTTP ${response.status}`);
      setDone(true);
      setCredential("");
      setCode("");
      window.setTimeout(close, 1800);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Konfirmasi gagal.");
    } finally { setBusy(false); }
  }

  if (!open) return null;
  return createPortal(
    <div className="fixed inset-0 z-[130] flex items-center justify-center bg-black/70 p-3" role="presentation"
      onMouseDown={(event) => { if (event.target === event.currentTarget && !busy) close(); }}>
      <section role="dialog" aria-modal="true" aria-labelledby="reauth-title" className="w-full max-w-sm rounded-2xl border border-base-border bg-base-900 p-4 shadow-2xl">
        <div className="flex items-start justify-between gap-2">
          <h2 id="reauth-title" className="flex items-center gap-2 text-base font-bold text-white"><ShieldCheck size={18} className="text-accent-blue" /> Konfirmasi identitas</h2>
          <button type="button" aria-label="Tutup" disabled={busy} onClick={close} className="rounded-lg p-1 text-slate-300 hover:bg-base-800 disabled:opacity-50"><X size={18} /></button>
        </div>
        {done ? (
          <p role="status" className="mt-3 flex items-start gap-2 rounded-lg bg-emerald-500/10 p-3 text-sm text-emerald-300">
            <CheckCircle2 size={16} className="mt-0.5 shrink-0" /> Terkonfirmasi untuk 15 menit. Silakan ulangi aksi tadi.
          </p>
        ) : (
          <form onSubmit={(event) => void confirm(event)} className="mt-3 space-y-3">
            <p className="text-xs text-slate-400">Aksi ini membuka rahasia, mengganti kode, atau mengubah akses, jadi {owner ? "kata sandi admin" : "token akses Anda"} perlu dimasukkan lagi. Konfirmasi berlaku 15 menit.</p>
            <label className="block text-xs font-medium text-slate-300">{owner ? "Kata sandi admin" : "Token akses (dcm_…)"}
              <input ref={inputRef} type="password" autoComplete="current-password" required maxLength={512} spellCheck={false} value={credential}
                onChange={(event) => setCredential(event.target.value)} className="mt-1.5 w-full rounded-lg border border-base-border bg-base-850 px-3 py-2 text-sm text-slate-100" />
            </label>
            {needsCode && (
              <label className="block text-xs font-medium text-slate-300">Kode 2FA (6 digit)
                <input inputMode="numeric" autoComplete="one-time-code" pattern="[0-9 ]*" maxLength={7} required value={code}
                  onChange={(event) => setCode(event.target.value)} className="mt-1.5 w-full rounded-lg border border-base-border bg-base-850 px-3 py-2 text-center font-mono text-lg tracking-[0.3em] text-slate-100" />
              </label>
            )}
            {error && <p role="alert" className="rounded-lg bg-red-500/10 p-2 text-xs text-red-300">{error}</p>}
            <button type="submit" disabled={busy} className="w-full rounded-lg bg-accent-blue px-3 py-2 text-sm font-semibold text-white disabled:opacity-50">{busy ? "Memeriksa…" : "Konfirmasi"}</button>
          </form>
        )}
      </section>
    </div>,
    document.body,
  );
}
