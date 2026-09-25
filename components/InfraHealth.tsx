"use client";

import { useEffect, useState } from "react";
import { Activity, Cpu, Gauge, Wifi } from "lucide-react";
import Sparkline from "./Sparkline";
import type { InfraMetric } from "@/lib/types";

const META: Record<
  InfraMetric["metric"],
  { label: string; icon: JSX.Element; color: string }
> = {
  cpu: { label: "CPU", icon: <Cpu size={14} />, color: "#3B82F6" },
  memory: { label: "Memory", icon: <Gauge size={14} />, color: "#A855F7" },
  network: { label: "Network", icon: <Wifi size={14} />, color: "#22D3EE" },
  requests: { label: "Requests", icon: <Activity size={14} />, color: "#22C55E" },
};

function formatValue(metric: InfraMetric) {
  if (typeof metric.current !== "number" || !Number.isFinite(metric.current)) return "—";
  const amount = new Intl.NumberFormat("id-ID", {
    maximumFractionDigits: metric.metric === "requests" ? 0 : metric.metric === "cpu" || metric.metric === "memory" ? 2 : metric.source === "DevControl · API" ? 3 : 1,
    ...(metric.metric === "requests" && metric.current >= 10_000 ? { notation: "compact" as const } : {}),
  }).format(metric.current);
  return `${amount}${metric.unit === "%" ? "%" : ` ${metric.unit}`}`;
}

type Sample = { time: number; value: number };
type History = Partial<Record<"cpu" | "memory", Sample[]>>;

export default function InfraHealth({ metrics, updatedAt, error }: { metrics: InfraMetric[]; updatedAt?: number | null; error?: string | null }) {
  const [history, setHistory] = useState<History>({});

  useEffect(() => {
    // Cached offline values are useful for the number, but cannot be new chart samples.
    if (!updatedAt || error || Date.now() - updatedAt > 30_000) return;
    setHistory((previous) => {
      const next = { ...previous };
      let changed = false;
      for (const metric of metrics) {
        if (metric.metric !== "cpu" && metric.metric !== "memory") continue;
        if (typeof metric.current !== "number" || !Number.isFinite(metric.current)) continue;
        const samples = previous[metric.metric] ?? [];
        if (samples[samples.length - 1]?.time === updatedAt) continue;
        next[metric.metric] = [...samples.filter((sample) => updatedAt - sample.time < 5 * 60_000), { time: updatedAt, value: metric.current }].slice(-30);
        changed = true;
      }
      return changed ? next : previous;
    });
  }, [metrics, updatedAt, error]);

  return (
    <div className="card min-w-0 p-4 md:p-3 xl:p-6">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-sm font-bold text-white sm:text-base">Infrastructure Health</h2>
        <span className="text-right text-[9px] text-slate-500">{error ? "Data terakhir tersimpan" : `Go · ${metrics.some((m) => m.source === "DevControl · API") ? "API" : "Cloudflare"}`}</span>
      </div>

      <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2 md:gap-3 xl:grid-cols-4 xl:gap-4">
        {metrics.map((m) => {
          const meta = META[m.metric];
          const sparkValues = m.metric === "cpu" || m.metric === "memory"
            ? history[m.metric]?.map((sample) => sample.value)
            : m.values;
          const series = sparkValues ?? [];
          const hasSeries = series.length > 1;
          return (
            <div key={m.metric} title={m.note} className="min-w-0 rounded-xl border border-base-border/70 p-3 md:p-2 xl:p-3">
              <div className="flex items-center justify-between gap-1 md:flex-wrap xl:flex-nowrap">
                <span className="flex min-w-0 items-center gap-1 text-xs font-medium text-slate-300 md:text-[9px] xl:text-xs">
                  <span style={{ color: meta.color }}>{meta.icon}</span>
                  {m.source === "DevControl · API" && m.metric === "network" ? "API Network" : m.source === "DevControl · API" && m.metric === "requests" ? "API Requests" : meta.label}
                </span>
                <span className="min-w-0 text-right text-xs font-bold tabular-nums text-white md:text-[9px] xl:text-xs">
                  {formatValue(m)}
                </span>
              </div>
              <div className="mt-3 flex h-14 items-center">
                {hasSeries
                  ? <Sparkline values={series} color={meta.color} height={56} width={220} />
                  : <span className="text-[11px] text-slate-500 md:text-[8px] xl:text-[11px]">{m.current === null ? "Belum tersedia" : "Menunggu sampel berikutnya"}</span>}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
