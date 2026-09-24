"use client";

import { createContext, useCallback, useContext, useState } from "react";
import Link from "next/link";
import { Eye, Loader2 } from "lucide-react";
import DeployFormModal from "./DeployFormModal";
import SelfUpdateModal from "./SelfUpdateModal";

export type ModalKey = "new_app" | "update_app" | "self_update";

const LABELS: Record<ModalKey, string> = {
  new_app: "Aplikasi Baru",
  update_app: "Update Aplikasi",
  self_update: "Update Diri",
};

interface Progress {
  running: boolean;
  failed: boolean;
  message: string;
}

const DeploymentOverlayContext = createContext<(mode: ModalKey) => void>(() => {});

export function useDeploymentOverlay() {
  return useContext(DeploymentOverlayContext);
}

export default function DeploymentOverlayProvider({ children }: { children: React.ReactNode }) {
  const [activeModal, setActiveModal] = useState<ModalKey | null>(null);
  const [hidden, setHidden] = useState(false);
  const [progress, setProgress] = useState<Progress>({ running: false, failed: false, message: "" });

  const openModal = useCallback((mode: ModalKey) => {
    // Keep the existing form mounted while it is working, even if another
    // deployment menu is opened from a different page.
    setActiveModal((current) => current ?? mode);
    setHidden(false);
  }, []);

  const closeModal = useCallback(() => {
    setActiveModal(null);
    setHidden(false);
    setProgress({ running: false, failed: false, message: "" });
  }, []);

  const onSuccess = useCallback(() => {
    window.dispatchEvent(new Event("deployment:changed"));
  }, []);

  return (
    <DeploymentOverlayContext.Provider value={openModal}>
      {children}
      {activeModal && (
        <>
          {(activeModal === "new_app" || activeModal === "update_app") && (
            <DeployFormModal
              mode={activeModal}
              hidden={hidden}
              onHide={() => setHidden(true)}
              onClose={closeModal}
              onSuccess={onSuccess}
              onProgress={setProgress}
            />
          )}
          {activeModal === "self_update" && (
            <SelfUpdateModal
              hidden={hidden}
              onHide={() => setHidden(true)}
              onClose={closeModal}
              onSuccess={onSuccess}
              onProgress={setProgress}
            />
          )}
          {hidden && (
            <div className="fixed bottom-4 right-4 z-[55] w-[min(22rem,calc(100vw-2rem))] rounded-xl border border-base-border bg-base-850 p-3 shadow-glow" role="status" aria-live="polite">
              <div className="flex items-center gap-2 text-sm font-semibold text-slate-100">
                {progress.running && <Loader2 size={16} className="shrink-0 animate-spin text-accent-blue" />}
                <span>{LABELS[activeModal]} {progress.running ? "sedang diproses" : progress.failed ? "gagal" : "selesai diproses"}</span>
              </div>
              <p className="mt-1 line-clamp-2 text-xs text-slate-400">{progress.message || "Lihat hasil proses di halaman ini."}</p>
              {progress.running && (
                <p className="mt-1 text-xs text-amber-400">Biarkan tab tetap terbuka sampai proses selesai.</p>
              )}
              <div className="mt-3 flex flex-wrap items-center gap-3 text-xs font-semibold">
                <Link href="/deployments" className="text-accent-blue hover:underline">Pantau di Pipeline</Link>
                <button type="button" onClick={() => setHidden(false)} className="inline-flex items-center gap-1 text-slate-200 hover:underline">
                  <Eye size={13} /> Tampilkan proses
                </button>
              </div>
            </div>
          )}
        </>
      )}
    </DeploymentOverlayContext.Provider>
  );
}
