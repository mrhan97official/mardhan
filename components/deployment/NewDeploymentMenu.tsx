"use client";

import { useEffect, useRef, useState } from "react";
import { ChevronDown, FolderPlus, Plus, RefreshCw, UploadCloud, type LucideIcon } from "lucide-react";
import { useDeploymentOverlay, type ModalKey } from "./DeploymentOverlayProvider";

const MENU_ITEMS: { key: ModalKey; label: string; description: string; icon: LucideIcon }[] = [
  { key: "new_app", label: "Aplikasi Baru", description: "Uji build, simpan di GitHub, lalu online di Vercel", icon: FolderPlus },
  {
    key: "update_app",
    label: "Update Aplikasi",
    description: "Unggah zip untuk memperbarui aplikasi yang ada",
    icon: RefreshCw,
  },
  {
    key: "self_update",
    label: "Update Diri",
    description: "Uji zip di Vercel, lalu dorong ke GitHub bila aman",
    icon: UploadCloud,
  },
];

export default function NewDeploymentMenu() {
  const [open, setOpen] = useState(false);
  const openModal = useDeploymentOverlay();
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function onClickOutside(e: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", onClickOutside);
    return () => document.removeEventListener("mousedown", onClickOutside);
  }, []);

  return (
    <div ref={containerRef} className="relative">
      <button
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="menu"
        aria-expanded={open}
        className="flex items-center gap-1.5 rounded-xl bg-accent-blue px-3 py-2 text-sm font-semibold text-white hover:bg-blue-500 whitespace-nowrap"
      >
        <Plus size={16} />
        <span className="hidden sm:inline">New Deployment</span>
        <span className="sm:hidden">Deploy</span>
        <ChevronDown size={14} className={`transition-transform ${open ? "rotate-180" : ""}`} />
      </button>

      {open && (
        <div className="absolute right-0 z-20 mt-2 w-72">
          <span
            aria-hidden="true"
            className="absolute -top-[5px] right-4 h-2.5 w-2.5 rotate-45 rounded-[2px] border-l border-t border-base-border bg-base-850"
          />
          <div
            role="menu"
            className="relative overflow-hidden rounded-xl border border-base-border bg-base-850 shadow-glow"
          >
          {MENU_ITEMS.map(({ key, label, description, icon: Icon }) => (
            <button
              key={key}
              role="menuitem"
              type="button"
              onClick={() => {
                openModal(key);
                setOpen(false);
              }}
              className="flex w-full items-start gap-3 px-3.5 py-3 text-left hover:bg-base-800"
            >
              <span className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-accent-blue/15 text-accent-blue">
                <Icon size={16} />
              </span>
              <span>
                <span className="block text-sm font-semibold text-slate-100">{label}</span>
                <span className="block text-xs text-slate-500">{description}</span>
              </span>
            </button>
          ))}
          </div>
        </div>
      )}
    </div>
  );
}
