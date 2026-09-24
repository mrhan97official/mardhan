"use client";

import { useEffect } from "react";
import { Check, Loader2, X, Clock3, Activity } from "lucide-react";
import type { PipelineStage } from "@/lib/types";
import { fallbackPipeline } from "@/lib/fallbackData";
import NewDeploymentMenu from "@/components/deployment/NewDeploymentMenu";

const ICON_BY_STATUS: Record<string, JSX.Element> = {
  Success: <Check size={18} strokeWidth={3} />,
  Running: <Loader2 size={18} className="animate-spin" />,
  Failed: <X size={18} strokeWidth={3} />,
  Pending: <span className="h-2 w-2 rounded-full bg-slate-500" />,
};

const RING_BY_STATUS: Record<string, string> = {
  Success: "border-emerald-400 text-emerald-400 shadow-[0_0_0_4px_rgba(34,197,94,0.12)]",
  Running: "border-purple-400 text-purple-400 shadow-[0_0_0_4px_rgba(168,85,247,0.15)]",
  Failed: "border-red-400 text-red-400 shadow-[0_0_0_4px_rgba(239,68,68,0.12)]",
  Pending: "border-slate-600 text-slate-500",
};

const LINE_BY_STATUS: Record<string, string> = {
  Success: "bg-emerald-400/60",
  Running: "bg-gradient-to-r from-emerald-400/60 to-purple-400/60",
  Failed: "bg-red-400/60",
  Pending: "bg-base-border",
};

export default function DeploymentPipeline({
  stages,
  onDeployed,
  loading = false,
  error = null,
  isOffline = false,
}: {
  stages: PipelineStage[];
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

  const visibleStages = stages.length > 0 ? stages : fallbackPipeline;
  const statusLabel = error ? "Status terganggu" : loading ? "Memuat" : isOffline ? "Offline" : stages.length === 0 ? "Belum tersedia" : "Live";

  return (
    <div className="card p-4 sm:p-6">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h2 className="text-base font-bold text-white sm:text-lg">Deployment Pipeline</h2>
          <p className="mt-1 text-xs text-slate-500">Real-time deployment tracking</p>
        </div>
        <div className="flex items-center gap-2">
          <span className="inline-flex items-center gap-1 rounded-full border border-base-border px-2 py-1 text-xs text-slate-400">
            <Activity size={12} /> {statusLabel}
          </span>
          <NewDeploymentMenu />
        </div>
      </div>

      {(error || (!loading && stages.length === 0)) && (
        <div role="status" className="mt-4 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-200">
          {error ? `Gagal memuat status terbaru: ${error}.` : "Tahap pipeline belum tersimpan di database."}
          {onDeployed && (
            <button type="button" onClick={onDeployed} className="ml-2 font-semibold text-accent-blue hover:underline">Coba lagi</button>
          )}
        </div>
      )}
      <div className="mt-6 flex flex-col gap-6 sm:flex-row sm:items-start sm:justify-between sm:gap-2">
        {visibleStages.map((s, i) => (
          <div
            key={s.id}
            className="flex flex-1 items-center gap-4 sm:flex-col sm:items-center sm:gap-0 sm:text-center"
          >
            <div className="flex items-center sm:contents">
              <div
                className={`flex h-12 w-12 shrink-0 items-center justify-center rounded-full border-2 bg-base-850 ${RING_BY_STATUS[s.status]}`}
              >
                {ICON_BY_STATUS[s.status]}
              </div>
              {i < visibleStages.length - 1 && (
                <div
                  className={`hidden h-0.5 flex-1 sm:block ${LINE_BY_STATUS[s.status]}`}
                  style={{ marginTop: "-1px" }}
                />
              )}
            </div>
            <div className="sm:mt-3">
              <p className="text-sm font-semibold text-slate-100">{s.stage}</p>
              <p className="text-xs text-slate-500">
                <Clock3 size={11} className="mr-1 inline" />
                {s.status === "Running" ? "Deploying..." : s.duration}
              </p>
              {s.message && <p className="mt-1 max-w-40 text-[11px] text-slate-500">{s.message}</p>}
              <p
                className={`mt-1 inline-flex items-center gap-1 text-xs font-medium ${
                  s.status === "Success"
                    ? "text-emerald-400"
                    : s.status === "Running"
                    ? "text-purple-400"
                    : s.status === "Failed"
                    ? "text-red-400"
                    : "text-slate-500"
                }`}
              >
                <span className="h-1.5 w-1.5 rounded-full bg-current" />
                {s.status === "Running" ? s.duration : s.status}
              </p>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
