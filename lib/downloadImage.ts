// Downloads an image exactly as the server stores it (no canvas, no
// re-encode), so a 4K original is saved as the same 4K file. Works for the
// authenticated same-origin image endpoints and for local blob: previews.

const EXTENSIONS: Record<string, string> = {
  "image/jpeg": "jpg",
  "image/png": "png",
  "image/webp": "webp",
  "image/gif": "gif",
  "image/svg+xml": "svg",
  "image/avif": "avif",
};

function safeName(name: string) {
  const cleaned = name.replace(/[\\/:*?"<>|~\s]+/g, "-").replace(/-+/g, "-").replace(/^-|-$/g, "");
  return cleaned || "gambar";
}

export async function downloadImage(url: string, baseName: string) {
  const response = await fetch(url, { credentials: "same-origin" });
  if (!response.ok) throw new Error(`Gambar gagal diunduh (HTTP ${response.status}).`);
  const blob = await response.blob();
  const type = (blob.type || response.headers.get("Content-Type") || "").split(";")[0].trim().toLowerCase();
  const extension = EXTENSIONS[type];
  const href = URL.createObjectURL(blob);
  try {
    const link = document.createElement("a");
    link.href = href;
    link.download = extension ? `${safeName(baseName)}.${extension}` : safeName(baseName);
    link.rel = "noopener";
    document.body.appendChild(link);
    link.click();
    link.remove();
  } finally {
    // Give the browser time to start saving before the blob is released.
    window.setTimeout(() => URL.revokeObjectURL(href), 60_000);
  }
}
