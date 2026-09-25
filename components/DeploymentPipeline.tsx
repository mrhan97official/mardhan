"use client";

import { useEffect, useState } from "react";
import { Check, Loader2, X, Clock3 } from "lucide-react";
import type { DeploymentJob, PipelineStage } from "@/lib/types";
import { fallbackPipeline } from "@/lib/fallbackData";
import NewDeploymentMenu from "@/components/deployment/NewDeploymentMenu";

type StageStatus = PipelineStage["status"] | "Interrupted";

const ICON_BY_STATUS: Record<StageStatus, JSX.Element> = {
  Success: <Check size={18} strokeWidth={3} />,
  Running: <Loader2 size={18} className="animate-spin" />,
  Failed: <X size={18} strokeWidth={3} />,
  Interrupted: <X size={18} strokeWidth={3} />,
  Pending: <span className="h-2 w-2 rounded-full bg-slate-500" />,
};

const RING_BY_STATUS: Record<StageStatus, string> = {
  Success: "border-emerald-400 text-emerald-400 shadow-[0_0_0_4px_rgba(34,197,94,0.12)]",
  Running: "border-purple-400 text-purple-400 shadow-[0_0_0_4px_rgba(168,85,247,0.15)]",
  Failed: "border-red-400 text-red-400 shadow-[0_0_0_4px_rgba(239,68,68,0.12)]",
  Interrupted: "border-amber-400 text-amber-400",
  Pending: "border-slate-600 text-slate-500",
};

const STATUS_TEXT: Record<DeploymentJob["status"], string> = {
  Running: "Berjalan",
  Success: "Berhasil",
  Failed: "Gagal",
  Interrupted: "Terputus",
};

const KIND_TEXT: Record<DeploymentJob["kind"], string> = {
  new_app: "Aplikasi Baru",
  update_app: "Update Aplikasi",
  self_update: "Update Diri",
};

function utcMillis(value: string): number | null {
  const parsed = Date.parse(value.includes("T") ? value : `${value.replace(" ", "T")}Z`);
  return Number.isFinite(parsed) ? parsed : null;
}

function stageTime(stage: PipelineStage, job: DeploymentJob, now: number | null): string {
  if (stage.status === "Pending") return "0m 0s";
  const started = stage.started_at
    ? stage.started_at * 1000
    : stage.position === 1 ? utcMillis(job.created_at) : null;
  if (started === null) return "—";

  const finished = stage.finished_at
    ? stage.finished_at * 1000
    : stage.status === "Running" && job.status === "Running" ? now
    : stage.status === "Running" && job.status === "Interrupted" ? utcMillis(job.updated_at)
    : null;
  if (finished === null) return "—";
  const elapsed = Math.max(0, Math.floor((finished - started) / 1000));
  return `${Math.floor(elapsed / 60)}m ${elapsed % 60}s`;
}

function JobStages({ stages, job, inactive = false, now }: {
  stages: PipelineStage[];
  job?: DeploymentJob;
  inactive?: boolean;
  now: number | null;
}) {
  return (
    <div className={`grid grid-cols-2 gap-4 sm:grid-cols-4 ${inactive ? "opacity-60" : "mt-5"}`}>
      {stages.map((stage) => {
        const status: StageStatus = job?.status === "Interrupted" && stage.status === "Running" ? "Interrupted" : stage.status;
        return (
          <div key={stage.id} aria-label={`${stage.stage}: ${status}`} className="flex items-start gap-2 sm:flex-col sm:items-center sm:text-center">
            <div className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-full border-2 bg-base-850 ${RING_BY_STATUS[status]}`}>
              {ICON_BY_STATUS[status]}
            </div>
            <div className="min-w-0 sm:mt-2">
              <p className="text-xs font-semibold text-slate-100">{stage.stage}</p>
              <time aria-live="off" className={`mt-1 block font-mono text-xs tabular-nums ${status === "Failed" ? "text-red-400" : status === "Interrupted" ? "text-amber-400" : status === "Running" ? "text-purple-400" : "text-slate-500"}`}>
                {job ? stageTime(stage, job, now) : "0m 0s"}
              </time>
            </div>
          </div>
        );
      })}
    </div>
  );
}

export default function DeploymentPipeline({
  jobs,
  limit,
  onDeployed,
  error = null,
}: {
  jobs: DeploymentJob[];
  limit?: number;
  onDeployed?: () => void;
  error?: string | null;
}) {
  const [now, setNow] = useState<number | null>(null);
  const hasRunningJob = jobs.some((job) => job.status === "Running");

  useEffect(() => {
    if (!hasRunningJob) return;
    const tick = () => setNow(Date.now());
    tick();
    const interval = window.setInterval(tick, 1000);
    document.addEventListener("visibilitychange", tick);
    return () => {
      window.clearInterval(interval);
      document.removeEventListener("visibilitychange", tick);
    };
  }, [hasRunningJob]);

  useEffect(() => {
    if (!onDeployed) return;
    window.addEventListener("deployment:changed", onDeployed);
    return () => window.removeEventListener("deployment:changed", onDeployed);
  }, [onDeployed]);

  const visibleJobs = limit ? jobs.slice(0, limit) : jobs;
  return (
    <div className="card min-w-0 p-4 sm:p-6">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-base font-bold text-white sm:text-lg">Deployment Pipeline</h2>
        <NewDeploymentMenu />
      </div>

      {error && (
        <div role="status" className="mt-4 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-200">
          Gagal memuat status terbaru: {error}.
          {onDeployed && <button type="button" onClick={onDeployed} className="ml-2 font-semibold text-accent-blue hover:underline">Coba lagi</button>}
        </div>
      )}
      <div className="mt-4 space-y-4">
        {visibleJobs.length === 0 && (
          <section className="flex min-h-[clamp(136px,10rem,160px)] flex-col justify-center rounded-xl border border-base-border bg-base-900 p-4" aria-label="Tahapan deployment">
            <JobStages stages={fallbackPipeline} inactive now={now} />
          </section>
        )}
        {visibleJobs.map((job) => (
          <section key={job.id} className="min-h-[clamp(136px,10rem,160px)] rounded-xl border border-base-border bg-base-900 p-4" aria-label={`${KIND_TEXT[job.kind]} ${job.target}`}>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="min-w-0">
                <p className="text-sm font-semibold text-slate-100">{KIND_TEXT[job.kind]} · <span className="break-all">{job.target}</span></p>
                <p className="mt-1 text-[11px] text-slate-500"><Clock3 size={11} className="mr-1 inline" />{job.created_at} UTC</p>
              </div>
              <span className={`rounded-full border px-2 py-1 text-xs font-medium ${job.status === "Running" ? "border-purple-400/40 text-purple-400" : job.status === "Success" ? "border-emerald-400/40 text-emerald-400" : job.status === "Failed" ? "border-red-400/40 text-red-400" : "border-amber-400/40 text-amber-400"}`}>
                {job.status === "Running" && <Loader2 size={12} className="mr-1 inline animate-spin" />}{STATUS_TEXT[job.status]}
              </span>
            </div>
            {job.message && <p className={`mt-2 text-xs ${job.status === "Failed" ? "text-red-400" : job.status === "Interrupted" ? "text-amber-400" : "text-slate-400"}`}>{job.message}</p>}
            <JobStages stages={job.stages} job={job} now={now} />
          </section>
        ))}
      </div>
    </div>
  );
}
