"use client";

import { Activity, ChevronDown, Cpu, Gauge, Wifi } from "lucide-react";
import Sparkline from "./Sparkline";
import type { InfraMetric } from "@/lib/types";

const META: Record<
  InfraMetric["metric"],
  { label: string; icon: JSX.Element; color: string; dot: string; suffix: string }
> = {
  cpu: { label: "CPU", icon: <Cpu size={14} />, color: "#3B82F6", dot: "bg-accent-blue", suffix: "%" },
  memory: { label: "Memory", icon: <Gauge size={14} />, color: "#A855F7", dot: "bg-accent-purple", suffix: "%" },
  network: { label: "Network", icon: <Wifi size={14} />, color: "#22D3EE", dot: "bg-accent-cyan", suffix: "%" },
  requests: { label: "Requests", icon: <Activity size={14} />, color: "#22C55E", dot: "bg-emerald-400", suffix: "/s" },
};

export default function InfraHealth({ metrics }: { metrics: InfraMetric[] }) {
  return (
    <div className="card p-4 sm:p-6">
      <div className="flex items-center justify-between">
        <h2 className="text-base font-bold text-white sm:text-lg">Infrastructure Health</h2>
        <button className="flex items-center gap-1 rounded-lg border border-base-border bg-base-850 px-2.5 py-1.5 text-xs font-medium text-slate-300">
          Last 24 hours <ChevronDown size={12} />
        </button>
      </div>

      <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {metrics.map((m) => {
          const meta = META[m.metric];
          const displayValue = m.metric === "requests" ? `${(m.current / 10).toFixed(1)}K` : m.current;
          return (
            <div key={m.metric} className="rounded-xl border border-base-border/70 p-3">
              <div className="flex items-center justify-between">
                <span className="flex items-center gap-1.5 text-sm font-medium text-slate-300">
                  <span className={`h-2 w-2 rounded-full ${meta.dot}`} />
                  {meta.label}
                </span>
                <span className="text-sm font-bold text-white">
                  {displayValue}
                  {m.metric !== "requests" && meta.suffix}
                </span>
              </div>
              <div className="mt-3 h-14">
                <Sparkline values={m.values} color={meta.color} height={56} width={220} />
              </div>
              <div className="mt-1 flex justify-between text-[10px] text-slate-600">
                <span>00:00</span>
                <span>06:00</span>
                <span>12:00</span>
                <span>18:00</span>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
