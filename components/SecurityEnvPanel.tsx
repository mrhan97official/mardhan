"use client";

import { useState } from "react";
import { CheckCircle2, Copy, Loader2, Search, ShieldCheck, Wand2 } from "lucide-react";

type Target = "production" | "preview" | "development";
type EnvLike = { id: string; key: string; type: string; target: string[] };
type SaveData = { key: string; value: string | null; target: Target[]; type: string };

type Item = {
  key: string;
  label: string;
  type: "sensitive" | "encrypted";
  help: string;
  kind: "input" | "chat" | "generate" | "fixed";
  placeholder?: string;
  fixed?: string;
  warning?: string;
};

// Security variables DevControl reads. They are listed here already, so the
// owner only fills in (or generates) the values.
const ITEMS: Item[] = [
  { key: "DEVCONTROL_TELEGRAM_BOT_TOKEN", label: "Token bot Telegram", type: "sensitive", kind: "input", placeholder: "123456789:AA…",
    help: "Di Telegram buka @BotFather → /newbot → ikuti langkahnya, lalu tempel token yang diberikan." },
  { key: "DEVCONTROL_TELEGRAM_CHAT_ID", label: "Chat ID Telegram", type: "encrypted", kind: "chat", placeholder: "mis. 123456789",
    help: "Buka bot Anda di Telegram, tekan Start atau kirim pesan apa saja, lalu tekan Deteksi otomatis." },
  { key: "DEVCONTROL_HEARTBEAT_SECRET", label: "Rahasia penjaga independen", type: "sensitive", kind: "generate",
    help: "Dipakai watchdog/worker.js di Cloudflare. Salin nilainya ke Worker sebelum menyimpan, karena nilai Sensitive tidak bisa dibaca ulang." },
  { key: "DEVCONTROL_REQUIRE_2FA", label: "Wajibkan 2FA owner", type: "encrypted", kind: "fixed", fixed: "1",
    help: "Login owner ditahan bila rahasia 2FA hilang dari database. Aktifkan hanya setelah 2FA owner menyala di Settings." },
  { key: "DEVCONTROL_SESSION_SECRET", label: "Rahasia sesi (acak)", type: "sensitive", kind: "generate",
    help: "Memisahkan kunci cookie dari kata sandi admin.",
    warning: "Setelah redeploy semua perangkat keluar; tekan Tandatangani ulang semua di Member & Akses, dan bila 2FA aktif atur ulang lewat DEVCONTROL_TOTP_RESET=1." },
];

function randomSecret() {
  const bytes = new Uint8Array(48);
  crypto.getRandomValues(bytes);
  return btoa(String.fromCharCode(...Array.from(bytes))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function targetsFor(type: string): Target[] {
  return type === "sensitive" ? ["production", "preview"] : ["production", "preview", "development"];
}

export default function SecurityEnvPanel({ envs, busy, onSave }: {
  envs: EnvLike[];
  busy: boolean;
  onSave: (existing: EnvLike | undefined, data: SaveData) => Promise<void> | void;
}) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [chats, setChats] = useState<{ id: string; name: string; type: string }[]>([]);
  const [detecting, setDetecting] = useState(false);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState<string | null>(null);
  const existing = (key: string) => envs.find((item) => item.key === key);
  const missing = ITEMS.filter((item) => !existing(item.key)).length;
  const set = (key: string, value: string) => setValues((current) => ({ ...current, [key]: value }));

  async function detect() {
    setDetecting(true);
    setError("");
    setChats([]);
    try {
      const response = await fetch("/api/security", {
        method: "POST", headers: { "Content-Type": "application/json" }, credentials: "same-origin", cache: "no-store",
        body: JSON.stringify({ action: "telegram_chats", token: (values.DEVCONTROL_TELEGRAM_BOT_TOKEN ?? "").trim() }),
      });
      const body = await response.json().catch(() => null);
      if (!response.ok) throw new Error(body?.error || `HTTP ${response.status}`);
      const list = body.chats ?? [];
      setChats(list);
      if (list.length === 1) set("DEVCONTROL_TELEGRAM_CHAT_ID", list[0].id);
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Chat ID tidak dapat dideteksi."); }
    finally { setDetecting(false); }
  }

  function save(item: Item) {
    const value = item.kind === "fixed" ? item.fixed ?? "" : (values[item.key] ?? "").trim();
    if (!value) { setError(`Isi nilai ${item.key} terlebih dahulu.`); return; }
    setError("");
    void Promise.resolve(onSave(existing(item.key), { key: item.key, value, target: targetsFor(item.type), type: item.type }))
      .then(() => set(item.key, ""));
  }

  function copy(key: string, value: string) {
    void navigator.clipboard?.writeText(value).then(() => { setCopied(key); window.setTimeout(() => setCopied(null), 1500); }).catch(() => {});
  }

  return (
    <div className="space-y-1.5 rounded-xl border border-accent-blue/40 bg-accent-blue/5 p-2">
      <p className="flex items-center gap-1.5 text-sm font-semibold text-white"><ShieldCheck size={15} className="text-accent-blue" /> Variabel keamanan DevControl</p>
      <p className="text-[11px] text-slate-400">{missing ? `${missing} variabel belum diisi.` : "Semua sudah diisi."} Isi nilainya di sini, simpan, lalu tekan Redeploy production agar aktif. Tombol Uji alarm ada di Pusat Keamanan.</p>
      {error && <p role="alert" className="rounded-lg bg-red-500/10 p-2 text-xs text-red-300">{error}</p>}
      {ITEMS.map((item) => {
        const present = existing(item.key);
        const value = values[item.key] ?? "";
        return (
          <div key={item.key} className="space-y-1 rounded-lg bg-base-900 p-2">
            <div className="flex flex-wrap items-center gap-1.5">
              <span className="text-xs font-semibold text-slate-100">{item.label}</span>
              <code className="text-[10px] text-slate-500">{item.key}</code>
              {present
                ? <span className="inline-flex items-center gap-1 rounded-full bg-emerald-400/15 px-1.5 py-0.5 text-[10px] font-semibold text-emerald-400"><CheckCircle2 size={10} /> Sudah diisi</span>
                : <span className="rounded-full bg-amber-400/15 px-1.5 py-0.5 text-[10px] font-semibold text-amber-300">Belum diisi</span>}
            </div>
            <p className="text-[11px] text-slate-400">{item.help}</p>
            {item.warning && <p className="text-[11px] text-amber-300">{item.warning}</p>}
            <div className="flex flex-wrap items-center gap-1.5">
              {item.kind === "fixed" ? (
                <span className="rounded-lg border border-base-border px-2 py-1.5 font-mono text-xs text-slate-300">= {item.fixed}</span>
              ) : (
                <input value={value} onChange={(event) => set(item.key, event.target.value)} placeholder={present ? "Isi untuk mengganti nilai" : item.placeholder ?? "Nilai"}
                  type={item.kind === "chat" ? "text" : "password"} autoComplete="off" spellCheck={false}
                  className="min-w-0 flex-1 rounded-lg border border-base-border bg-base-850 px-2 py-1.5 font-mono text-xs text-slate-100" />
              )}
              {item.kind === "generate" && (
                <button type="button" onClick={() => set(item.key, randomSecret())} className="inline-flex items-center gap-1 rounded-lg border border-base-border px-2 py-1.5 text-xs text-slate-200 hover:bg-base-800"><Wand2 size={12} /> Buat acak</button>
              )}
              {item.kind === "generate" && value && (
                <button type="button" onClick={() => copy(item.key, value)} className="inline-flex items-center gap-1 rounded-lg border border-base-border px-2 py-1.5 text-xs text-slate-200 hover:bg-base-800"><Copy size={12} /> {copied === item.key ? "Tersalin" : "Salin"}</button>
              )}
              {item.kind === "chat" && (
                <button type="button" disabled={detecting} onClick={() => void detect()} title="Membaca pesan terbaru ke bot memakai token bot yang Anda ketik di atas"
                  className="inline-flex items-center gap-1 rounded-lg border border-base-border px-2 py-1.5 text-xs text-slate-200 hover:bg-base-800 disabled:opacity-50">
                  {detecting ? <Loader2 size={12} className="animate-spin" /> : <Search size={12} />} Deteksi otomatis
                </button>
              )}
              <button type="button" disabled={busy || (item.kind !== "fixed" && !value.trim())} onClick={() => save(item)}
                className="rounded-lg bg-accent-blue px-2.5 py-1.5 text-xs font-semibold text-white disabled:opacity-50">{present ? "Ganti" : "Simpan"}</button>
            </div>
            {item.kind === "chat" && !values.DEVCONTROL_TELEGRAM_BOT_TOKEN && <p className="text-[11px] text-slate-500">Untuk deteksi otomatis, ketik juga token bot di baris atas (tidak perlu disimpan ulang).</p>}
            {item.kind === "chat" && chats.length > 1 && (
              <div className="flex flex-wrap gap-1.5">
                {chats.map((chat) => (
                  <button key={chat.id} type="button" onClick={() => set(item.key, chat.id)} className={`rounded-lg border px-2 py-1 text-[11px] ${value === chat.id ? "border-accent-blue text-white" : "border-base-border text-slate-300"}`}>
                    {chat.name || chat.id} <span className="text-slate-500">({chat.type})</span>
                  </button>
                ))}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
