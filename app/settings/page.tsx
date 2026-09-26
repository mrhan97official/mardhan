"use client";

import { useEffect, useRef, useState, type ChangeEvent } from "react";
import { Cloud, ImagePlus, RotateCcw, Save, Settings } from "lucide-react";
import AppShell from "@/components/AppShell";
import CloudflareZoneSettings from "@/components/CloudflareZoneSettings";
import AppsPromoSettings from "@/components/AppsPromoSettings";
import { useBranding } from "@/components/BrandingProvider";
import { notifyDataChanged } from "@/lib/liveUpdates";

type LogoBackground = "transparent" | "black" | "white";
type PreparedLogo = { icon192: Blob; icon512: Blob; maskable: Blob; preview: string; filename: string };

async function prepareLogo(file: File, background: LogoBackground): Promise<PreparedLogo> {
  if (file.type !== "image/png" && file.type !== "image/jpeg") throw new Error("Pilih gambar PNG atau JPG.");
  const source = URL.createObjectURL(file);
  try {
    const image = new Image();
    await new Promise<void>((resolve, reject) => {
      image.onload = () => resolve();
      image.onerror = () => reject(new Error("Gambar tidak dapat dibuka."));
      image.src = source;
    });
    if (!image.naturalWidth || !image.naturalHeight) throw new Error("Ukuran gambar tidak valid.");
    const render = (size: number, padding = 0) => new Promise<Blob>((resolve, reject) => {
      const canvas = document.createElement("canvas");
      canvas.width = canvas.height = size;
      const context = canvas.getContext("2d");
      if (!context) { reject(new Error("Perangkat tidak mendukung pembuatan ikon.")); return; }
      if (background !== "transparent") {
        context.fillStyle = background === "black" ? "#000000" : "#FFFFFF";
        context.fillRect(0, 0, size, size);
      }
      const available = size * (1 - 2 * padding);
      const scale = Math.min(available / image.naturalWidth, available / image.naturalHeight);
      const width = image.naturalWidth * scale;
      const height = image.naturalHeight * scale;
      context.drawImage(image, (size - width) / 2, (size - height) / 2, width, height);
      canvas.toBlob((blob) => blob ? resolve(blob) : reject(new Error("Gagal membuat ikon PNG.")), "image/png");
    });
    const [icon192, icon512, maskable] = await Promise.all([render(192), render(512), render(512, 0.13)]);
    return { icon192, icon512, maskable, preview: URL.createObjectURL(icon512), filename: file.name };
  } finally { URL.revokeObjectURL(source); }
}

export default function SettingsPage() {
  const { version, reload } = useBranding();
  const picker = useRef<HTMLInputElement>(null);
  const selectedFile = useRef<File | null>(null);
  const selection = useRef(0);
  const [prepared, setPrepared] = useState<PreparedLogo | null>(null);
  const [background, setBackground] = useState<LogoBackground>("transparent");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  useEffect(() => () => { if (prepared) URL.revokeObjectURL(prepared.preview); }, [prepared]);

  async function buildPreview(file: File, mode: LogoBackground) {
    const request = ++selection.current;
    setError("");
    setMessage("");
    setBusy(true);
    try {
      const next = await prepareLogo(file, mode);
      if (request !== selection.current) { URL.revokeObjectURL(next.preview); return; }
      setPrepared(next);
    } catch (reason) {
      if (request === selection.current) {
        selectedFile.current = null;
        setPrepared(null);
        setError(reason instanceof Error ? reason.message : "Gagal menyiapkan logo.");
      }
    } finally { if (request === selection.current) setBusy(false); }
  }

  async function chooseLogo(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file) return;
    selectedFile.current = file;
    await buildPreview(file, background);
  }

  function chooseBackground(mode: LogoBackground) {
    setBackground(mode);
    if (selectedFile.current) void buildPreview(selectedFile.current, mode);
  }

  async function saveLogo() {
    if (!prepared) return;
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const form = new FormData();
      form.append("192", prepared.icon192, "192.png");
      form.append("512", prepared.icon512, "512.png");
      form.append("maskable", prepared.maskable, "maskable.png");
      const response = await fetch("/api/branding", { method: "POST", body: form, credentials: "same-origin", cache: "no-store" });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || "Gagal menyimpan logo.");
      await reload();
      notifyDataChanged();
      setPrepared(null);
      selectedFile.current = null;
      setMessage("Logo tersimpan. Sidebar, ikon tab, dan ikon untuk pemasangan baru sudah diperbarui.");
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Gagal menyimpan logo."); }
    finally { setBusy(false); }
  }

  async function resetLogo() {
    if (!window.confirm("Kembalikan logo bawaan DevControl?")) return;
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const response = await fetch("/api/branding", { method: "DELETE", credentials: "same-origin", cache: "no-store" });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || "Gagal mengembalikan logo.");
      await reload();
      notifyDataChanged();
      setPrepared(null);
      selectedFile.current = null;
      setMessage("Logo bawaan DevControl sudah aktif kembali.");
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Gagal mengembalikan logo."); }
    finally { setBusy(false); }
  }

  return (
    <AppShell title="Settings" subtitle="Workspace and account preferences">
      <div className="flex items-center gap-3">
        <div className="rounded-xl bg-accent-blue/15 p-2.5 text-accent-blue"><Settings size={19} /></div>
        <div><h2 className="font-semibold text-white">Identitas aplikasi</h2><p className="text-sm text-slate-400">Atur logo DevControl untuk sidebar dan ikon aplikasi.</p></div>
      </div>

      <section className="card max-w-3xl space-y-5 p-5 sm:p-6" aria-labelledby="logo-title">
        <div>
          <h3 id="logo-title" className="text-lg font-semibold text-white">Logo aplikasi</h3>
          <p className="mt-1 text-sm text-slate-400">Unggah PNG atau JPG, lalu pilih latar ikon untuk browser dan PWA. Untuk latar bening, gunakan PNG transparan.</p>
        </div>
        <div role="group" aria-label="Latar belakang logo" className="flex flex-wrap items-center gap-2">
          {(["transparent", "black", "white"] as const).map((mode) => (
            <button key={mode} type="button" disabled={busy} aria-pressed={background === mode} onClick={() => chooseBackground(mode)} className={`flex items-center gap-2 rounded-xl border px-3 py-2 text-xs font-medium transition-colors disabled:opacity-50 ${background === mode ? "border-accent-blue bg-accent-blue/10 text-accent-blue" : "border-base-border text-slate-300 hover:bg-base-800"}`}>
              <span className={`h-4 w-4 rounded border border-base-border ${mode === "transparent" ? "logo-transparency-bg" : mode === "black" ? "bg-black" : "bg-[#ffffff]"}`} />
              {mode === "transparent" ? "Transparan" : mode === "black" ? "Hitam" : "Putih"}
            </button>
          ))}
        </div>
        <div className="flex flex-wrap items-center gap-6">
          <div className="space-y-2 text-center">
            <div className="logo-transparency-bg flex h-24 w-24 items-center justify-center overflow-hidden rounded-2xl border border-base-border">
              {version ? <img src={`/api/branding/icon?size=512&v=${version}`} alt="Logo aplikasi saat ini" className="h-full w-full object-contain" /> : <Cloud size={37} className="text-accent-blue" />}
            </div>
            <p className="text-xs text-slate-400">Saat ini</p>
          </div>
          {prepared && (
            <div className="space-y-2 text-center">
              <img src={prepared.preview} alt={`Pratinjau ${prepared.filename}`} className="logo-transparency-bg h-24 w-24 rounded-2xl border border-accent-blue/60 object-contain" />
              <p className="max-w-28 truncate text-xs text-slate-400">Pratinjau</p>
            </div>
          )}
          <div className="min-w-0 flex-1 space-y-3">
            <input ref={picker} type="file" accept="image/png,image/jpeg" onChange={(event) => void chooseLogo(event)} className="sr-only" aria-label="Pilih file logo" />
            <button type="button" disabled={busy} onClick={() => picker.current?.click()} className="inline-flex items-center gap-2 rounded-xl border border-base-border bg-base-800 px-4 py-2.5 text-sm font-medium text-slate-100 hover:bg-base-700 disabled:opacity-50">
              <ImagePlus size={16} /> {busy ? "Memproses…" : version ? "Pilih logo baru" : "Pilih logo"}
            </button>
            {prepared && <p className="truncate text-xs text-slate-400">{prepared.filename}</p>}
          </div>
        </div>
        <div className="flex flex-wrap gap-3 border-t border-base-border pt-4">
          <button type="button" disabled={!prepared || busy} onClick={() => void saveLogo()} className="inline-flex items-center gap-2 rounded-xl bg-accent-blue px-4 py-2.5 text-sm font-semibold text-white hover:bg-blue-500 disabled:cursor-not-allowed disabled:opacity-50">
            <Save size={16} /> Simpan logo
          </button>
          {version && <button type="button" disabled={busy} onClick={() => void resetLogo()} className="inline-flex items-center gap-2 rounded-xl border border-base-border px-4 py-2.5 text-sm text-slate-300 hover:bg-base-800 disabled:opacity-50"><RotateCcw size={16} /> Kembalikan bawaan</button>}
        </div>
        {error && <p role="alert" className="rounded-lg bg-red-500/10 p-3 text-sm text-red-300">{error}</p>}
        {message && <p role="status" className="rounded-lg bg-emerald-500/10 p-3 text-sm text-emerald-300">{message}</p>}
        <p className="text-xs text-slate-500">Untuk menghilangkan latar hitam pada logo lama, pilih lagi file PNG asli yang transparan sebelum menyimpan.</p>
      </section>

      <AppsPromoSettings />

      <CloudflareZoneSettings />

      <aside className="max-w-3xl rounded-xl border border-base-border bg-base-900/60 p-4 text-sm text-slate-300">
        <p className="font-medium text-white">Ikon aplikasi yang sudah dipasang</p>
        <p className="mt-1">Logo di dalam aplikasi berubah otomatis. Untuk mengganti ikon di Layar Utama iPad atau iPhone yang sudah terpasang, hapus ikon lama, buka alamat aplikasi di Safari, lalu pilih Bagikan → Tambahkan ke Layar Utama. Pembaruan ikon yang sudah terpasang pada perangkat lain bergantung pada browser; pasang ulang jika ikon lamanya masih terlihat.</p>
      </aside>
    </AppShell>
  );
}
