"use client";

import { useEffect, useState } from "react";
import { CheckCircle2, Circle, Loader2, UploadCloud, XCircle } from "lucide-react";
import Modal from "./Modal";
import { useOfflineData } from "@/lib/useOfflineData";
import { fallbackServices } from "@/lib/fallbackData";
import type { Service } from "@/lib/types";
import { pollDeploymentJob } from "@/lib/pollDeploymentJob";
import Link from "next/link";

type Mode = "new_app" | "update_app";

interface DeployResponse {
  step: "extract" | "vercel-test" | "github" | "vercel-live" | "done";
  ok: boolean;
  message: string;
  repo?: string;
  app_url?: string;
  status?: "pending" | "ready";
  ticket?: string;
  archive_id?: string;
}

const STEP_ORDER = ["extract", "vercel-test", "github", "vercel-live"] as const;
type StepKey = (typeof STEP_ORDER)[number];
type StepState = "pending" | "active" | "done" | "failed";

const STEPS: { key: StepKey; label: string }[] = [
  { key: "extract", label: "Ekstrak file zip" },
  { key: "vercel-test", label: "Uji build di Vercel" },
  { key: "github", label: "Dorong ke GitHub" },
  { key: "vercel-live", label: "Onlinekan di Vercel" },
];

export default function DeployFormModal({
  mode,
  hidden,
  onHide,
  onClose,
  onSuccess,
  onProgress,
}: {
  mode: Mode;
  hidden: boolean;
  onHide: () => void;
  onClose: () => void;
  onSuccess: () => void;
  onProgress: (progress: { running: boolean; failed: boolean; message: string; target?: string }) => void;
}) {
  const services = useOfflineData<Service[]>("services", "/api/services", fallbackServices, 10000);
  const isUpdate = mode === "update_app";

  const [name, setName] = useState("");
  const [branch, setBranch] = useState("");
  const [zipFile, setZipFile] = useState<File | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [activeStep, setActiveStep] = useState<StepKey | null>(null);
  const [failedStep, setFailedStep] = useState<StepKey | null>(null);
  const [completedSteps, setCompletedSteps] = useState<StepKey[]>([]);
  const [result, setResult] = useState<DeployResponse | null>(null);
  const [archiveSaved, setArchiveSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [elapsedSeconds, setElapsedSeconds] = useState(0);

  useEffect(() => {
    const stage = STEPS.find((step) => step.key === activeStep)?.label;
    onProgress({
      running: submitting,
      failed: Boolean(error || (result && !result.ok)),
      message: error ?? (submitting && activeStep && result?.step !== activeStep ? `${stage}...` : result?.message) ?? (stage ? `${stage}...` : "Menunggu proses."),
      target: name.trim(),
    });
  }, [submitting, activeStep, result, error, name, onProgress]);

  function stepState(step: StepKey): StepState {
    if (completedSteps.includes(step)) return "done";
    if (failedStep === step) return "failed";
    if (result?.step === step && !result.ok) return "failed";
    if (activeStep === step) return "active";
    return "pending";
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);

    if (!name.trim()) {
      setError(isUpdate ? "Pilih aplikasi yang akan diperbarui." : "Nama aplikasi wajib diisi.");
      return;
    }
    if (!zipFile) {
      setError("Unggah file .zip terlebih dahulu.");
      return;
    }
    if (!zipFile.name.toLowerCase().endsWith(".zip")) {
      setError("File harus berformat .zip.");
      return;
    }
    if (zipFile.size > 4 * 1024 * 1024) {
      setError("File ZIP maksimal 4 MB untuk deployment melalui Vercel.");
      return;
    }

    setSubmitting(true);
    setResult(null);
    setArchiveSaved(false);
    setCompletedSteps([]);
    setFailedStep(null);
    setElapsedSeconds(0);
    let currentPhase: StepKey = "extract";
    let archiveID = "";
    try {
      const form = new FormData();
      form.append("type", mode);
      form.append("phase", "extract");
      form.append("name", name.trim());
      if (branch.trim()) form.append("branch", branch.trim());
      form.append("environment", "Production");
      form.append("zip", zipFile);
      const res = await fetch("/api/deploy", { method: "POST", body: form });
      const body = await res.json().catch(() => null);
      if (!res.ok) throw new Error((body && body.error) || `Gagal menyimpan ZIP (HTTP ${res.status})`);
      const parsed = body as DeployResponse;
      setResult(parsed);
      if (parsed.archive_id) { archiveID = parsed.archive_id; setArchiveSaved(true); }
      if (!parsed.ok || !archiveID) { setFailedStep("extract"); return; }
      setCompletedSteps(["extract"]);
      setActiveStep("vercel-test");
      const final = await pollDeploymentJob(archiveID, (job, elapsed) => {
        const done = job.stages.filter((stage) => stage.status === "Success").map((stage) => STEP_ORDER[stage.position - 1]);
        setCompletedSteps(done.filter((step): step is StepKey => Boolean(step)));
        const running = job.stages.find((stage) => stage.status === "Running") ?? job.stages.find((stage) => stage.status === "Pending");
        if (running) setActiveStep(STEP_ORDER[running.position - 1]);
        setResult({ step: running ? STEP_ORDER[running.position - 1] : "vercel-live", ok: true,
          status: "pending", message: job.message || "Tahap deployment berjalan di server.", archive_id: archiveID });
        setElapsedSeconds(elapsed);
      });
      if (final.status === "Success") {
        setCompletedSteps([...STEP_ORDER]);
        setResult({ step: "done", ok: true, status: "ready", message: final.message || "Aplikasi sudah online di Vercel.", archive_id: archiveID });
        onSuccess();
      } else {
        const stage = final.stages.find((item) => item.status === "Failed" || item.status === "Running");
        currentPhase = STEP_ORDER[(stage?.position ?? 4) - 1];
        setFailedStep(currentPhase);
        setResult({ step: currentPhase, ok: false, message: final.message || "Deployment terhenti; periksa Pipeline, GitHub, dan Vercel.", archive_id: archiveID });
      }
    } catch (err) {
      if (!archiveID) setFailedStep(currentPhase);
      setError(err instanceof Error ? err.message : "Terjadi kesalahan tak terduga.");
    } finally {
      setActiveStep(null);
      setSubmitting(false);
    }
  }

  const finished = result?.step === "done" && result.ok;

  return (
    <Modal title={isUpdate ? "Update Aplikasi" : "Aplikasi Baru"} hidden={hidden} onHide={submitting ? onHide : undefined} onClose={submitting ? onHide : onClose}>
      <form onSubmit={handleSubmit} className="space-y-4">
        <p className="text-xs text-slate-500">
          {isUpdate
            ? "Setelah ZIP tersimpan, server otomatis menguji build, memperbarui GitHub, lalu Vercel. Versi sebelumnya tetap tersedia jika update gagal."
            : "Setelah ZIP tersimpan, server otomatis menguji build, membuat repo GitHub, lalu menayangkan aplikasi di Vercel."}
        </p>

        {isUpdate ? (
          <div>
            <label className="mb-1.5 block text-xs font-medium text-slate-400">Aplikasi yang diperbarui</label>
            <select
              value={name}
              onChange={(e) => setName(e.target.value)}
              disabled={submitting || finished}
              className="w-full rounded-xl border border-base-border bg-base-850 px-3 py-2.5 text-sm text-slate-200 focus:border-accent-blue/60 disabled:opacity-60"
            >
              <option value="">Pilih aplikasi...</option>
              {services.data.filter((s) => s.repo).map((s) => (
                <option key={s.id} value={s.name}>
                  {s.name} · v{s.version}
                </option>
              ))}
            </select>
          </div>
        ) : (
          <div>
            <label className="mb-1.5 block text-xs font-medium text-slate-400">Nama aplikasi</label>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              disabled={submitting || finished}
              placeholder="mis. customer-portal"
              className="w-full rounded-xl border border-base-border bg-base-850 px-3 py-2.5 text-sm text-slate-200 placeholder:text-slate-500 focus:border-accent-blue/60 disabled:opacity-60"
            />
          </div>
        )}

        <div>
          <label className="mb-1.5 block text-xs font-medium text-slate-400">File .zip</label>
          <label
            className={`flex items-center gap-3 rounded-xl border border-dashed border-base-border bg-base-850 px-3 py-3 text-sm text-slate-400 hover:border-accent-blue/60 ${
              submitting || finished ? "pointer-events-none opacity-60" : "cursor-pointer"
            }`}
          >
            <UploadCloud size={18} className="shrink-0 text-accent-blue" />
            <span className="truncate">{zipFile ? zipFile.name : "Pilih file .zip untuk diunggah"}</span>
            <input
              type="file"
              accept=".zip"
              className="hidden"
              disabled={submitting || finished}
              onChange={(e) => setZipFile(e.target.files?.[0] ?? null)}
            />
          </label>
        </div>

        <div>
          <div>
            <label className="mb-1.5 block text-xs font-medium text-slate-400">
              Branch <span className="text-slate-600">(opsional)</span>
            </label>
            <input
              value={branch}
              onChange={(e) => setBranch(e.target.value)}
              disabled={submitting || finished}
              placeholder="main"
              className="w-full rounded-xl border border-base-border bg-base-850 px-3 py-2.5 text-sm text-slate-200 placeholder:text-slate-500 focus:border-accent-blue/60 disabled:opacity-60"
            />
          </div>
        </div>

        <div className="space-y-2 rounded-xl border border-base-border bg-base-850/60 p-3">
          {STEPS.map((s) => {
            const state = stepState(s.key);
            return (
              <div key={s.key} className="flex items-center gap-2 text-sm">
                {state === "done" && <CheckCircle2 size={16} className="text-emerald-400" />}
                {state === "failed" && <XCircle size={16} className="text-red-400" />}
                {state === "active" && <Loader2 size={16} className="animate-spin text-accent-blue" />}
                {state === "pending" && <Circle size={16} className="text-slate-600" />}
                <span
                  className={
                    state === "done"
                      ? "text-emerald-400"
                      : state === "failed"
                      ? "text-red-400"
                      : state === "active"
                      ? "text-slate-200"
                      : "text-slate-500"
                  }
                >
                  {s.label}
                </span>
              </div>
            );
          })}
        </div>

        {result && (
          <p role="status" aria-live="polite" className={`text-xs font-medium ${result.ok ? result.status === "pending" ? "text-accent-blue" : "text-emerald-400" : "text-red-400"}`}>
            {result.message}{result.status === "pending" && ` · ${Math.floor(elapsedSeconds / 60)}m ${elapsedSeconds % 60}s`}
          </p>
        )}
        {submitting && result?.status === "pending" && (
          <p className="text-xs text-slate-500">ZIP sudah tersimpan. Laptop boleh ditutup; runner Cloudflare melanjutkan proses. Cek hasilnya di Pipeline.</p>
        )}
        {finished && result?.app_url && (
          <a href={result.app_url} target="_blank" rel="noopener noreferrer" className="block break-all text-xs text-accent-blue hover:underline">
            Buka aplikasi: {result.app_url}
          </a>
        )}
        {error && <p className="text-xs font-medium text-red-400">{error}</p>}
        {archiveSaved && (
          <Link href="/projects#zip-archives" className="text-xs font-medium text-accent-blue hover:underline">
            Lihat dan unduh ZIP tersimpan di Projects
          </Link>
        )}

        <div className="flex justify-end gap-2 pt-1">
          <button
            type="button"
            onClick={onClose}
            disabled={submitting && !archiveSaved}
            className="rounded-xl border border-base-border px-3.5 py-2 text-sm font-medium text-slate-300 hover:bg-base-800 disabled:opacity-60"
          >
            {submitting && archiveSaved ? "Sembunyikan" : finished ? "Tutup" : archiveSaved ? "Tutup" : "Batal"}
          </button>
          {!finished && !archiveSaved && (
            <button
              type="submit"
              disabled={submitting}
              className="flex items-center gap-2 rounded-xl bg-accent-blue px-3.5 py-2 text-sm font-semibold text-white hover:bg-blue-500 disabled:opacity-60"
            >
              {submitting && <Loader2 size={14} className="animate-spin" />}
              {submitting ? "Memproses..." : isUpdate ? "Update Sekarang" : "Deploy Sekarang"}
            </button>
          )}
        </div>
      </form>
    </Modal>
  );
}
