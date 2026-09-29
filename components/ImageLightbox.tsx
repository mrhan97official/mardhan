"use client";

import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { Download, ExternalLink, Loader2, Maximize2, Minimize2, X } from "lucide-react";
import { downloadImage } from "@/lib/downloadImage";

// Full-screen image viewer shared by the app-card images dialog and Settings.
// The picture is shown at its original resolution (CSS-scaled only in
// "Pas layar"), and "Unduh" saves the untouched original file.
export default function ImageLightbox({ src, label, filename, onClose, zIndexClass = "z-[110]" }: {
  src: string;
  label: string;
  /** File name without extension; the extension follows the image type. */
  filename: string;
  onClose: () => void;
  zIndexClass?: string;
}) {
  const [actualSize, setActualSize] = useState(false);
  const [size, setSize] = useState("");
  const [downloading, setDownloading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  async function download() {
    setDownloading(true);
    setError("");
    try { await downloadImage(src, filename); }
    catch (cause) { setError(cause instanceof Error ? cause.message : "Gambar gagal diunduh."); }
    finally { setDownloading(false); }
  }

  const external = !src.startsWith("blob:");

  return createPortal(
    <div className={`fixed inset-0 ${zIndexClass} flex flex-col bg-black/90`} role="dialog" aria-modal="true" aria-label={label}>
      <div className="flex items-center justify-between gap-2 p-2 text-sm text-slate-200">
        <span className="min-w-0 truncate">{label}{size ? ` · ${size}` : ""}</span>
        <div className="flex shrink-0 items-center gap-1">
          <button type="button" onClick={() => setActualSize((value) => !value)} className="inline-flex items-center gap-1 rounded-lg px-2 py-1.5 hover:bg-white/10">
            {actualSize ? <><Minimize2 size={15} /> <span className="hidden sm:inline">Pas layar</span></> : <><Maximize2 size={15} /> <span className="hidden sm:inline">Ukuran asli</span></>}
          </button>
          {external && <a href={src} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 rounded-lg px-2 py-1.5 hover:bg-white/10"><ExternalLink size={15} /> <span className="hidden sm:inline">Tab baru</span></a>}
          <button type="button" disabled={downloading} onClick={() => void download()} className="inline-flex items-center gap-1 rounded-lg bg-accent-blue px-2.5 py-1.5 font-medium text-white hover:bg-blue-500 disabled:opacity-60">
            {downloading ? <Loader2 size={15} className="animate-spin" /> : <Download size={15} />} Unduh
          </button>
          <button type="button" aria-label="Tutup pratinjau" onClick={onClose} className="rounded-lg p-1.5 hover:bg-white/10"><X size={18} /></button>
        </div>
      </div>
      {error && <p role="alert" className="mx-2 mb-2 rounded-lg bg-red-500/15 p-2 text-xs text-red-300">{error}</p>}
      <div className={`min-h-0 flex-1 overflow-auto ${actualSize ? "" : "flex items-center justify-center"}`} onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img src={src} alt={label} decoding="async"
          onLoad={(event) => { const img = event.currentTarget; setSize(`${img.naturalWidth} × ${img.naturalHeight} px`); }}
          className={actualSize ? "max-w-none" : "max-h-full max-w-full object-contain"} />
      </div>
    </div>,
    document.body,
  );
}
