"use client";

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
    maximumFractionDigits: metric.metric === "requests" ? 0 : 1,
    ...(metric.metric === "requests" && metric.current >= 10_000 ? { notation: "compact" as const } : {}),
  }).format(metric.current);
  return `${amount}${metric.unit === "%" ? "%" : ` ${metric.unit}`}`;
}

export default function InfraHealth({ metrics }: { metrics: InfraMetric[] }) {
  return (
    <div className="card min-w-0 p-4 md:p-3 xl:p-6">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-base font-bold text-white sm:text-lg">Infrastructure Health</h2>
        <span className="text-right text-[10px] text-slate-500">Go · Cloudflare</span>
      </div>

      <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2 md:grid-cols-4 md:gap-1 xl:gap-4">
        {metrics.map((m) => {
          const meta = META[m.metric];
          const hasSeries = Array.isArray(m.values) && m.values.length > 1 && m.current !== null;
          return (
            <div key={m.metric} className="min-w-0 rounded-xl border border-base-border/70 p-3 md:p-1 xl:p-3">
              <div className="flex items-center justify-between gap-1 md:flex-wrap xl:flex-nowrap">
                <span className="flex min-w-0 items-center gap-1 text-sm font-medium text-slate-300 md:text-[10px] xl:text-sm">
                  <span style={{ color: meta.color }}>{meta.icon}</span>
                  {meta.label}
                </span>
                <span className="min-w-0 text-right text-sm font-bold tabular-nums text-white md:text-[10px] xl:text-sm">
                  {formatValue(m)}
                </span>
              </div>
              <div className="mt-3 flex h-14 items-center">
                {hasSeries
                  ? <Sparkline values={m.values} color={meta.color} height={56} width={220} />
                  : <span className="text-xs text-slate-500 md:text-[9px] xl:text-xs">{m.current === null ? "Belum tersedia" : "Pengukuran langsung"}</span>}
              </div>
              <p className="mt-1 break-words text-[10px] leading-tight text-slate-400 md:text-[9px] xl:text-[10px]">{m.source}</p>
              <p className="mt-1 break-words text-[10px] leading-tight text-slate-500 md:text-[9px] xl:text-[10px]">{m.note}</p>
            </div>
          );
        })}
      </div>
    </div>
  );
}
