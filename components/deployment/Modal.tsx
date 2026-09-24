"use client";

import { useEffect } from "react";
import { EyeOff, X } from "lucide-react";

export default function Modal({
  title,
  onClose,
  onHide,
  hidden = false,
  children,
  widthClassName = "max-w-md",
}: {
  title: string;
  onClose: () => void;
  onHide?: () => void;
  hidden?: boolean;
  children: React.ReactNode;
  widthClassName?: string;
}) {
  useEffect(() => {
    if (hidden) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, hidden]);

  return (
    <div className={hidden ? "hidden" : "fixed inset-0 z-[60] flex items-center justify-center p-4"}>
      <button
        aria-label={onHide ? "Sembunyikan proses" : "Tutup"}
        onClick={onClose}
        className="absolute inset-0 bg-black/70 backdrop-blur-sm"
      />
      <div className={`card relative w-full ${widthClassName} max-h-[90vh] overflow-y-auto p-5 sm:p-6`}>
        <div className="mb-4 flex items-center justify-between gap-3">
          <h2 className="text-base font-bold text-white sm:text-lg">{title}</h2>
          <div className="flex items-center gap-2">
            {onHide && (
              <button type="button" onClick={onHide} className="inline-flex items-center gap-1 rounded-lg px-2 py-1.5 text-xs font-semibold text-accent-blue hover:bg-base-800">
                <EyeOff size={15} /> Hide
              </button>
            )}
            <button
              aria-label={onHide ? "Sembunyikan proses" : "Tutup"}
              onClick={onClose}
              className="rounded-lg p-1.5 text-slate-400 hover:bg-base-800 hover:text-slate-200"
            >
              <X size={18} />
            </button>
          </div>
        </div>
        {children}
      </div>
    </div>
  );
}
