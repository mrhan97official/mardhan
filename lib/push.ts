// Web Push on this device: notifications arrive even when DevControl is
// closed. The server keeps one subscription per device (browser endpoint).

export type PushEvent = "deploy" | "self_update" | "confirm" | "app404";

export const PUSH_EVENTS: { id: PushEvent; label: string; hint: string }[] = [
  { id: "deploy", label: "Deploy aplikasi", hint: "Aplikasi Baru dan Update Aplikasi berhasil, gagal, atau terhenti." },
  { id: "self_update", label: "Update diri", hint: "Update DevControl selesai, gagal, atau terhenti." },
  { id: "confirm", label: "Konfirmasi menunggu", hint: "Ada pilihan yang perlu dijawab admin." },
  { id: "app404", label: "Aplikasi 404", hint: "Tautan aplikasi menampilkan 404 Vercel (diperiksa tiap 15 menit)." },
];

export type PushSupport = "ok" | "unsupported" | "ios-install";

const eventsKey = "devcontrol-push-events";
const syncedKey = "devcontrol-push-synced";

// Guards the automatic permission prompt so it runs at most once per loaded
// page (React effects can fire twice in development/StrictMode).
let autoPrompted = false;

function isIOS() {
  return /iPad|iPhone|iPod/.test(navigator.userAgent) || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
}

function isStandalone() {
  return window.matchMedia?.("(display-mode: standalone)").matches || (navigator as Navigator & { standalone?: boolean }).standalone === true;
}

export function pushSupport(): PushSupport {
  if (typeof window === "undefined") return "unsupported";
  const supported = "serviceWorker" in navigator && "PushManager" in window && "Notification" in window;
  if (isIOS() && !isStandalone()) return "ios-install";
  return supported ? "ok" : "unsupported";
}

function keyBytes(base64: string) {
  const padded = (base64 + "=".repeat((4 - (base64.length % 4)) % 4)).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(padded);
  const bytes = new Uint8Array(raw.length);
  for (let index = 0; index < raw.length; index += 1) bytes[index] = raw.charCodeAt(index);
  return bytes;
}

function sameKey(buffer: ArrayBuffer | null | undefined, expected: Uint8Array) {
  if (!buffer) return false;
  const actual = new Uint8Array(buffer);
  return actual.length === expected.length && actual.every((value, index) => value === expected[index]);
}

const PUSH_WORKER = "/push-sw.js";
const PUSH_SCOPE = "/push/";
let updateChecked = false;

function errorText(cause: unknown) {
  return cause instanceof Error ? cause.message : String(cause);
}

// The push worker is our own small file with its own scope, independent of
// the next-pwa service worker. We register it directly and wait for exactly
// this registration, so a slow or broken PWA worker can never block push.
async function registration(): Promise<ServiceWorkerRegistration> {
  let reg = await navigator.serviceWorker.getRegistration(PUSH_SCOPE);
  // getRegistration returns the closest matching scope, which may be the PWA
  // worker at "/"; only a registration scoped to /push/ is ours.
  if (!reg || !new URL(reg.scope).pathname.startsWith(PUSH_SCOPE)) {
    try {
      reg = await navigator.serviceWorker.register(PUSH_WORKER, { scope: PUSH_SCOPE, updateViaCache: "none" });
    } catch (cause) {
      throw new Error(`Service worker notifikasi gagal dipasang: ${errorText(cause)}`);
    }
  } else if (!updateChecked) {
    updateChecked = true;
    void reg.update().catch(() => undefined);
  }
  if (reg.active) return reg;
  const worker = reg.installing ?? reg.waiting;
  if (!worker) throw new Error("Service worker notifikasi belum terpasang. Muat ulang halaman lalu coba lagi.");
  await new Promise<void>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`Service worker notifikasi belum aktif (status: ${worker.state}). Muat ulang halaman lalu coba lagi.`)), 20000);
    const check = () => {
      if (worker.state === "activated") { clearTimeout(timer); resolve(); }
      if (worker.state === "redundant") { clearTimeout(timer); reject(new Error("Service worker notifikasi gagal dipasang oleh browser. Muat ulang halaman lalu coba lagi.")); }
    };
    worker.addEventListener("statechange", check);
    check();
  });
  return reg;
}

async function api<T>(body: unknown, method = "POST"): Promise<T> {
  const response = await fetch("/api/push", {
    method, credentials: "same-origin", cache: "no-store",
    headers: method === "POST" ? { "Content-Type": "application/json" } : undefined,
    body: method === "POST" ? JSON.stringify(body) : undefined,
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error((data as { error?: string }).error || `HTTP ${response.status}`);
  return data as T;
}

let cachedInfo: { public_key: string; events: PushEvent[] } | null = null;

export async function pushInfo() {
  cachedInfo = await api<{ public_key: string; events: PushEvent[] }>(undefined, "GET");
  return cachedInfo;
}

// Prepares everything a subscription needs before the person taps, so the
// tap only asks permission and subscribes. Safari requires the permission
// prompt to follow the tap closely; slow network work beforehand breaks it.
export async function warmPush() {
  if (pushSupport() !== "ok") return;
  await Promise.all([cachedInfo ? Promise.resolve(cachedInfo) : pushInfo(), registration()]);
}

export async function currentSubscription() {
  if (pushSupport() !== "ok") return null;
  return (await registration()).pushManager.getSubscription();
}

function storeEvents(events: PushEvent[] | null) {
  try {
    if (events) localStorage.setItem(eventsKey, JSON.stringify(events)); else localStorage.removeItem(eventsKey);
  } catch { /* storage may be disabled */ }
}

// Asks permission (only from a tap), subscribes and saves the chosen events.
export async function enablePush(events: PushEvent[]) {
  if (pushSupport() !== "ok") throw new Error("Browser ini belum mendukung notifikasi push.");
  const permission = await Notification.requestPermission();
  if (permission !== "granted") throw new Error("Izin notifikasi ditolak. Izinkan notifikasi untuk situs ini di pengaturan browser.");
  return subscribeWithPermission(events);
}

async function subscribeWithPermission(events: PushEvent[]) {
  const info = cachedInfo ?? await pushInfo();
  const applicationServerKey = keyBytes(info.public_key);
  const reg = await registration();
  let subscription = await reg.pushManager.getSubscription();
  if (subscription && !sameKey(subscription.options.applicationServerKey, applicationServerKey)) {
    await subscription.unsubscribe().catch(() => false);
    subscription = null;
  }
  subscription ??= await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey });
  const json = subscription.toJSON();
  const result = await api<{ events: PushEvent[] }>({ action: "subscribe", endpoint: json.endpoint, keys: json.keys, events });
  storeEvents(result.events);
  return result.events;
}

export async function disablePush() {
  storeEvents(null);
  const subscription = await currentSubscription().catch(() => null);
  if (!subscription) return;
  await api({ action: "unsubscribe", endpoint: subscription.endpoint }).catch(() => undefined);
  await subscription.unsubscribe().catch(() => false);
}

export async function pushStatus() {
  const subscription = await currentSubscription();
  if (!subscription) return { subscribed: false as const };
  return api<{ subscribed: boolean; events?: PushEvent[]; last_error?: string }>({ action: "status", endpoint: subscription.endpoint });
}

export async function sendTestPush() {
  const subscription = await currentSubscription();
  if (!subscription) throw new Error("Perangkat ini belum mengaktifkan notifikasi.");
  await api({ action: "test", endpoint: subscription.endpoint });
}

// Requests notification permission as soon as the app is opened, without
// waiting for the person to open Settings. Only runs while permission is
// still "default" (not yet answered), so it never re-prompts someone who
// already allowed or blocked it, and never fights with the manual button.
//
// Safari (desktop and installed iOS/iPadOS) ignores a permission request
// that is not triggered by a click, so on those browsers this silently does
// nothing and the manual "Aktifkan notifikasi" button remains the way in.
export async function autoPromptPush() {
  if (typeof window === "undefined" || autoPrompted) return;
  if (pushSupport() !== "ok" || typeof Notification === "undefined" || Notification.permission !== "default") return;
  autoPrompted = true;
  try {
    const info = await pushInfo();
    await enablePush(info.events);
  } catch { /* dismissed, denied, or Safari's gesture requirement; the manual button still works */ }
}

// Once per session: re-register this device if the browser rotated its push
// address, so notifications keep arriving without the user doing anything.
export async function syncPush() {
  try {
    if (pushSupport() !== "ok" || Notification.permission !== "granted") return;
    if (sessionStorage.getItem(syncedKey)) return;
    const saved = localStorage.getItem(eventsKey);
    if (!saved) return;
    sessionStorage.setItem(syncedKey, "1");
    await subscribeWithPermission(JSON.parse(saved) as PushEvent[]);
  } catch { /* best effort; the settings panel shows details */ }
}
