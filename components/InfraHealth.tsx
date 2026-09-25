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
    maximumFractionDigits: metric.metric === "requests" ? 0 : metric.source === "DevControl · API" ? 3 : 1,
    ...(metric.metric === "requests" && metric.current >= 10_000 ? { notation: "compact" as const } : {}),
  }).format(metric.current);
  return `${amount}${metric.unit === "%" ? "%" : ` ${metric.unit}`}`;
}

export default function InfraHealth({ metrics }: { metrics: InfraMetric[] }) {
  const [history, setHistory] = useState<Record<string, number[]>>({});

  useEffect(() => {
    const now = Date.now();
    const stored = JSON.parse(localStorage.getItem("devcontrol-infra-history") || "{}") as Record<string, { t: number; v: number }[]>;
    const next: Record<string, { t: number; v: number }[]> = { ...stored };
    metrics.forEach((m) => {
      if (typeof m.current === "number") {
        next[m.metric] = [...(next[m.metric] || []), { t: now, v: m.current }].filter((x) => now - x.t <= 24 * 60 * 60 * 1000).slice(-288);
      }
    });
    localStorage.setItem("devcontrol-infra-history", JSON.stringify(next));
    setHistory(Object.fromEntries(Object.entries(next).map(([k, v]) => [k, v.map((x) => x.v)])));
  }, [metrics]);

  return (
    <div className="card min-w-0 p-4 md:p-3 xl:p-6">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-sm font-bold text-white sm:text-base">Infrastructure Health</h2>
        <span className="text-right text-[9px] text-slate-500">Go · {metrics.some((m) => m.source === "DevControl · API") ? "API" : "Cloudflare"}</span>
      </div>

      <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2 md:gap-3 xl:grid-cols-4 xl:gap-4">
        {metrics.map((m) => {
          const meta = META[m.metric];
          const hasSeries = Array.isArray(m.values) && m.values.length > 1 && m.current !== null;
          const sparkValues = hasSeries ? m.values : history[m.metric]?.length > 1 ? history[m.metric] : m.current !== null ? [m.current, m.current] : null;
          return (
            <div key={m.metric} className="min-w-0 rounded-xl border border-base-border/70 p-3 md:p-2 xl:p-3">
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
                {sparkValues
                  ? <Sparkline values={sparkValues} color={meta.color} height={56} width={220} />
                  : <span className="text-[11px] text-slate-500 md:text-[8px] xl:text-[11px]">Belum tersedia</span>}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
