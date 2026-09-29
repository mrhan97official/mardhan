"use client";

import { useState } from "react";
import { Cloud, PanelLeftClose, PanelLeftOpen } from "lucide-react";
import { logoOriginalURL, useBranding } from "@/components/BrandingProvider";

const TOOLTIP_BASE = "nav-tooltip pointer-events-none invisible absolute z-[60] hidden whitespace-nowrap rounded-lg border border-base-border bg-base-800 px-3 py-2 text-xs font-semibold text-slate-100 opacity-0 shadow-xl transition-opacity before:absolute before:h-2 before:w-2 before:rotate-45 before:border-base-border before:bg-base-800 md:block md:group-focus-visible:visible md:group-focus-visible:opacity-100";

// Same custom tooltip as the sidebar menu icons (not the browser's title
// tooltip): to the right when the sidebar is collapsed, below the logo when
// it is expanded so it doesn't cover the "DEV CONTROL" wordmark.
const TOOLTIP_PLACEMENT = {
  right: "left-full top-1/2 ml-3 -translate-y-1/2 before:left-0 before:top-1/2 before:-translate-x-1/2 before:-translate-y-1/2 before:border-b before:border-l",
  bottom: "left-0 top-full mt-3 before:left-3.5 before:top-0 before:-translate-y-1/2 before:border-l before:border-t",
} as const;

export default function SidebarLogo({ expanded, onClick, label, tooltip }: {
  expanded: boolean;
  onClick: () => void;
  label: string;
  /** Where the custom tooltip appears; omit for no tooltip (touch header). */
  tooltip?: keyof typeof TOOLTIP_PLACEMENT;
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
      onClick={onClick}
      className={`sidebar-brand-control ${tooltip ? "nav-tooltip-parent group" : ""} relative flex h-9 w-9 shrink-0 items-center justify-center rounded-xl text-accent-blue ${version ? "bg-transparent" : "bg-accent-blue/15"}`}
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
      {tooltip && <span aria-hidden="true" className={`${TOOLTIP_BASE} ${TOOLTIP_PLACEMENT[tooltip]}`}>{label}</span>}
    </button>
  );
}
