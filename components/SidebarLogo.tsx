"use client";

import { Cloud, PanelLeftClose, PanelLeftOpen } from "lucide-react";
import { useBranding } from "@/components/BrandingProvider";

export default function SidebarLogo({ expanded, onClick, label }: {
  expanded: boolean;
  onClick: () => void;
  label: string;
}) {
  const { version } = useBranding();

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
          ? <img src={`/api/branding/icon?size=192&v=${version}`} alt="" className="h-full w-full rounded-xl object-contain" />
          : <Cloud size={20} />}
      </span>
      <span className="sidebar-brand-toggle pointer-events-none absolute inset-0 flex items-center justify-center opacity-0 transition-opacity duration-150">
        {expanded ? <PanelLeftClose size={20} /> : <PanelLeftOpen size={20} />}
      </span>
    </button>
  );
}
