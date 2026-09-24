"use client";

import { createContext, useCallback, useContext, useState } from "react";
import Link from "next/link";
import { Eye, Loader2, Plus, X } from "lucide-react";
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
  target?: string;
}

interface Task {
  id: string;
  mode: ModalKey;
  progress: Progress;
  started: boolean;
}

const DeploymentOverlayContext = createContext<(mode: ModalKey) => void>(() => {});

export function useDeploymentOverlay() {
  return useContext(DeploymentOverlayContext);
}

function DeploymentTask({
  task, hidden, onHide, onClose, onSuccess, onProgress,
}: {
  task: Task;
  hidden: boolean;
  onHide: () => void;
  onClose: () => void;
  onSuccess: () => void;
  onProgress: (id: string, progress: Progress) => void;
}) {
  // Both modals report progress in an effect that depends on this callback.
  const report = useCallback((progress: Progress) => onProgress(task.id, progress), [onProgress, task.id]);
  return task.mode === "self_update" ? (
    <SelfUpdateModal hidden={hidden} onHide={onHide} onClose={onClose} onSuccess={onSuccess} onProgress={report} />
  ) : (
    <DeployFormModal mode={task.mode} hidden={hidden} onHide={onHide} onClose={onClose} onSuccess={onSuccess} onProgress={report} />
  );
}

export default function DeploymentOverlayProvider({ children }: { children: React.ReactNode }) {
  const [tasks, setTasks] = useState<Task[]>([]);
  const [activeId, setActiveId] = useState<string | null>(null);

  const openModal = useCallback((mode: ModalKey) => {
    const id = crypto.randomUUID();
    setTasks((current) => [...current, {
      id, mode, started: false,
      progress: { running: false, failed: false, message: "Menunggu proses." },
    }]);
    setActiveId(id);
  }, []);

  const updateProgress = useCallback((id: string, progress: Progress) => {
    setTasks((current) => current.map((task) => {
      if (task.id !== id) return task;
      const started = task.started || progress.running || progress.failed;
      if (task.started === started &&
          task.progress.running === progress.running && task.progress.failed === progress.failed &&
          task.progress.message === progress.message && task.progress.target === progress.target) return task;
      return { ...task, started, progress };
    }));
  }, []);

  const closeTask = useCallback((id: string) => {
    setTasks((current) => current.filter((task) => task.id !== id || task.progress.running));
    setActiveId((current) => current === id ? null : current);
  }, []);

  const onSuccess = useCallback(() => {
    window.dispatchEvent(new Event("deployment:changed"));
  }, []);

  return (
    <DeploymentOverlayContext.Provider value={openModal}>
      {children}
      {tasks.map((task) => (
        <DeploymentTask
          key={task.id}
          task={task}
          hidden={activeId !== task.id}
          onHide={() => setActiveId(null)}
          onClose={() => closeTask(task.id)}
          onSuccess={onSuccess}
          onProgress={updateProgress}
        />
      ))}
      {activeId === null && tasks.length > 0 && (
        <div className="fixed bottom-4 right-4 z-[55] w-[min(24rem,calc(100vw-2rem))] rounded-xl border border-base-border bg-base-850 p-3 shadow-glow" role="status" aria-live="polite">
          <div className="mb-2 flex items-center justify-between gap-2">
            <p className="text-sm font-semibold text-slate-100">Proses deployment ({tasks.filter((task) => task.progress.running).length} berjalan)</p>
            <Link href="/deployments" className="text-xs font-medium text-accent-blue hover:underline">Lihat pipeline</Link>
          </div>
          <div className="max-h-[45vh] space-y-2 overflow-y-auto">
            {tasks.map((task) => (
              <div key={task.id} className="rounded-lg border border-base-border bg-base-900 p-2.5">
                <div className="flex items-center justify-between gap-2">
                  <div className="flex min-w-0 items-center gap-2 text-xs font-semibold text-slate-100">
                    {task.progress.running && <Loader2 size={14} className="shrink-0 animate-spin text-accent-blue" />}
                    <span className="truncate">{LABELS[task.mode]}{task.progress.target ? ` · ${task.progress.target}` : ""}</span>
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
                    <button type="button" onClick={() => setActiveId(task.id)} aria-label={`Tampilkan ${LABELS[task.mode]}`} className="text-accent-blue hover:text-blue-300"><Eye size={15} /></button>
                    {!task.progress.running && <button type="button" onClick={() => closeTask(task.id)} aria-label={`Tutup ${LABELS[task.mode]}`} className="text-slate-400 hover:text-slate-200"><X size={15} /></button>}
                  </div>
                </div>
                <p className={`mt-1 line-clamp-2 text-xs ${task.progress.failed ? "text-red-400" : "text-slate-400"}`}>
                  {task.started ? task.progress.message : "Form siap diisi."}
                </p>
              </div>
            ))}
          </div>
          {tasks.some((task) => task.progress.running) && <p className="mt-2 text-xs text-amber-400">Biarkan tab tetap terbuka sampai semua proses selesai.</p>}
          <div className="mt-3 flex flex-wrap gap-2 border-t border-base-border pt-3">
            {(["new_app", "update_app", "self_update"] as const).map((mode) => (
              <button key={mode} type="button" onClick={() => openModal(mode)} className="inline-flex items-center gap-1 rounded-lg border border-base-border px-2 py-1.5 text-xs font-medium text-accent-blue hover:bg-base-800">
                <Plus size={12} /> {LABELS[mode]}
              </button>
            ))}
          </div>
        </div>
      )}
    </DeploymentOverlayContext.Provider>
  );
}
