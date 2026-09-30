"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { AlertOctagon, AlertTriangle, Loader2, ShieldAlert } from "lucide-react";
import { isAdminRole, useSession } from "@/lib/session";

export type SecurityAlert = {
  id: number;
  level: "waspada" | "siaga" | "darurat";
  kind: string;
  title: string;
  detail: string;
  created_at: string;
  acknowledged_at: string;
  answer: string;
};

export const LEVEL_STYLE = {
  waspada: { label: "Waspada", box: "border-amber-300/50 bg-amber-300/10", text: "text-amber-300", solid: "bg-amber-500" },
  siaga: { label: "Siaga", box: "border-orange-400/60 bg-orange-500/10", text: "text-orange-400", solid: "bg-orange-600" },
  darurat: { label: "Darurat", box: "border-red-500/70 bg-red-600/15", text: "text-red-400", solid: "bg-red-600" },
} as const;

export async function answerAlert(id: number, answer: "saya" | "bukan" | "dicatat") {
  const response = await fetch("/api/security", {
    method: "POST", headers: { "Content-Type": "application/json" }, credentials: "same-origin", cache: "no-store",
    body: JSON.stringify({ action: "ack", id: String(id), answer }),
  });
  if (!response.ok) { const body = await response.json().catch(() => null); throw new Error(body?.error || `HTTP ${response.status}`); }
  window.dispatchEvent(new Event("devcontrol:security-changed"));
}

const IDLE_LIMIT_MS = 45 * 60 * 1000;

// Signs out after 45 minutes without any interaction, so a tab left open on
// a shared computer does not stay signed in for the whole 12-hour session.
function useIdleLogout() {
  const last = useRef(Date.now());
  useEffect(() => {
    const touch = () => { last.current = Date.now(); };
    const events = ["pointerdown", "keydown", "wheel", "touchstart"];
    events.forEach((name) => window.addEventListener(name, touch, { passive: true }));
    const check = () => {
      if (Date.now() - last.current < IDLE_LIMIT_MS) return;
      void fetch("/api/session", { method: "DELETE", credentials: "same-origin" }).catch(() => {}).finally(() => {
        window.dispatchEvent(new Event("devcontrol:logout"));
      });
    };
    const interval = window.setInterval(check, 60 * 1000);
    document.addEventListener("visibilitychange", check);
    return () => {
      events.forEach((name) => window.removeEventListener(name, touch));
      window.clearInterval(interval);
      document.removeEventListener("visibilitychange", check);
    };
  }, []);
}

// The Siaga pop-up: owner/admin see unanswered alerts from the gate and the
// patrol. Waspada is a banner, Siaga a dialog, Darurat a red dialog that
// leads to Mode Darurat.
export default function SecurityAlertCenter() {
  const { role } = useSession();
  const pathname = usePathname();
  const [alerts, setAlerts] = useState<SecurityAlert[]>([]);
  const [lockdown, setLockdown] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const allowed = isAdminRole(role);
  useIdleLogout();

  const load = useCallback(async () => {
    if (!allowed) return;
    try {
      const response = await fetch("/api/security", { credentials: "same-origin", cache: "no-store" });
      if (!response.ok) return;
      const body = await response.json();
      setAlerts((body.alerts ?? []).filter((item: SecurityAlert) => !item.acknowledged_at));
      setLockdown(!!body.lockdown?.active);
    } catch { /* offline: try again later */ }
  }, [allowed]);

  useEffect(() => {
    void load();
    const interval = window.setInterval(() => { if (!document.hidden) void load(); }, 60 * 1000);
    const refresh = () => { void load(); };
    window.addEventListener("focus", refresh);
    window.addEventListener("devcontrol:security-changed", refresh);
    return () => { window.clearInterval(interval); window.removeEventListener("focus", refresh); window.removeEventListener("devcontrol:security-changed", refresh); };
  }, [load]);

  async function answer(id: number, value: "saya" | "bukan" | "dicatat") {
    setBusy(true);
    setError("");
    try { await answerAlert(id, value); await load(); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Jawaban gagal disimpan."); }
    finally { setBusy(false); }
  }

  if (!allowed || pathname === "/security") return null;
  const top = alerts.find((item) => item.level === "darurat") ?? alerts.find((item) => item.level === "siaga") ?? null;
  const banner = !top ? alerts.find((item) => item.level === "waspada") ?? null : null;

  if (top) {
    const style = LEVEL_STYLE[top.level];
    const Icon = top.level === "darurat" ? AlertOctagon : ShieldAlert;
    return createPortal(
      <div className="fixed inset-0 z-[125] flex items-center justify-center bg-black/75 p-3" role="presentation">
        <section role="alertdialog" aria-modal="true" aria-labelledby="siaga-title" className={`w-full max-w-md rounded-2xl border-2 bg-base-900 p-4 shadow-2xl ${style.box}`}>
          <p className={`flex items-center gap-2 text-xs font-bold uppercase tracking-wider ${style.text}`}><Icon size={16} /> {style.label} keamanan</p>
          <h2 id="siaga-title" className="mt-2 text-lg font-bold text-white">{top.title}</h2>
          <p className="mt-1.5 text-sm text-slate-300">{top.detail}</p>
          <p className="mt-1 text-[11px] text-slate-500">{top.created_at} UTC{alerts.length > 1 ? ` · ${alerts.length - 1} peringatan lain menunggu` : ""}</p>
          {error && <p role="alert" className="mt-2 rounded-lg bg-red-500/10 p-2 text-xs text-red-300">{error}</p>}
          <div className="mt-3 grid gap-2 sm:grid-cols-2">
            <button type="button" disabled={busy} onClick={() => void answer(top.id, "saya")} className="rounded-xl border border-base-border px-3 py-2 text-sm font-semibold text-slate-100 hover:bg-base-800 disabled:opacity-50">
              {top.level === "darurat" ? "Sudah saya tangani" : "Ini saya / dikenali"}
            </button>
            <button type="button" disabled={busy} onClick={() => void answer(top.id, "bukan")} className="rounded-xl bg-red-600 px-3 py-2 text-sm font-semibold text-white hover:bg-red-500 disabled:opacity-50">
              {busy ? <Loader2 size={15} className="mx-auto animate-spin" /> : "Bukan saya — ada penyusup"}
            </button>
          </div>
          {(top.level === "darurat" || lockdown) && (
            <Link href="/security" className="mt-2 flex items-center justify-center gap-2 rounded-xl bg-red-600 px-3 py-2.5 text-sm font-bold text-white hover:bg-red-500">
              <AlertOctagon size={16} /> {role === "owner" ? (lockdown ? "Lanjutkan checklist Mode Darurat" : "Buka Mode Darurat & checklist") : "Buka Pusat Keamanan"}
            </Link>
          )}
        </section>
      </div>,
      document.body,
    );
  }

  if (!banner && !lockdown) return null;
  return createPortal(
    <div className="fixed bottom-3 right-3 z-[115] w-[min(24rem,calc(100vw-1.5rem))]" role="status">
      {lockdown && (
        <Link href="/security" className="mb-2 flex items-center gap-2 rounded-xl border-2 border-red-500/70 bg-base-900 p-2.5 text-sm font-semibold text-red-400 shadow-2xl">
          <AlertOctagon size={16} /> Mode Darurat aktif — selesaikan checklist
        </Link>
      )}
      {banner && (
        <div className={`rounded-xl border bg-base-900 p-2.5 shadow-2xl ${LEVEL_STYLE.waspada.box}`}>
          <p className="flex items-center gap-1.5 text-[11px] font-bold uppercase tracking-wider text-amber-300"><AlertTriangle size={13} /> Waspada</p>
          <p className="mt-1 text-sm font-semibold text-white">{banner.title}</p>
          <p className="mt-0.5 text-xs text-slate-300">{banner.detail}</p>
          <div className="mt-2 flex flex-wrap gap-1.5">
            <button type="button" disabled={busy} onClick={() => void answer(banner.id, "dicatat")} className="rounded-lg border border-base-border px-2.5 py-1 text-xs font-medium text-slate-100 hover:bg-base-800">Ini saya / dicatat</button>
            <button type="button" disabled={busy} onClick={() => void answer(banner.id, "bukan")} className="rounded-lg bg-red-600 px-2.5 py-1 text-xs font-semibold text-white">Bukan saya</button>
            <Link href="/security" className="ml-auto self-center text-xs text-accent-blue hover:underline">Pusat Keamanan</Link>
          </div>
        </div>
      )}
    </div>,
    document.body,
  );
}
