"use client";

import { useCallback, useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { BellOff, BellRing, Send, X } from "lucide-react";
import {
  PUSH_EVENTS, disablePush, enablePush, pushInfo, pushStatus, pushSupport, sendTestPush, warmPush,
  type PushEvent, type PushSupport,
} from "@/lib/push";

// Per-device switch for notifications that arrive even when DevControl is
// closed. Every role can use it; confirmations are offered to admins only.
export default function PushNotificationSettings({ compact = false }: { compact?: boolean }) {
  const [support, setSupport] = useState<PushSupport | null>(null);
  const [available, setAvailable] = useState<PushEvent[]>([]);
  const [subscribed, setSubscribed] = useState(false);
  const [events, setEvents] = useState<PushEvent[]>([]);
  const [permission, setPermission] = useState<NotificationPermission | "unsupported">("default");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [lastError, setLastError] = useState("");

  const load = useCallback(async () => {
    const mode = pushSupport();
    setSupport(mode);
    if (mode !== "ok") return;
    setPermission(Notification.permission);
    // The event list comes from the server only; it must never wait on the
    // service worker, otherwise a worker problem hides every choice and
    // disables the button.
    let info: Awaited<ReturnType<typeof pushInfo>>;
    try {
      info = await pushInfo();
      setAvailable(info.events);
      setEvents(info.events);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Daftar notifikasi belum dapat dibaca.");
      return;
    }
    try {
      const status = await pushStatus();
      setSubscribed(status.subscribed);
      if (status.subscribed && "events" in status && status.events) setEvents(status.events);
      setLastError(("last_error" in status && status.last_error) || "");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Status perangkat belum dapat dibaca.");
    }
    // Get the worker and key ready now, so the tap goes straight to the prompt.
    void warmPush().catch(() => undefined);
  }, []);

  useEffect(() => { void load(); }, [load]);

  async function run(action: () => Promise<void>, done: string) {
    setBusy(true);
    setError("");
    setMessage("");
    try { await action(); setMessage(done); }
    catch (cause) { setError(cause instanceof Error ? cause.message : "Notifikasi gagal diatur."); }
    finally {
      setBusy(false);
      if (typeof Notification !== "undefined") setPermission(Notification.permission);
    }
  }

  const enable = () => run(async () => {
    const saved = await enablePush(events.length ? events : available);
    setEvents(saved);
    setSubscribed(true);
    setLastError("");
  }, "Notifikasi aktif di perangkat ini. Pemberitahuan tetap masuk walaupun DevControl ditutup.");

  const disable = () => run(async () => {
    await disablePush();
    setSubscribed(false);
  }, "Notifikasi dimatikan untuk perangkat ini.");

  const toggle = (event: PushEvent) => {
    const next = events.includes(event) ? events.filter((item) => item !== event) : [...events, event];
    setEvents(next);
    if (subscribed) void run(async () => { setEvents(await enablePush(next)); }, "Pilihan notifikasi disimpan.");
  };

  const test = () => run(async () => { await sendTestPush(); }, "Notifikasi uji dikirim. Coba juga dengan DevControl ditutup.");

  return (
    <section className={compact ? "space-y-2" : "card max-w-3xl space-y-2 p-2"} aria-labelledby="push-title">
      <div>
        <h3 id="push-title" className="flex items-center gap-2 text-lg font-semibold text-white"><BellRing size={18} className="text-accent-blue" /> Notifikasi perangkat ini</h3>
        <p className="mt-1 text-sm text-slate-400">Pemberitahuan tetap masuk walaupun DevControl ditutup. Atur di setiap perangkat yang ingin menerima notifikasi.</p>
      </div>

      {support === "ios-install" && (
        <p className="rounded-lg bg-amber-500/10 p-2 text-sm text-amber-200">Di iPhone/iPad, pasang DevControl dulu: buka di Safari → Bagikan → Tambahkan ke Layar Utama, lalu buka dari ikon itu dan aktifkan notifikasi di sini (perlu iOS/iPadOS 16.4 atau lebih baru).</p>
      )}
      {support === "unsupported" && <p className="rounded-lg bg-base-800 p-2 text-sm text-slate-300">Browser ini belum mendukung notifikasi push. Gunakan Chrome, Edge, Firefox, atau Safari versi terbaru.</p>}
      {support === "ok" && permission === "denied" && (
        <p className="rounded-lg bg-amber-500/10 p-2 text-sm text-amber-200">Notifikasi untuk situs ini diblokir browser. Izinkan lewat ikon gembok/pengaturan situs di browser, lalu muat ulang halaman.</p>
      )}

      {support === "ok" && (
        <>
          <fieldset className="space-y-1.5" disabled={busy}>
            <legend className="mb-1 text-xs font-medium text-slate-300">Jenis notifikasi</legend>
            {PUSH_EVENTS.filter((item) => available.includes(item.id)).map((item) => (
              <label key={item.id} className="flex cursor-pointer items-start gap-2 rounded-lg border border-base-border px-2.5 py-2 hover:bg-base-800/60">
                <input type="checkbox" checked={events.includes(item.id)} onChange={() => toggle(item.id)} className="mt-0.5 h-4 w-4 accent-[#3b82f6]" />
                <span><span className="block text-sm text-slate-100">{item.label}</span><span className="block text-xs text-slate-400">{item.hint}</span></span>
              </label>
            ))}
          </fieldset>
          <div className="flex flex-wrap gap-2 border-t border-base-border pt-2">
            {subscribed ? <>
              <button type="button" disabled={busy} onClick={() => void test()} className="inline-flex items-center gap-2 rounded-xl bg-accent-blue px-4 py-2.5 text-sm font-semibold text-white hover:bg-blue-500 disabled:opacity-50"><Send size={15} /> Kirim notifikasi uji</button>
              <button type="button" disabled={busy} onClick={() => void disable()} className="inline-flex items-center gap-2 rounded-xl border border-base-border px-4 py-2.5 text-sm text-slate-300 hover:bg-base-800 disabled:opacity-50"><BellOff size={15} /> Matikan di perangkat ini</button>
            </> : (
              <button type="button" disabled={busy || permission === "denied" || available.length === 0} onClick={() => void enable()} className="inline-flex items-center gap-2 rounded-xl bg-accent-blue px-4 py-2.5 text-sm font-semibold text-white hover:bg-blue-500 disabled:opacity-50"><BellRing size={15} /> {busy ? "Mengaktifkan…" : "Aktifkan notifikasi"}</button>
            )}
          </div>
          <p className="text-xs text-slate-500">{subscribed ? "Status: aktif di perangkat ini." : "Status: belum aktif di perangkat ini."} Di laptop/PC, browser perlu tetap berjalan di latar belakang; tab DevControl boleh ditutup.</p>
          {lastError && <p className="rounded-lg bg-amber-500/10 p-2 text-xs text-amber-200">Pengiriman terakhir gagal: {lastError}</p>}
        </>
      )}
      {error && <p role="alert" className="rounded-lg bg-red-500/10 p-2 text-sm text-red-300">{error}</p>}
      {message && <p role="status" className="rounded-lg bg-emerald-500/10 p-2 text-sm text-emerald-300">{message}</p>}
    </section>
  );
}

export function PushNotificationDialog({ onClose }: { onClose: () => void }) {
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);
  return createPortal(
    <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/75 p-2" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
      <div role="dialog" aria-modal="true" aria-label="Notifikasi perangkat" className="relative max-h-[92vh] w-full max-w-lg overflow-y-auto rounded-2xl border border-base-border bg-base-900 p-3 shadow-2xl">
        <button type="button" aria-label="Tutup" onClick={onClose} className="absolute right-2 top-2 rounded-lg p-1 text-slate-300 hover:bg-base-800"><X size={18} /></button>
        <PushNotificationSettings compact />
      </div>
    </div>,
    document.body,
  );
}
