"use client";

import { useEffect, useState, type FormEvent } from "react";
import { ExternalLink, ImagePlus, Pencil, Trash2 } from "lucide-react";
import type { ProjectEntry } from "@/lib/projectRepos";
import { useOfflineData } from "@/lib/useOfflineData";
import { notifyDataChanged } from "@/lib/liveUpdates";

interface Promotion {
  repo: string;
  title: string;
  description: string;
  image_repo: string;
  version: string;
}
interface PreparedImage {
  blob: Blob;
  preview: string;
  originalBytes: number;
  width: number;
  height: number;
  optimized: boolean;
}

const MAX_INPUT_BYTES = 30 * 1024 * 1024;
const IMAGE_PREFIX = "__devcontrol__/banner-";

function formatSize(bytes: number) {
  return `${(bytes / 1024 / 1024).toLocaleString("id-ID", { maximumFractionDigits: 2 })} MB`;
}

async function optimizeImage(file: File): Promise<PreparedImage> {
  if (!["image/jpeg", "image/png", "image/webp"].includes(file.type) || !file.size || file.size > MAX_INPUT_BYTES) {
    throw new Error("Pilih JPG, PNG, atau WebP berukuran maksimal 30 MB.");
  }
  const source = URL.createObjectURL(file);
  try {
    const image = new Image();
    image.src = source;
    await image.decode();
    if (!image.naturalWidth || !image.naturalHeight) throw new Error("Gambar tidak dapat dibaca.");
    const scale = Math.min(1, 1920 / image.naturalWidth, 720 / image.naturalHeight);
    const width = Math.max(1, Math.round(image.naturalWidth * scale));
    const height = Math.max(1, Math.round(image.naturalHeight * scale));
    const canvas = document.createElement("canvas");
    canvas.width = width;
    canvas.height = height;
    const context = canvas.getContext("2d");
    if (!context) throw new Error("Perangkat tidak dapat mengolah gambar banner.");
    context.fillStyle = "#0b1220";
    context.fillRect(0, 0, width, height);
    context.drawImage(image, 0, 0, width, height);
    const blob = await new Promise<Blob>((resolve, reject) => canvas.toBlob(
      (result) => result ? resolve(result) : reject(new Error("Gambar gagal dikompres.")),
      "image/jpeg", 0.82,
    ));
    if (blob.type !== "image/jpeg" || !blob.size) throw new Error("Format hasil kompresi tidak didukung.");
    const useOriginal = scale === 1 && file.type !== "image/webp" && blob.size >= file.size;
    const output = useOriginal ? file : blob;
    return { blob: output, preview: URL.createObjectURL(output), originalBytes: file.size, width, height, optimized: !useOriginal };
  } finally {
    URL.revokeObjectURL(source);
  }
}

async function apiRequest(url: string, init?: RequestInit) {
  const response = await fetch(url, { cache: "no-store", credentials: "same-origin", ...init });
  const body = await response.json().catch(() => null);
  if (!response.ok) throw new Error(body?.error || `Permintaan gagal (HTTP ${response.status}).`);
  return body;
}

export default function AppsPromoBanner({ apps }: { apps: ProjectEntry[] }) {
  const promotion = useOfflineData<Promotion | null>("app-promo-v1", "/api/app-promo", null, 10000);
  const [editing, setEditing] = useState(false);
  const [repo, setRepo] = useState("");
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [prepared, setPrepared] = useState<PreparedImage | null>(null);
  const [preparing, setPreparing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [imageFailed, setImageFailed] = useState(false);

  useEffect(() => () => { if (prepared) URL.revokeObjectURL(prepared.preview); }, [prepared]);
  useEffect(() => { setImageFailed(false); }, [promotion.data?.image_repo, promotion.data?.version]);

  const selected = promotion.data && apps.find((item) => item.repo.full_name.toLowerCase() === promotion.data?.repo.toLowerCase());
  const appURL = selected?.service?.app_url || selected?.repo.html_url;
  const imageURL = promotion.data
    ? `/api/project-thumbnails?repo=${encodeURIComponent(promotion.data.image_repo)}&v=${encodeURIComponent(promotion.data.version)}`
    : "";

  function openEditor() {
    setRepo(promotion.data?.repo || "");
    setTitle(promotion.data?.title || "");
    setDescription(promotion.data?.description || "");
    setPrepared(null);
    setError("");
    setEditing(true);
  }

  async function chooseImage(file?: File) {
    if (!file) return;
    setPreparing(true);
    setError("");
    try { setPrepared(await optimizeImage(file)); }
    catch (cause) { setPrepared(null); setError(cause instanceof Error ? cause.message : "Gagal mengolah gambar."); }
    finally { setPreparing(false); }
  }

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!apps.some((item) => item.repo.full_name === repo)) { setError("Pilih aplikasi dari daftar."); return; }
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
        body: JSON.stringify({ repo, title: title.trim(), description: description.trim(), image_repo: imageRepo, version }),
      });
      committed = true;
      setEditing(false);
      setPrepared(null);
      notifyDataChanged();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Banner gagal disimpan.");
    } finally {
      if (stagedRepo && !committed) {
        const cleanup = finished
          ? `repo=${encodeURIComponent(stagedRepo)}`
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
      notifyDataChanged();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Banner gagal dihapus.");
    } finally { setBusy(false); }
  }

  return (
    <div className="space-y-3">
      <section className="relative isolate overflow-hidden rounded-2xl border border-accent-blue/20 bg-[#0b1220] shadow-lg">
        {promotion.data && !imageFailed && (
          // The stored JPEG is already optimized. CSS cropping does not alter the uploaded file.
          // eslint-disable-next-line @next/next/no-img-element
          <img key={imageURL} src={imageURL} alt="" aria-hidden="true" className="absolute inset-0 h-full w-full object-cover" onError={() => setImageFailed(true)} />
        )}
        <div className="absolute inset-0 bg-gradient-to-r from-[#08111e]/95 via-[#08111e]/75 to-[#08111e]/20" aria-hidden="true" />
        <div className="relative flex min-h-[210px] flex-col justify-between gap-6 p-5 sm:p-7">
          <div className="flex items-start justify-between gap-3">
            <span className="rounded-full border border-white/20 bg-black/30 px-3 py-1 text-[11px] font-bold uppercase tracking-widest text-[#dbeafe]">Aplikasi unggulan</span>
            <button type="button" onClick={() => editing ? setEditing(false) : openEditor()} disabled={busy || preparing} className="inline-flex shrink-0 items-center gap-1.5 rounded-lg border border-white/25 bg-black/35 px-3 py-2 text-xs font-semibold text-[#fff] hover:bg-black/60 disabled:opacity-50">
              <Pencil size={13} /> {editing ? "Tutup editor" : "Atur banner"}
            </button>
          </div>
          <div className="max-w-2xl">
            <h2 className="text-xl font-bold text-[#fff] sm:text-2xl">{promotion.data?.title || "Promosikan aplikasi Anda di sini"}</h2>
            <p className="mt-2 text-sm leading-relaxed text-[#e2e8f0]">{promotion.data?.description || "Pilih aplikasi, tulis pesan promosi, lalu unggah gambar banner dari perangkat Anda."}</p>
            {appURL && <a href={appURL} target="_blank" rel="noopener noreferrer" className="mt-4 inline-flex items-center gap-2 rounded-lg bg-accent-blue px-4 py-2 text-sm font-semibold text-[#fff] hover:brightness-110">
              {selected?.service?.app_url ? "Buka aplikasi" : "Lihat repository"} <ExternalLink size={15} />
            </a>}
          </div>
        </div>
      </section>

      {promotion.error && <p role="alert" className="text-xs text-amber-300">Banner belum dapat diperbarui: {promotion.error}</p>}
      {editing && <form onSubmit={(event) => void save(event)} className="card space-y-4 p-4 sm:p-5">
        <div>
          <h3 className="text-base font-bold text-white">Atur promosi aplikasi</h3>
          <p className="mt-1 text-xs text-slate-400">Tersimpan untuk semua perangkat. Gambar otomatis diperkecil hingga maksimal 1920 × 720 piksel dan dikompres JPEG bila hasilnya lebih ringan.</p>
        </div>
        <div className="grid gap-4 md:grid-cols-2">
          <label className="block text-xs font-medium text-slate-300">Aplikasi yang dipromosikan
            <select required value={repo} disabled={busy} onChange={(event) => {
              setRepo(event.target.value);
              if (!title) setTitle(apps.find((item) => item.repo.full_name === event.target.value)?.repo.name || "");
            }} className="mt-1.5 w-full rounded-lg border border-base-border bg-base-900 px-3 py-2.5 text-sm text-white">
              <option value="">Pilih aplikasi</option>
              {repo && !apps.some((item) => item.repo.full_name === repo) && <option value={repo}>{repo} (tidak tersedia)</option>}
              {apps.map((item) => <option key={item.repo.full_name} value={item.repo.full_name}>{item.repo.full_name}</option>)}
            </select>
          </label>
          <label className="block text-xs font-medium text-slate-300">Judul banner
            <input required maxLength={80} value={title} disabled={busy} onChange={(event) => setTitle(event.target.value)} placeholder="Contoh: Coba aplikasi terbaru kami" className="mt-1.5 w-full rounded-lg border border-base-border bg-base-900 px-3 py-2.5 text-sm text-white" />
          </label>
        </div>
        <label className="block text-xs font-medium text-slate-300">Deskripsi singkat
          <textarea maxLength={220} rows={2} value={description} disabled={busy} onChange={(event) => setDescription(event.target.value)} placeholder="Jelaskan manfaat utama aplikasi..." className="mt-1.5 w-full resize-y rounded-lg border border-base-border bg-base-900 px-3 py-2.5 text-sm text-white" />
        </label>
        <label className="inline-flex cursor-pointer items-center gap-2 rounded-lg border border-base-border bg-base-800 px-3 py-2 text-xs font-medium text-slate-200 hover:bg-base-700">
          <ImagePlus size={15} /> {prepared ? "Ganti gambar pilihan" : promotion.data ? "Ganti gambar banner" : "Pilih gambar banner"}
          <input type="file" accept="image/jpeg,image/png,image/webp" disabled={busy || preparing} className="sr-only" onChange={(event) => { void chooseImage(event.target.files?.[0]); event.target.value = ""; }} />
        </label>
        {preparing && <p role="status" className="text-xs text-slate-400">Mengolah dan mengompres gambar…</p>}
        {prepared && <div className="flex flex-wrap items-center gap-3">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src={prepared.preview} alt="Pratinjau gambar banner" className="h-20 w-40 rounded-lg border border-base-border object-cover" />
          <div className="text-xs text-slate-400"><p>{prepared.width} × {prepared.height} piksel · {formatSize(prepared.originalBytes)} → {formatSize(prepared.blob.size)}{!prepared.optimized && " · file awal sudah lebih ringan"}</p>
            {prepared.width / prepared.height < 1.8 && <p className="mt-1 text-amber-300">Gunakan gambar lanskap lebar agar tidak banyak terpotong.</p>}
          </div>
        </div>}
        {error && <p role="alert" className="text-xs text-red-300">{error}</p>}
        <div className="flex flex-wrap items-center justify-between gap-3 border-t border-base-border pt-4">
          <p className="text-[11px] text-slate-500">JPG, PNG, atau WebP hingga 30 MB. Gambar transparan diberi latar gelap sebelum dikompres.</p>
          <div className="flex gap-2">
            {promotion.data && <button type="button" disabled={busy || preparing} onClick={() => void remove()} className="inline-flex items-center gap-1.5 rounded-lg border border-red-500/30 px-3 py-2 text-xs font-medium text-red-300 disabled:opacity-50"><Trash2 size={14} /> Hapus banner</button>}
            <button type="submit" disabled={busy || preparing || !repo || !title.trim()} className="rounded-lg bg-accent-blue px-4 py-2 text-xs font-semibold text-[#fff] disabled:opacity-50">{busy ? "Menyimpan…" : "Simpan banner"}</button>
          </div>
        </div>
      </form>}
    </div>
  );
}
