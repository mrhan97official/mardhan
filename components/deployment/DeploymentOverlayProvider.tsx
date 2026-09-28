"use client";

import { createContext, useCallback, useContext, useEffect, useState } from "react";
import DeployFormModal from "./DeployFormModal";
import SelfUpdateModal from "./SelfUpdateModal";

export type ModalKey = "new_app" | "update_app" | "self_update";

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

  // Tanpa panel melayang: tugas tersembunyi yang sudah selesai dibersihkan otomatis;
  // jika gagal, jendela dimunculkan lagi supaya pesan galat tidak hilang.
  useEffect(() => {
    const finished = tasks.filter((task) => task.id !== activeId && task.started && !task.progress.running);
    if (finished.length === 0) return;
    const failed = finished.find((task) => task.progress.failed);
    if (failed) { setActiveId(failed.id); return; }
    setTasks((current) => current.filter((task) => !finished.some((done) => done.id === task.id)));
  }, [tasks, activeId]);

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
    </DeploymentOverlayContext.Provider>
  );
}
