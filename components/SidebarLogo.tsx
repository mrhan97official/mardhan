"use client";

import { useState } from "react";
import { Cloud, PanelLeftClose, PanelLeftOpen } from "lucide-react";
import { logoOriginalURL, useBranding } from "@/components/BrandingProvider";

export default function SidebarLogo({ expanded, onClick, label }: {
  expanded: boolean;
  onClick: () => void;
  label: string;
}) {
  const { version, original } = useBranding();
  const [failed, setFailed] = useState("");
  // Show the original upload at full resolution; fall back to the cached PWA
  // icon when offline or when an older logo has no stored original.
  const showOriginal = !!original && failed !== original;

  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={onClick}
      className={`sidebar-brand-control relative flex h-9 w-9 shrink-0 items-center justify-center rounded-xl text-accent-blue ${version ? "bg-transparent" : "bg-accent-blue/15"}`}
    >
      <span className="sidebar-brand-face absolute inset-0 flex items-center justify-center transition-opacity duration-150">
        {version
          ? showOriginal
            ? <img key={original} src={logoOriginalURL(version, original)} alt="" decoding="async" className="h-full w-full rounded-xl object-contain" onError={() => setFailed(original)} />
            : <img src={`/api/branding/icon?size=192&v=${version}`} alt="" className="h-full w-full rounded-xl object-contain" />
          : <Cloud size={20} />}
      </span>
      <span className="sidebar-brand-toggle pointer-events-none absolute inset-0 flex items-center justify-center opacity-0 transition-opacity duration-150">
        {expanded ? <PanelLeftClose size={20} /> : <PanelLeftOpen size={20} />}
      </span>
    </button>
  );
}
