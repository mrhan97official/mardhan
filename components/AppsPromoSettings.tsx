"use client";

import { useEffect, useState, type FormEvent } from "react";
import { ImagePlus, Pencil, Trash2 } from "lucide-react";
import { notifyDataChanged } from "@/lib/liveUpdates";
import { useOfflineData } from "@/lib/useOfflineData";

interface Promotion {
  repo: string;
  app_name: string;
  app_url: string;
  title: string;
  description: string;
  image_repo: string;
  version: string;
}

interface PreparedImage {
  blob: Blob;
  preview: string;
  originalBytes: number;
  originalWidth: number;
  originalHeight: number;
  optimized: boolean;
}

const WIDTH = 1440;
const HEIGHT = 450;
const MAX_INPUT_BYTES = 30 * 1024 * 1024;
const IMAGE_PREFIX = "__devcontrol__/banner-";

function formatSize(bytes: number) {
  return `${(bytes / 1024 / 1024).toLocaleString("id-ID", { maximumFractionDigits: 2 })} MB`;
}

async function prepareImage(file: File): Promise<PreparedImage> {
  if (!["image/jpeg", "image/png", "image/webp"].includes(file.type) || !file.size || file.size > MAX_INPUT_BYTES) {
    throw new Error("Pilih JPG, PNG, atau WebP berukuran maksimal 30 MB.");
  }
  const source = URL.createObjectURL(file);
  try {
    const image = new Image();
    image.src = source;
    await image.decode();
    const originalWidth = image.naturalWidth;
    const originalHeight = image.naturalHeight;
    if (!originalWidth || !originalHeight) throw new Error("Gambar tidak dapat dibaca.");

    const canvas = document.createElement("canvas");
    canvas.width = WIDTH;
    canvas.height = HEIGHT;
    const context = canvas.getContext("2d");
    if (!context) throw new Error("Perangkat tidak dapat mengolah gambar banner.");
    // Keep the central composition, never stretch a source with another ratio.
    const cropWidth = Math.min(originalWidth, originalHeight * WIDTH / HEIGHT);
    const cropHeight = Math.min(originalHeight, originalWidth * HEIGHT / WIDTH);
    context.fillStyle = "#ffffff";
    context.fillRect(0, 0, WIDTH, HEIGHT);
    context.drawImage(image, (originalWidth - cropWidth) / 2, (originalHeight - cropHeight) / 2,
      cropWidth, cropHeight, 0, 0, WIDTH, HEIGHT);
    const jpeg = await new Promise<Blob>((resolve, reject) => canvas.toBlob(
      (result) => result ? resolve(result) : reject(new Error("Gambar gagal dikompres.")),
      "image/jpeg", 0.84,
    ));
    if (jpeg.type !== "image/jpeg" || !jpeg.size) throw new Error("Format hasil kompresi tidak didukung.");
    const keepOriginal = originalWidth === WIDTH && originalHeight === HEIGHT &&
      file.type !== "image/webp" && file.size <= jpeg.size;
    const output = keepOriginal ? file : jpeg;
    return { blob: output, preview: URL.createObjectURL(output), originalBytes: file.size,
      originalWidth, originalHeight, optimized: !keepOriginal };
  } finally {
    URL.revokeObjectURL(source);
  }
}

async function apiRequest(path: string, init?: RequestInit) {
  const response = await fetch(path, { cache: "no-store", credentials: "same-origin", ...init });
  const body = await response.json().catch(() => null);
  if (!response.ok) throw new Error(body?.error || `Permintaan gagal (HTTP ${response.status}).`);
  return body;
}

export default function AppsPromoSettings() {
  const promotion = useOfflineData<Promotion | null>("app-promo-v1", "/api/app-promo", null, 10000);
  const [editing, setEditing] = useState(false);
  const [appName, setAppName] = useState("");
  const [appURL, setAppURL] = useState("");
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [prepared, setPrepared] = useState<PreparedImage | null>(null);
  const [preparing, setPreparing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => () => { if (prepared) URL.revokeObjectURL(prepared.preview); }, [prepared]);

  function openEditor() {
    const current = promotion.data;
    setAppName(current?.app_name || (current?.repo ? current.repo.split("/").pop() || "" : ""));
    setAppURL(current?.app_url || "");
    setTitle(current?.title || "");
    setDescription(current?.description || "");
    setPrepared(null);
    setError("");
    setEditing(true);
  }

  async function chooseImage(file?: File) {
    if (!file) return;
    setPreparing(true);
    setError("");
    try { setPrepared(await prepareImage(file)); }
    catch (cause) { setPrepared(null); setError(cause instanceof Error ? cause.message : "Gagal mengolah gambar."); }
    finally { setPreparing(false); }
  }

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!prepared && !promotion.data) { setError("Pilih gambar banner terlebih dahulu."); return; }
    setBusy(true);
    setError("");
    let stagedRepo: string | null = null;
    let uploadID: string | null = null;
    let finished = false;
    let committed = false;
    try {
      let imageRepo = promotion.data?.image_repo || "";
      let version = promotion.data?.version || "";
      if (prepared) {
        stagedRepo = `${IMAGE_PREFIX}${crypto.randomUUID().replace(/-/g, "")}`;
        const ticket = await apiRequest("/api/project-thumbnails", {
          method: "POST", headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ action: "begin", repo: stagedRepo, content_type: prepared.blob.type, size_bytes: prepared.blob.size }),
        });
        uploadID = ticket.upload_id;
        if (!uploadID || !ticket.upload_url) throw new Error("Tiket unggahan banner tidak valid.");
        const upload = await fetch(ticket.upload_url, {
          method: "PUT", headers: { "Content-Type": prepared.blob.type }, body: prepared.blob,
        });
        if (!upload.ok) throw new Error(`R2 menolak unggahan banner (HTTP ${upload.status}). Periksa izin dan CORS bucket.`);
        const result = await apiRequest("/api/project-thumbnails", {
          method: "POST", headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ action: "finish", upload_id: uploadID }),
        });
        finished = true;
        imageRepo = stagedRepo;
        version = result.version;
      }
      await apiRequest("/api/app-promo", {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ app_name: appName.trim(), app_url: appURL.trim(),
          title: title.trim(), description: description.trim(), image_repo: imageRepo, version }),
      });
      committed = true;
      setEditing(false);
      setPrepared(null);
      promotion.reload();
      notifyDataChanged();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Banner gagal disimpan.");
    } finally {
      if (stagedRepo && !committed) {
        const cleanup = finished ? `repo=${encodeURIComponent(stagedRepo)}`
          : uploadID ? `upload_id=${encodeURIComponent(uploadID)}` : null;
        if (cleanup) await fetch(`/api/project-thumbnails?${cleanup}`, { method: "DELETE", credentials: "same-origin" }).catch(() => {});
      }
      setBusy(false);
    }
  }

  async function remove() {
    if (!promotion.data || busy || !window.confirm("Hapus banner promosi ini?")) return;
    setBusy(true);
    setError("");
    try {
      await apiRequest("/api/app-promo", { method: "DELETE" });
      setEditing(false);
      setPrepared(null);
      promotion.reload();
      notifyDataChanged();
    } catch (cause) { setError(cause instanceof Error ? cause.message : "Banner gagal dihapus."); }
    finally { setBusy(false); }
  }

  const imageURL = promotion.data
    ? `/api/project-thumbnails?repo=${encodeURIComponent(promotion.data.image_repo)}&v=${encodeURIComponent(promotion.data.version)}`
    : "";

  return (
    <section id="promo-banner" className="card max-w-3xl scroll-mt-6 space-y-2 p-2" aria-labelledby="promo-title">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <h3 id="promo-title" className="text-lg font-semibold text-white">Banner halaman Aplikasi</h3>
          <p className="mt-1 text-sm text-slate-400">Gambar ditampilkan bersih dengan rasio 1440 × 450. Nama aplikasi, tautan, judul, dan deskripsi boleh kosong untuk banner umum.</p>
        </div>
        <button type="button" onClick={() => editing ? setEditing(false) : openEditor()} disabled={busy || preparing || promotion.loading} className="inline-flex items-center gap-2 rounded-lg border border-base-border bg-base-800 px-3 py-2 text-xs font-medium text-white hover:bg-base-700 disabled:opacity-50">
          <Pencil size={14} /> {editing ? "Tutup pengaturan" : promotion.data ? "Ubah banner" : "Buat banner"}
        </button>
      </div>
      {promotion.data && (
        <div className="overflow-hidden rounded-xl border border-base-border bg-white">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src={imageURL} alt="Banner saat ini" className="aspect-[16/5] w-full object-cover" />
        </div>
      )}
      {promotion.error && <p role="alert" className="text-xs text-amber-300">Banner belum dapat dimuat: {promotion.error}</p>}
      {editing && <form onSubmit={(event) => void save(event)} className="space-y-2 border-t border-base-border pt-2">
        <div className="grid gap-2 md:grid-cols-2">
          <label className="block text-xs font-medium text-slate-300">Nama aplikasi yang dipromosikan (opsional)
            <input maxLength={100} value={appName} disabled={busy} onChange={(event) => setAppName(event.target.value)} placeholder="Contoh: DevControl" className="mt-1.5 w-full rounded-lg border border-base-border bg-base-900 px-3 py-2.5 text-sm text-white" />
          </label>
          <label className="block text-xs font-medium text-slate-300">Tautan aplikasi (opsional)
            <input type="url" maxLength={500} value={appURL} disabled={busy} onChange={(event) => setAppURL(event.target.value)} placeholder="https://contoh.com" className="mt-1.5 w-full rounded-lg border border-base-border bg-base-900 px-3 py-2.5 text-sm text-white" />
          </label>
        </div>
        <label className="block text-xs font-medium text-slate-300">Judul banner (opsional)
          <input maxLength={80} value={title} disabled={busy} onChange={(event) => setTitle(event.target.value)} placeholder="Kosongkan untuk banner tanpa judul" className="mt-1.5 w-full rounded-lg border border-base-border bg-base-900 px-3 py-2.5 text-sm text-white" />
        </label>
        <label className="block text-xs font-medium text-slate-300">Deskripsi singkat (opsional)
          <textarea maxLength={220} rows={2} value={description} disabled={busy} onChange={(event) => setDescription(event.target.value)} placeholder="Kosongkan jika pesan sudah ada pada gambar" className="mt-1.5 w-full resize-y rounded-lg border border-base-border bg-base-900 px-3 py-2.5 text-sm text-white" />
        </label>
        <label className="inline-flex cursor-pointer items-center gap-2 rounded-lg border border-base-border bg-base-800 px-2 py-2 text-xs font-medium text-slate-200 hover:bg-base-700">
          <ImagePlus size={15} /> {prepared ? "Ganti gambar pilihan" : promotion.data ? "Ganti gambar banner" : "Pilih gambar banner"}
          <input type="file" accept="image/jpeg,image/png,image/webp" disabled={busy || preparing} className="sr-only" onChange={(event) => { void chooseImage(event.target.files?.[0]); event.target.value = ""; }} />
        </label>
        {preparing && <p role="status" className="text-xs text-slate-400">Mengolah dan mengompres gambar…</p>}
        {prepared && <div className="flex flex-wrap items-center gap-2">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src={prepared.preview} alt="Pratinjau gambar banner" className="aspect-[16/5] w-48 rounded-lg border border-base-border bg-white object-cover" />
          <div className="text-xs text-slate-400">
            <p>{prepared.originalWidth} × {prepared.originalHeight} → 1440 × 450 piksel</p>
            <p>{formatSize(prepared.originalBytes)} → {formatSize(prepared.blob.size)}{!prepared.optimized && " · berkas asli lebih ringan"}</p>
            {prepared.originalWidth / prepared.originalHeight !== WIDTH / HEIGHT && <p className="mt-1 text-amber-300">Gambar dipotong di bagian tengah agar sesuai rasio banner.</p>}
            {(prepared.originalWidth < WIDTH || prepared.originalHeight < HEIGHT) && <p className="mt-1 text-amber-300">Gambar kecil mungkin terlihat kurang tajam setelah diperbesar.</p>}
          </div>
        </div>}
        {error && <p role="alert" className="text-xs text-red-300">{error}</p>}
        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-base-border pt-2">
          <p className="text-[11px] text-slate-500">Gunakan gambar 1440 × 450. JPG, PNG, atau WebP hingga 30 MB; gambar lain dipotong tengah dan hasilnya dikompres bila lebih ringan.</p>
          <div className="flex gap-2">
            {promotion.data && <button type="button" disabled={busy || preparing} onClick={() => void remove()} className="inline-flex items-center gap-1.5 rounded-lg border border-red-500/30 px-3 py-2 text-xs font-medium text-red-300 disabled:opacity-50"><Trash2 size={14} /> Hapus</button>}
            <button type="submit" disabled={busy || preparing || (!prepared && !promotion.data)} className="rounded-lg bg-accent-blue px-4 py-2 text-xs font-semibold text-white disabled:opacity-50">{busy ? "Menyimpan…" : "Simpan banner"}</button>
          </div>
        </div>
      </form>}
      {!editing && error && <p role="alert" className="text-xs text-red-300">{error}</p>}
    </section>
  );
}
