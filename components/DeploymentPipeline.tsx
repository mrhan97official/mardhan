"use client";

import { useEffect, useRef, useState } from "react";
import { Check, Loader2, X, Clock3, Stethoscope, XCircle } from "lucide-react";
import type { DeploymentJob, PipelineStage } from "@/lib/types";
import { fallbackPipeline } from "@/lib/fallbackData";
import NewDeploymentMenu from "@/components/deployment/NewDeploymentMenu";
import ErrorDiagnosis, { fixDirective } from "@/components/deployment/ErrorDiagnosis";

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

// Line between two stages. A finished stage flowing into a running one gets
// an animated green -> blue -> purple gradient (see .pipeline-flow).
function connectorClass(from: StageStatus, to: StageStatus): string {
  if (from !== "Success") return "bg-slate-600/40";
  if (to === "Success") return "bg-emerald-400/80";
  if (to === "Running") return "pipeline-flow";
  if (to === "Failed") return "bg-gradient-to-r from-emerald-400 to-red-400";
  if (to === "Interrupted") return "bg-gradient-to-r from-emerald-400 to-amber-400";
  return "bg-gradient-to-r from-emerald-400/70 to-slate-600/40";
}

function JobStages({ stages, job, inactive = false, now }: {
  stages: PipelineStage[];
  job?: DeploymentJob;
  inactive?: boolean;
  now: number | null;
}) {
  return (
    <div className={`grid grid-cols-4 gap-2 ${inactive ? "opacity-60" : "mt-2"}`}>
      {stages.map((stage, index) => {
        const statusOf = (item: PipelineStage): StageStatus => job?.status === "Interrupted" && item.status === "Running" ? "Interrupted" : item.status;
        const status = statusOf(stage);
        const next = stages[index + 1];
        return (
          <div key={stage.id} aria-label={`${stage.stage}: ${status}`} className="relative flex min-w-0 flex-col items-center text-center">
            {next && (
              // From just past this circle to just before the next one (circle 36px, gap 8px).
              <span aria-hidden="true" className={`absolute top-[16.5px] h-[3px] overflow-hidden rounded-full ${connectorClass(status, statusOf(next))}`}
                style={{ left: "calc(50% + 22px)", width: "calc(100% - 36px)" }} />
            )}
            <div className={`relative z-10 flex h-9 w-9 shrink-0 items-center justify-center rounded-full border-2 bg-base-850 ${RING_BY_STATUS[status]}`}>
              {ICON_BY_STATUS[status]}
            </div>
            <div className="mt-2 min-w-0">
              <p className="text-[11px] font-semibold leading-tight text-slate-100 sm:text-xs">{stage.stage}</p>
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
  scroll = false,
}: {
  jobs: DeploymentJob[];
  limit?: number;
  scroll?: boolean;
  onDeployed?: () => void;
  error?: string | null;
}) {
  const [now, setNow] = useState<number | null>(null);
  const [openDiagnosis, setOpenDiagnosis] = useState<string | null>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const cardRef = useRef<HTMLDivElement>(null);
  const seenFirstId = useRef<string | undefined>(undefined);
  const [freshId, setFreshId] = useState<string | null>(null);
  const [firstHeight, setFirstHeight] = useState<number | null>(null);
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

  const [dismissed, setDismissed] = useState<string[]>([]);
  const [confirmClose, setConfirmClose] = useState<string | null>(null);
  const [closing, setClosing] = useState<string | null>(null);
  const [closeError, setCloseError] = useState<{ id: string; message: string } | null>(null);
  const activeJobs = jobs.filter((job) => !dismissed.includes(job.id));
  const visibleJobs = limit ? activeJobs.slice(0, limit) : activeJobs;

  // Remove a failed run from the pipeline. The server discards its ZIP too
  // (the last successful ZIP stays), so nothing half-finished lingers.
  async function closeJob(id: string) {
    setClosing(id);
    setCloseError(null);
    try {
      const response = await fetch(`/api/deployments?id=${id}`, { method: "DELETE", credentials: "same-origin" });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error((body as { error?: string }).error || `HTTP ${response.status}`);
      setDismissed((current) => [...current, id]);
      setConfirmClose(null);
      onDeployed?.();
    } catch (reason) {
      setCloseError({ id, message: reason instanceof Error ? reason.message : "Proses gagal ditutup." });
    } finally { setClosing(null); }
  }
  const scrolling = scroll && visibleJobs.length > 1;
  const firstJobId = visibleJobs[0]?.id;

  // A new deployment appears on top: scroll the list (and the card, if it is
  // off screen) back to the top and highlight it so the start is noticed.
  useEffect(() => {
    const previous = seenFirstId.current;
    seenFirstId.current = firstJobId;
    if (!firstJobId || previous === undefined || previous === firstJobId) return;
    listRef.current?.scrollTo({ top: 0, behavior: "smooth" });
    cardRef.current?.scrollIntoView({ behavior: "smooth", block: "nearest" });
    setFreshId(firstJobId);
    const timer = window.setTimeout(() => setFreshId(null), 4000);
    return () => window.clearTimeout(timer);
  }, [firstJobId]);

  // With more than one job, show exactly one and scroll to the others so the
  // card never grows taller than a single job.
  useEffect(() => {
    const first = listRef.current?.firstElementChild as HTMLElement | null;
    if (!scrolling || !first) { setFirstHeight(null); return; }
    const measure = () => setFirstHeight(first.offsetHeight);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(first);
    return () => observer.disconnect();
  }, [scrolling, firstJobId]);
  return (
    <div ref={cardRef} className="card min-w-0 scroll-mt-2 p-2">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-base font-bold text-white sm:text-lg">Deployment Pipeline</h2>
        <NewDeploymentMenu />
      </div>

      {error && (
        <div role="status" className="mt-2 rounded-lg border border-amber-500/30 bg-amber-500/10 px-2 py-2 text-xs text-amber-200">
          Gagal memuat status terbaru: {error}.
          {onDeployed && <button type="button" onClick={onDeployed} className="ml-2 font-semibold text-accent-blue hover:underline">Coba lagi</button>}
        </div>
      )}
      {scrolling && <p className="mt-1 text-[11px] text-slate-500">{visibleJobs.length} proses · gulir untuk melihat lainnya</p>}
      <div ref={listRef} className={`mt-2 space-y-2 ${scrolling ? "snap-y snap-mandatory overflow-y-auto overscroll-contain pr-1" : ""}`}
        style={scrolling && firstHeight ? { maxHeight: firstHeight } : undefined}>
        {visibleJobs.length === 0 && (
          <section className="flex min-h-[clamp(136px,10rem,160px)] flex-col justify-center rounded-xl border border-base-border bg-base-900 p-2" aria-label="Tahapan deployment">
            <JobStages stages={fallbackPipeline} inactive now={now} />
          </section>
        )}
        {visibleJobs.map((job) => (
          <section key={job.id} className={`min-h-[clamp(136px,10rem,160px)] snap-start rounded-xl border bg-base-900 p-2 transition-shadow duration-700 ${freshId === job.id ? "border-purple-400/60 shadow-[0_0_0_3px_rgba(168,85,247,0.25)]" : "border-base-border"}`} aria-label={`${KIND_TEXT[job.kind]} ${job.target}`}>
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
            {(job.status === "Failed" || job.status === "Interrupted") && (
              <div className="mt-2 space-y-2">
                <div className="flex flex-wrap items-center gap-2">
                  <button type="button" onClick={() => setOpenDiagnosis((current) => current === job.id ? null : job.id)}
                    className="inline-flex items-center gap-1.5 rounded-lg border border-red-400/40 px-2.5 py-1.5 text-xs font-semibold text-red-300 hover:bg-red-500/10">
                    <Stethoscope size={13} /> {openDiagnosis === job.id ? "Tutup diagnosis" : "Lihat letak error & saran perbaikan"}
                  </button>
                  {job.diagnosis && (() => {
                    // Where to act, visible before the diagnosis is opened.
                    const directive = fixDirective(job.diagnosis.source, job.kind, job.diagnosis.log_missing);
                    return <span className={`rounded-full border px-2 py-1 text-[11px] font-semibold ${directive.chipTone}`}>{directive.chip}</span>;
                  })()}
                  {confirmClose === job.id ? (
                    <span className="inline-flex flex-wrap items-center gap-1.5 text-xs text-slate-300">
                      Hapus dari pipeline?
                      <button type="button" disabled={closing === job.id} onClick={() => void closeJob(job.id)}
                        className="inline-flex items-center gap-1 rounded-lg bg-red-500/80 px-2.5 py-1.5 font-semibold text-white hover:bg-red-500 disabled:opacity-50">
                        {closing === job.id && <Loader2 size={12} className="animate-spin" />} Ya, tutup
                      </button>
                      <button type="button" onClick={() => setConfirmClose(null)} className="rounded-lg px-2 py-1.5 text-slate-400 hover:bg-base-800">Batal</button>
                    </span>
                  ) : (
                    <button type="button" onClick={() => { setConfirmClose(job.id); setCloseError(null); }}
                      className="inline-flex items-center gap-1.5 rounded-lg border border-base-border px-2.5 py-1.5 text-xs font-semibold text-slate-300 hover:bg-base-800">
                      <XCircle size={13} /> Tutup proses
                    </button>
                  )}
                </div>
                {closeError?.id === job.id && <p role="alert" className="text-xs text-red-300">{closeError.message}</p>}
                {openDiagnosis === job.id && (
                  <ErrorDiagnosis jobId={job.id} diagnosis={job.diagnosis} message={job.message} kind={job.kind} target={job.target} />
                )}
              </div>
            )}
          </section>
        ))}
      </div>
    </div>
  );
}
