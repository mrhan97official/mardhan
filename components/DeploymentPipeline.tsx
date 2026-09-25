"use client";

import { useEffect } from "react";
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

function JobStages({ stages, interrupted = false, inactive = false }: {
  stages: PipelineStage[];
  interrupted?: boolean;
  inactive?: boolean;
}) {
  return (
    <div className={`mt-5 grid grid-cols-2 gap-4 sm:grid-cols-4 ${inactive ? "opacity-60" : ""}`}>
      {stages.map((stage) => {
        const status: StageStatus = interrupted && stage.status === "Running" ? "Interrupted" : stage.status;
        return (
          <div key={stage.id} className="flex items-start gap-2 sm:flex-col sm:items-center sm:text-center">
            <div className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-full border-2 bg-base-850 ${RING_BY_STATUS[status]}`}>
              {ICON_BY_STATUS[status]}
            </div>
            <div className="min-w-0 sm:mt-2">
              <p className="text-xs font-semibold text-slate-100">{stage.stage}</p>
              <p className={`mt-1 text-xs ${status === "Failed" ? "text-red-400" : status === "Interrupted" ? "text-amber-400" : status === "Running" ? "text-purple-400" : "text-slate-500"}`}>
                {inactive ? "Tidak aktif" : status === "Running" ? "Berjalan..." : status === "Interrupted" ? "Terputus" : status === "Pending" ? "Menunggu" : stage.duration}
              </p>
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
  loading = false,
  error = null,
  isOffline = false,
}: {
  jobs: DeploymentJob[];
  limit?: number;
  onDeployed?: () => void;
  loading?: boolean;
  error?: string | null;
  isOffline?: boolean;
}) {
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
          <section className="rounded-xl border border-base-border bg-base-900 p-4" aria-label="Pipeline belum aktif">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="min-w-0">
                <p className="text-sm font-semibold text-slate-100">
                  {loading ? "Memuat deployment..." : error || isOffline ? "Status belum tersedia" : "Belum ada deployment"}
                </p>
                <p className="mt-1 text-[11px] text-slate-500">Tahapan deployment siap digunakan</p>
              </div>
              <span className="rounded-full border border-slate-700 px-2 py-1 text-xs font-medium text-slate-500">Tidak aktif</span>
            </div>
            <JobStages stages={fallbackPipeline} inactive />
          </section>
        )}
        {visibleJobs.map((job) => (
          <section key={job.id} className="rounded-xl border border-base-border bg-base-900 p-4" aria-label={`${KIND_TEXT[job.kind]} ${job.target}`}>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="min-w-0">
                <p className="text-sm font-semibold text-slate-100">{KIND_TEXT[job.kind]} · <span className="break-all">{job.target}</span></p>
                <p className="mt-1 text-[11px] text-slate-500"><Clock3 size={11} className="mr-1 inline" />{job.created_at} UTC</p>
              </div>
              <span className={`rounded-full border px-2 py-1 text-xs font-medium ${job.status === "Running" ? "border-purple-400/40 text-purple-400" : job.status === "Success" ? "border-emerald-400/40 text-emerald-400" : job.status === "Failed" ? "border-red-400/40 text-red-400" : "border-amber-400/40 text-amber-400"}`}>
                {job.status === "Running" && <Loader2 size={12} className="mr-1 inline animate-spin" />}{STATUS_TEXT[job.status]}
              </span>
            </div>
            <JobStages stages={job.stages} interrupted={job.status === "Interrupted"} />
          </section>
        ))}
      </div>
    </div>
  );
}
