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

async function registration(): Promise<ServiceWorkerRegistration> {
  const ready = navigator.serviceWorker.ready;
  const timeout = new Promise<never>((_, reject) => setTimeout(() => reject(new Error("Service worker belum aktif. Muat ulang halaman lalu coba lagi.")), 10000));
  return Promise.race([ready, timeout]);
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

export async function pushInfo() {
  return api<{ public_key: string; events: PushEvent[] }>(undefined, "GET");
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
  const info = await pushInfo();
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
