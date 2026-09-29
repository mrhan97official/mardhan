"use client";

import { createContext, useCallback, useContext, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import DeployFormModal from "./DeployFormModal";
import SelfUpdateModal from "./SelfUpdateModal";

export type ModalKey = "new_app" | "update_app" | "self_update";

/** Opens "Update Aplikasi" for one app (from its card): the app is fixed. */
export type DeployPreset = { app: string; label?: string };

interface Progress {
  running: boolean;
  failed: boolean;
  message: string;
  target?: string;
}

interface Task {
  id: string;
  mode: ModalKey;
  preset?: DeployPreset;
  progress: Progress;
}

// Pages that already show the Deployment Pipeline card.
const PIPELINE_PAGES = ["/", "/deployments"];

const DeploymentOverlayContext = createContext<(mode: ModalKey, preset?: DeployPreset) => void>(() => {});

export function useDeploymentOverlay() {
  return useContext(DeploymentOverlayContext);
}

function DeploymentTask({
  task, hidden, onHide, onClose, onSuccess, onProgress, onStarted, onFinished,
}: {
  task: Task;
  hidden: boolean;
  onHide: () => void;
  onClose: () => void;
  onSuccess: () => void;
  onProgress: (id: string, progress: Progress) => void;
  onStarted: (id: string) => void;
  onFinished: (id: string, showForm: boolean) => void;
}) {
  // Both modals report in effects/handlers that depend on these callbacks.
  const report = useCallback((progress: Progress) => onProgress(task.id, progress), [onProgress, task.id]);
  const started = useCallback(() => onStarted(task.id), [onStarted, task.id]);
  const finished = useCallback((showForm: boolean) => onFinished(task.id, showForm), [onFinished, task.id]);
  return task.mode === "self_update" ? (
    <SelfUpdateModal hidden={hidden} onHide={onHide} onClose={onClose} onSuccess={onSuccess} onProgress={report}
      onStarted={started} onFinished={finished} />
  ) : (
    <DeployFormModal mode={task.mode} preset={task.preset} hidden={hidden} onHide={onHide} onClose={onClose} onSuccess={onSuccess} onProgress={report}
      onStarted={started} onFinished={finished} />
  );
}

// Deploy forms close themselves once a deploy starts; the running form stays
// mounted but hidden so it can finish its request, and progress is followed
// in the Deployment Pipeline card. There is no separate floating status panel.
export default function DeploymentOverlayProvider({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const pathname = usePathname();
  const [tasks, setTasks] = useState<Task[]>([]);
  const [activeId, setActiveId] = useState<string | null>(null);

  const openModal = useCallback((mode: ModalKey, preset?: DeployPreset) => {
    const id = crypto.randomUUID();
    // Idle forms that were never started have no way back once hidden; drop them.
    setTasks((current) => [...current.filter((task) => task.progress.running), {
      id, mode, preset, progress: { running: false, failed: false, message: "Menunggu proses." },
    }]);
    setActiveId(id);
  }, []);

  const updateProgress = useCallback((id: string, progress: Progress) => {
    setTasks((current) => current.map((task) => {
      if (task.id !== id) return task;
      if (task.progress.running === progress.running && task.progress.failed === progress.failed &&
          task.progress.message === progress.message && task.progress.target === progress.target) return task;
      return { ...task, progress };
    }));
  }, []);

  const closeTask = useCallback((id: string) => {
    setTasks((current) => current.filter((task) => task.id !== id || task.progress.running));
    setActiveId((current) => current === id ? null : current);
  }, []);

  const startTask = useCallback((id: string) => {
    setActiveId((current) => current === id ? null : current);
    if (!PIPELINE_PAGES.includes(pathname)) router.push("/");
  }, [pathname, router]);

  const finishTask = useCallback((id: string, showForm: boolean) => {
    if (showForm) {
      // Stopped before a Pipeline job existed: bring the form back with its error.
      setActiveId(id);
      return;
    }
    setTasks((current) => current.filter((task) => task.id !== id));
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
          onStarted={startTask}
          onFinished={finishTask}
        />
      ))}
    </DeploymentOverlayContext.Provider>
  );
}
