"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { AlertTriangle, Bell, CheckCircle2, Circle, Loader2, Sparkles, X } from "lucide-react";
import { notifyDataChanged, subscribeDataChanges } from "@/lib/liveUpdates";

type Option = { value: string; label: string; hint?: string };
type Confirmation = { id: string; title: string; detail: string; options: Option[]; source: "setup" | "zone" };
type Step = { id: string; label: string; status: string; detail: string; source?: string; required: boolean };
type SetupStatus = {
  ready: boolean;
  auto_runnable: boolean;
  steps: Step[];
  confirmations: Omit<Confirmation, "source">[];
  notes?: string[];
};
type Zone = { id: string; name: string; matches_domain: boolean };
type ZoneState = { zones: Zone[]; approved: unknown; mode: string; project_error?: string };

const snoozeKey = "devcontrol-confirm-snooze";
const seenKey = "devcontrol-confirm-seen";

function readJSON<T>(key: string, fallback: T): T {
  try { const raw = localStorage.getItem(key); return raw ? (JSON.parse(raw) as T) : fallback; } catch { return fallback; }
}
function writeJSON(key: string, value: unknown) {
  try { localStorage.setItem(key, JSON.stringify(value)); } catch { /* storage may be disabled */ }
}

async function api<T>(url: string, body?: unknown): Promise<T> {
  const response = await fetch(url, {
    method: body === undefined ? "GET" : "POST",
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: "same-origin",
    cache: "no-store",
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error((data as { error?: string }).error || `HTTP ${response.status}`);
  return data as T;
}

// Alerts the admin even when the app is in the background (PWA or other tab).
async function pushNotification(title: string, body: string) {
  if (typeof document === "undefined" || !document.hidden) return;
  if (typeof Notification === "undefined" || Notification.permission !== "granted") return;
  try {
    const registration = await navigator.serviceWorker?.getRegistration();
    if (registration) { await registration.showNotification(title, { body, tag: "devcontrol-confirm", icon: "/icons/icon-192.png" }); return; }
    new Notification(title, { body });
  } catch { /* notifications are best effort */ }
}

const statusIcon = (status: string) => {
  if (status === "ok") return <CheckCircle2 size={16} className="text-emerald-400" />;
  if (status === "pending") return <Loader2 size={16} className="animate-spin text-accent-blue" />;
  if (status === "confirm") return <Bell size={16} className="text-amber-300" />;
  if (status === "optional") return <Circle size={16} className="text-slate-500" />;
  return <AlertTriangle size={16} className="text-red-400" />;
};

export default function ConfirmationCenter() {
  const [status, setStatus] = useState<SetupStatus | null>(null);
  const [zone, setZone] = useState<Confirmation | null>(null);
  const [panelOpen, setPanelOpen] = useState(false);
  const [active, setActive] = useState<Confirmation | null>(null);
  const [running, setRunning] = useState(false);
  const [busyChoice, setBusyChoice] = useState("");
  const [notes, setNotes] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [notifyPermission, setNotifyPermission] = useState<string>("default");
  const autoRuns = useRef(0);
  const lastZoneCheck = useRef(0);

  const confirmations: Confirmation[] = [
    ...(status?.confirmations ?? []).map((item) => ({ ...item, source: "setup" as const })),
    ...(zone ? [zone] : []),
  ];

  const checkZone = useCallback(async () => {
    lastZoneCheck.current = Date.now();
    try {
      const data = await api<ZoneState>("/api/zone-approval");
      if (data.approved || data.mode) { setZone(null); return; }
      const match = data.zones.find((item) => item.matches_domain);
      if (!match || data.project_error) {
        // Nothing to decide: enable Go API metrics automatically.
        await api("/api/zone-approval", { action: "auto" });
        setZone(null);
        notifyDataChanged();
        return;
      }
      const snoozed = readJSON<Record<string, number>>(snoozeKey, {});
      const id = `zone:${match.id}`;
      if ((snoozed[id] ?? 0) > Date.now()) { setZone(null); return; }
      setZone({
        id, source: "zone", title: "Aktifkan metrik trafik Cloudflare?",
        detail: `Zona ${match.name} cocok dengan domain aplikasi ini. Jika diaktifkan, grafik Network dan Requests memakai data Cloudflare, dan CF_ZONE_ID disimpan otomatis ke Vercel.`,
        options: [{ value: match.id, label: "Aktifkan" }],
      });
    } catch { /* the Settings page still offers manual approval */ }
  }, []);

  const load = useCallback(async (fresh = false) => {
    try {
      const data = await api<SetupStatus>(`/api/auto-setup${fresh ? "?fresh=1" : ""}`);
      setStatus(data);
      setError("");
      if (data.ready && Date.now() - lastZoneCheck.current > 10 * 60 * 1000) void checkZone();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Status setup tidak dapat dibaca.");
    }
  }, [checkZone]);

  const runSetup = useCallback(async () => {
    setRunning(true);
    setError("");
    try {
      const data = await api<SetupStatus>("/api/auto-setup", { action: "run" });
      setStatus(data);
      setNotes(data.notes ?? []);
      notifyDataChanged();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Setup otomatis gagal.");
    } finally { setRunning(false); }
  }, []);

  // Poll quickly while something is unfinished, slowly once everything is ready.
  useEffect(() => {
    void load();
    if (typeof Notification !== "undefined") setNotifyPermission(Notification.permission);
    const fast = !status?.ready || confirmations.length > 0;
    const interval = setInterval(() => { if (!document.hidden || fast) void load(); }, fast ? 5000 : 60000);
    const onFocus = () => { void load(); };
    window.addEventListener("focus", onFocus);
    const unsubscribe = subscribeDataChanges(() => { void load(); });
    const onOpen = () => setPanelOpen(true);
    window.addEventListener("devcontrol:open-setup", onOpen);
    return () => { clearInterval(interval); window.removeEventListener("focus", onFocus); window.removeEventListener("devcontrol:open-setup", onOpen); unsubscribe(); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [load, status?.ready, confirmations.length]);

  // Run every step that needs no decision, without the admin pressing anything.
  useEffect(() => {
    if (!status?.auto_runnable || running || autoRuns.current >= 3) return;
    autoRuns.current += 1;
    setPanelOpen(true);
    void runSetup();
  }, [status?.auto_runnable, running, runSetup]);

  // Show a new confirmation immediately, once per id.
  useEffect(() => {
    if (active || confirmations.length === 0) return;
    const seen = readJSON<string[]>(seenKey, []);
    const next = confirmations.find((item) => !seen.includes(item.id)) ?? null;
    if (!next) return;
    writeJSON(seenKey, [...seen, next.id].slice(-50));
    setActive(next);
    void pushNotification(next.title, next.detail);
  }, [confirmations, active]);

  async function answer(item: Confirmation, value: string) {
    setBusyChoice(value);
    setError("");
    try {
      if (item.source === "setup") {
        const data = await api<SetupStatus>("/api/auto-setup", { action: "choose", id: item.id, value });
        setStatus(data);
        setNotes(data.notes ?? []);
      } else {
        await api("/api/zone-approval", { zone_id: value });
        setZone(null);
      }
      setActive(null);
      notifyDataChanged();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Konfirmasi gagal dikirim.");
    } finally { setBusyChoice(""); }
  }

  function later(item: Confirmation) {
    if (item.source === "zone") {
      const snoozed = readJSON<Record<string, number>>(snoozeKey, {});
      writeJSON(snoozeKey, { ...snoozed, [item.id]: Date.now() + 7 * 24 * 3600 * 1000 });
      setZone(null);
    }
    setActive(null);
  }

  async function enableNotifications() {
    if (typeof Notification === "undefined") return;
    setNotifyPermission(await Notification.requestPermission());
  }

  const pending = confirmations.length;
  const unfinished = status ? !status.ready : false;
  if (!status && !error) return null;
  if (!panelOpen && !active && pending === 0 && !unfinished && !running) return null;

  return (
    <>
      {!panelOpen && (
        <button type="button" onClick={() => setPanelOpen(true)}
          className="fixed bottom-[calc(1rem+env(safe-area-inset-bottom))] right-4 z-[55] inline-flex items-center gap-2 rounded-full border border-base-border bg-base-850 px-4 py-2.5 text-sm font-semibold text-slate-100 shadow-glow">
          {running ? <Loader2 size={16} className="animate-spin text-accent-blue" /> : pending > 0 ? <Bell size={16} className="text-amber-300" /> : <Sparkles size={16} className="text-accent-blue" />}
          {running ? "Menyiapkan otomatis…" : pending > 0 ? `${pending} perlu konfirmasi` : "Setup belum selesai"}
          {pending > 0 && <span className="h-2 w-2 animate-pulse rounded-full bg-amber-400" />}
        </button>
      )}

      {panelOpen && (
        <aside className="card fixed bottom-[calc(1rem+env(safe-area-inset-bottom))] right-4 z-[55] max-h-[75vh] w-[calc(100vw-2rem)] max-w-sm overflow-y-auto p-4" aria-label="Setup otomatis">
          <div className="mb-3 flex items-center justify-between gap-2">
            <div className="flex items-center gap-2">
              <Sparkles size={17} className="text-accent-blue" />
              <h2 className="text-sm font-bold text-white">Setup otomatis</h2>
            </div>
            <button type="button" aria-label="Tutup" onClick={() => setPanelOpen(false)} className="rounded-lg p-1 text-slate-400 hover:bg-base-800"><X size={16} /></button>
          </div>

          {confirmations.map((item) => (
            <button key={item.id} type="button" onClick={() => setActive(item)}
              className="mb-2 flex w-full items-center gap-2 rounded-xl border border-amber-400/40 bg-amber-400/10 p-3 text-left text-sm text-amber-200">
              <Bell size={15} /> <span className="flex-1 font-semibold">{item.title}</span> <span className="text-xs">Jawab</span>
            </button>
          ))}

          <ul className="space-y-2">
            {(status?.steps ?? []).map((step) => (
              <li key={step.id} className="flex gap-2.5 rounded-xl bg-base-800/60 p-2.5">
                <span className="mt-0.5">{statusIcon(step.status)}</span>
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium text-slate-100">
                    {step.label}
                    {step.source === "auto" && <span className="ml-1.5 rounded bg-accent-blue/15 px-1.5 py-0.5 text-[10px] text-accent-blue">otomatis</span>}
                    {step.source === "derived" && <span className="ml-1.5 rounded bg-accent-purple/15 px-1.5 py-0.5 text-[10px] text-accent-purple">diturunkan</span>}
                  </p>
                  <p className="break-words text-xs text-slate-400">{step.detail}</p>
                </div>
              </li>
            ))}
          </ul>

          {notes.length > 0 && (
            <div className="mt-3 space-y-1 rounded-xl bg-base-800/60 p-2.5 text-xs text-slate-300">
              {notes.map((note, index) => <p key={index}>• {note}</p>)}
            </div>
          )}
          {error && <p role="alert" className="mt-3 rounded-lg bg-red-500/10 p-2.5 text-xs text-red-300">{error}</p>}

          <div className="mt-3 flex flex-wrap gap-2">
            <button type="button" disabled={running} onClick={() => { autoRuns.current = 0; void runSetup(); }}
              className="rounded-lg bg-accent-blue px-3 py-2 text-xs font-semibold text-white disabled:opacity-50">
              {running ? "Menyiapkan…" : "Jalankan ulang setup"}
            </button>
            {notifyPermission === "default" && (
              <button type="button" onClick={() => void enableNotifications()} className="rounded-lg border border-base-border px-3 py-2 text-xs font-semibold text-slate-200">
                Aktifkan notifikasi
              </button>
            )}
          </div>
        </aside>
      )}

      {active && (
        <div className="fixed inset-0 z-[70] flex items-end justify-center p-4 sm:items-center" role="dialog" aria-modal="true" aria-labelledby="confirm-title">
          <button aria-label="Nanti saja" onClick={() => later(active)} className="absolute inset-0 bg-black/70 backdrop-blur-sm" />
          <div className="card relative w-full max-w-md space-y-4 p-5 sm:p-6">
            <div className="flex items-start gap-3">
              <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-amber-400/15 text-amber-300"><Bell size={19} /></span>
              <div>
                <h2 id="confirm-title" className="text-base font-bold text-white">{active.title}</h2>
                <p className="mt-1 text-sm text-slate-400">{active.detail}</p>
              </div>
            </div>
            <div className="space-y-2">
              {active.options.map((option) => (
                <button key={option.value} type="button" disabled={busyChoice !== ""} onClick={() => void answer(active, option.value)}
                  className="flex w-full items-center justify-between gap-3 rounded-xl border border-base-border bg-base-800 p-3 text-left hover:border-accent-blue disabled:opacity-50">
                  <span>
                    <span className="block text-sm font-semibold text-slate-100">{option.label}</span>
                    {option.hint && <span className="block text-xs text-slate-400">{option.hint}</span>}
                  </span>
                  {busyChoice === option.value && <Loader2 size={16} className="animate-spin text-accent-blue" />}
                </button>
              ))}
            </div>
            {error && <p role="alert" className="rounded-lg bg-red-500/10 p-2.5 text-xs text-red-300">{error}</p>}
            <button type="button" onClick={() => later(active)} className="text-xs text-slate-400 hover:text-white">Nanti saja</button>
          </div>
        </div>
      )}
    </>
  );
}
