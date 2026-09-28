// Image slots of an application card. The main thumbnail keeps the plain
// repo key (older thumbnails stay valid); every extra slot is stored as
// "owner/repo~<slot>" in the same private R2 flow. Files are never
// compressed or resized, so a 4K upload stays 4K.

export type ImageSlot = "thumbnail-light" | "thumbnail-dark" | "logo" | "design-light" | "design-dark";
export type ProjectImages = Partial<Record<ImageSlot, string>>;

export const IMAGE_SLOTS: { slot: ImageSlot; suffix: string; label: string; hint: string }[] = [
  { slot: "thumbnail-light", suffix: "", label: "Thumbnail mode terang", hint: "Tampil di kartu saat mode terang." },
  { slot: "thumbnail-dark", suffix: "~dark", label: "Thumbnail mode gelap", hint: "Tampil di kartu saat mode gelap." },
  { slot: "logo", suffix: "~logo", label: "Logo aplikasi", hint: "Disimpan, tidak tampil di kartu." },
  { slot: "design-light", suffix: "~design-light", label: "Desain mode terang", hint: "Disimpan, tidak tampil di kartu." },
  { slot: "design-dark", suffix: "~design-dark", label: "Desain mode gelap", hint: "Disimpan, tidak tampil di kartu." },
];

export const IMAGE_ACCEPT = "image/jpeg,image/png,image/webp";

export function slotRepo(repo: string, slot: ImageSlot) {
  return repo + (IMAGE_SLOTS.find((item) => item.slot === slot)?.suffix ?? "");
}

// Group the flat /api/project-thumbnails list by lower-cased base repo.
export function groupProjectImages(rows: { repo: string; version: string }[]) {
  const grouped = new Map<string, ProjectImages>();
  for (const row of rows) {
    const [base, suffix] = row.repo.split("~", 2);
    const match = IMAGE_SLOTS.find((item) => item.suffix === (suffix === undefined ? "" : `~${suffix}`));
    if (!match || !base) continue;
    const key = base.toLowerCase();
    grouped.set(key, { ...(grouped.get(key) ?? {}), [match.slot]: row.version });
  }
  return grouped;
}

export function imageURL(repo: string, slot: ImageSlot, version: string) {
  return `/api/project-thumbnails?repo=${encodeURIComponent(slotRepo(repo, slot))}&v=${encodeURIComponent(version)}`;
}

// The card follows the active theme and falls back to the other thumbnail
// when only one is set, so an existing single thumbnail keeps showing.
export function cardThumbnail(images: ProjectImages, theme: "dark" | "light"): { slot: ImageSlot; version: string } | null {
  const order: ImageSlot[] = theme === "dark" ? ["thumbnail-dark", "thumbnail-light"] : ["thumbnail-light", "thumbnail-dark"];
  for (const slot of order) {
    const version = images[slot];
    if (version) return { slot, version };
  }
  return null;
}

export function imageContentType(file: File) {
  if (["image/jpeg", "image/png", "image/webp"].includes(file.type)) return file.type;
  if (/\.jpe?g$/i.test(file.name)) return "image/jpeg";
  if (/\.png$/i.test(file.name)) return "image/png";
  if (/\.webp$/i.test(file.name)) return "image/webp";
  return "";
}
