"use client";

import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { usePathname } from "next/navigation";
import { subscribeDataChanges } from "@/lib/liveUpdates";

// version: PWA/tab icon set. original: untouched original logo (any
// resolution) stored beside it; empty when only the icon set exists.
type Branding = { version: string; original: string; reload: () => Promise<void> };
const BrandingContext = createContext<Branding | null>(null);

export const LOGO_REPO_PREFIX = "__devcontrol__/logo-";

// The original logo is served untouched from private R2 through the same
// endpoint as thumbnails and banners.
export function logoOriginalURL(version: string, original: string) {
  return `/api/project-thumbnails?repo=${encodeURIComponent(LOGO_REPO_PREFIX + version)}&v=${encodeURIComponent(original)}`;
}

export function useBranding() {
  const branding = useContext(BrandingContext);
  if (!branding) throw new Error("BrandingProvider belum terpasang");
  return branding;
}

export default function BrandingProvider({ children }: { children: ReactNode }) {
  const [version, setVersion] = useState("");
  const [original, setOriginal] = useState("");
  const pathname = usePathname();
  const reload = useCallback(async () => {
    try {
      const response = await fetch("/api/branding", { cache: "no-store", credentials: "same-origin" });
      if (!response.ok) return;
      const body = await response.json();
      if (typeof body.version === "string" && (body.version === "" || /^[0-9a-f]{32}$/.test(body.version))) {
        setVersion(body.version);
        setOriginal(typeof body.original === "string" && /^[0-9a-f]{32}$/.test(body.original) ? body.original : "");
      }
    } catch { /* Keep the last logo when the connection is unavailable. */ }
  }, []);

  useEffect(() => {
    void reload();
    const onVisible = () => { if (document.visibilityState === "visible") void reload(); };
    const unsubscribe = subscribeDataChanges(onVisible);
    const interval = setInterval(onVisible, 30_000);
    document.addEventListener("visibilitychange", onVisible);
    window.addEventListener("online", onVisible);
    return () => {
      clearInterval(interval);
      unsubscribe();
      document.removeEventListener("visibilitychange", onVisible);
      window.removeEventListener("online", onVisible);
    };
  }, [reload]);

  useEffect(() => {
    // The HTML metadata points at the same endpoint for first load. Update its
    // links in open tabs as soon as a new logo version is saved elsewhere.
    for (const [kind, size] of [["icon", "favicon"], ["apple-touch-icon", "apple"]]) {
      let link = document.querySelector<HTMLLinkElement>(`link[data-devcontrol-brand="${kind}"]`);
      if (!link) {
        link = document.createElement("link");
        link.rel = kind;
        link.type = "image/png";
        link.dataset.devcontrolBrand = kind;
        document.head.appendChild(link);
      }
      link.href = `/api/branding/icon?size=${size}&v=${version || "default"}`;
    }
  }, [version, pathname]);

  return <BrandingContext.Provider value={{ version, original, reload }}>{children}</BrandingContext.Provider>;
}
