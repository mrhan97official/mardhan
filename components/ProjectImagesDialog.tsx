"use client";

import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Download, ImageIcon, ImagePlus, Loader2, Maximize2, Trash2, X } from "lucide-react";
import ImageLightbox from "@/components/ImageLightbox";
import { downloadImage } from "@/lib/downloadImage";
import { IMAGE_ACCEPT, IMAGE_SLOTS, imageURL, type ImageSlot, type ProjectImages } from "@/lib/projectImages";

// Every slot is shown at its original resolution when opened; previews are
// only scaled by CSS, the stored file is never compressed or resized.
export default function ProjectImagesDialog({ repo, images, admin, uploadingSlot, uploadProgress, error, onUpload, onRemove, onClose }: {
  repo: string;
  images: ProjectImages;
  admin: boolean;
  uploadingSlot: ImageSlot | null;
  uploadProgress: number | null;
  error?: string;
  onUpload: (slot: ImageSlot, file: File) => void;
  onRemove: (slot: ImageSlot) => void;
  onClose: () => void;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  const pendingSlot = useRef<ImageSlot | null>(null);
  const [sizes, setSizes] = useState<Partial<Record<ImageSlot, string>>>({});
  const [failed, setFailed] = useState<Partial<Record<ImageSlot, string>>>({});
  const [viewing, setViewing] = useState<ImageSlot | null>(null);
  const [downloadingSlot, setDownloadingSlot] = useState<ImageSlot | null>(null);
  const [downloadError, setDownloadError] = useState("");
  const busy = uploadingSlot !== null;

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      if (viewing) setViewing(null); else if (!busy) onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [viewing, busy, onClose]);

  function pick(slot: ImageSlot) {
    pendingSlot.current = slot;
    inputRef.current?.click();
  }

  const viewed = viewing ? images[viewing] : undefined;
  const viewedLabel = IMAGE_SLOTS.find((item) => item.slot === viewing)?.label ?? "";
  const repoName = repo.split("/").pop() || repo;

  // Saves the stored original byte-for-byte (same file shown in "Ukuran asli").
  async function download(slot: ImageSlot, version: string) {
    setDownloadingSlot(slot);
    setDownloadError("");
    try { await downloadImage(imageURL(repo, slot, version), `${repoName}-${slot}`); }
    catch (cause) { setDownloadError(cause instanceof Error ? cause.message : "Gambar gagal diunduh."); }
    finally { setDownloadingSlot(null); }
  }

  return createPortal(
    <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/75 p-2" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget && !busy) onClose(); }}>
      <section role="dialog" aria-modal="true" aria-labelledby="project-images-title" className="flex max-h-[92vh] w-full max-w-4xl flex-col rounded-2xl border border-base-border bg-base-900 p-3 shadow-2xl">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <h2 id="project-images-title" className="flex items-center gap-2 text-base font-bold text-white"><ImageIcon size={18} className="text-accent-blue" /> Gambar aplikasi</h2>
            <p className="truncate text-xs text-slate-400" title={repo}>{repo} · semua gambar opsional, disimpan dalam resolusi asli tanpa kompresi</p>
          </div>
          <button type="button" aria-label="Tutup" disabled={busy} onClick={onClose} className="rounded-lg p-1 text-slate-300 hover:bg-base-800 disabled:opacity-50"><X size={18} /></button>
        </div>
        {error && <p role="alert" className="mt-2 rounded-lg bg-red-500/10 p-2 text-xs text-red-300">{error}</p>}
        {downloadError && <p role="alert" className="mt-2 rounded-lg bg-red-500/10 p-2 text-xs text-red-300">{downloadError}</p>}
        <div className="mt-3 grid min-h-0 grid-cols-1 gap-2 overflow-y-auto sm:grid-cols-2 lg:grid-cols-3">
          {IMAGE_SLOTS.map(({ slot, label, hint }) => {
            const version = images[slot];
            const shown = version && failed[slot] !== version;
            const uploadingHere = uploadingSlot === slot;
            return (
              <article key={slot} className="flex min-w-0 flex-col gap-2 rounded-xl border border-base-border bg-base-800/40 p-2">
                <div>
                  <h3 className="text-sm font-semibold text-white">{label}</h3>
                  <p className="text-[11px] text-slate-400">{hint}</p>
                </div>
                <button type="button" disabled={!shown} onClick={() => setViewing(slot)} aria-label={shown ? `Lihat ${label}` : `${label} belum ada`} className="logo-transparency-bg relative flex aspect-video items-center justify-center overflow-hidden rounded-lg border border-base-border disabled:cursor-default">
                  {shown
                    // eslint-disable-next-line @next/next/no-img-element
                    ? <img key={version} src={imageURL(repo, slot, version)} alt={label} loading="lazy" decoding="async" className="h-full w-full object-contain"
                        onLoad={(event) => { const img = event.currentTarget; setSizes((current) => ({ ...current, [slot]: `${img.naturalWidth} × ${img.naturalHeight} px` })); }}
                        onError={() => setFailed((current) => ({ ...current, [slot]: version }))} />
                    : <span className="text-xs text-slate-500">{version ? "Gambar belum dapat dimuat" : "Belum ada gambar"}</span>}
                </button>
                <p className="min-h-[1rem] text-[11px] text-slate-500">
                  {uploadingHere ? (uploadProgress === null ? "Menyiapkan unggahan…" : uploadProgress < 100 ? `Mengunggah ${uploadProgress}%` : "Memverifikasi gambar…") : shown ? sizes[slot] ?? "Memuat ukuran…" : ""}
                </p>
                <div className="mt-auto flex flex-wrap gap-1.5">
                  {shown && <button type="button" onClick={() => setViewing(slot)} className="inline-flex items-center gap-1 rounded-lg border border-base-border px-2 py-1.5 text-xs text-slate-200 hover:bg-base-800"><Maximize2 size={13} /> Lihat</button>}
                  {shown && <button type="button" disabled={downloadingSlot === slot} onClick={() => void download(slot, version!)} className="inline-flex items-center gap-1 rounded-lg border border-base-border px-2 py-1.5 text-xs text-slate-200 hover:bg-base-800 disabled:opacity-50">{downloadingSlot === slot ? <Loader2 size={13} className="animate-spin" /> : <Download size={13} />} Unduh</button>}
                  {admin && <button type="button" disabled={busy} onClick={() => pick(slot)} className="inline-flex items-center gap-1 rounded-lg bg-accent-blue px-2 py-1.5 text-xs font-medium text-white hover:bg-blue-500 disabled:opacity-50"><ImagePlus size={13} /> {version ? "Ganti" : "Unggah"}</button>}
                  {admin && version && <button type="button" disabled={busy} onClick={() => { if (window.confirm(`Hapus ${label.toLowerCase()}?`)) onRemove(slot); }} className="inline-flex items-center gap-1 rounded-lg border border-red-400/40 px-2 py-1.5 text-xs text-red-300 hover:bg-red-500/10 disabled:opacity-50"><Trash2 size={13} /> Hapus</button>}
                </div>
              </article>
            );
          })}
        </div>
        <p className="mt-2 text-[11px] text-slate-500">JPG, PNG, atau WebP resolusi berapa pun (termasuk 4K). Kartu memakai thumbnail sesuai mode tampilan; bila hanya satu yang diisi, thumbnail itu dipakai di kedua mode.</p>
        <input ref={inputRef} type="file" accept={IMAGE_ACCEPT} className="sr-only" aria-label="Pilih gambar" onChange={(event) => {
          const file = event.currentTarget.files?.[0];
          const slot = pendingSlot.current;
          event.currentTarget.value = "";
          if (file && slot) onUpload(slot, file);
        }} />
      </section>

      {viewing && viewed && (
        <ImageLightbox src={imageURL(repo, viewing, viewed)} label={viewedLabel} filename={`${repoName}-${viewing}`} onClose={() => setViewing(null)} />
      )}
    </div>,
    document.body,
  );
}
