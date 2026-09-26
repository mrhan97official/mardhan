"use client";

import { AlertTriangle, Boxes, FolderOpen, Activity } from "lucide-react";
import Sparkline from "./Sparkline";
import type { OverviewStats } from "@/lib/types";
import { summarySparklines } from "@/lib/fallbackData";

// Older offline snapshots may contain the empty response from a fresh D1.
function numberOrZero(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function ChangeTag({ value, invert = false }: { value: number; invert?: boolean }) {
  const positive = invert ? value < 0 : value >= 0;
  return (
    <span
      className={`shrink-0 whitespace-nowrap text-[11px] font-semibold md:text-[10px] xl:text-xs ${
        positive ? "text-emerald-400" : "text-red-400"
      }`}
    >
      {value >= 0 ? "+" : ""}
      {value}%
    </span>
  );
}

export default function StatCards({ stats }: { stats: OverviewStats }) {
  const cards = [
    {
      label: "Active Projects",
      value: numberOrZero(stats.active_projects),
      change: numberOrZero(stats.active_projects_change),
      icon: FolderOpen,
      color: "#3B82F6",
      iconBg: "bg-accent-blue/15 text-accent-blue",
      sub: "vs. bulan lalu",
      spark: summarySparklines.projects,
    },
    {
      label: "Deployments Today",
      value: numberOrZero(stats.deployments_today),
      change: numberOrZero(stats.deployments_change),
      icon: Boxes,
      color: "#A855F7",
      iconBg: "bg-accent-purple/15 text-accent-purple",
      sub: "vs. kemarin",
      spark: summarySparklines.deployments,
    },
    {
      label: "Uptime",
      value: `${numberOrZero(stats.uptime)}%`,
      change: numberOrZero(stats.uptime_change),
      icon: Activity,
      color: "#22D3EE",
      iconBg: "bg-accent-cyan/15 text-accent-cyan",
      sub: "vs. bulan lalu",
      spark: summarySparklines.uptime,
    },
    {
      label: "Open Incidents",
      value: numberOrZero(stats.open_incidents),
      change: numberOrZero(stats.incidents_change),
      icon: AlertTriangle,
      color: "#EF4444",
      iconBg: "bg-accent-red/15 text-accent-red",
      sub: "vs. minggu lalu",
      spark: summarySparklines.incidents,
      invert: true,
    },
  ];

  return (
    <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 md:grid-cols-4">
      {cards.map((c) => (
        <div key={c.label} className="card p-2">
          <div className="flex min-w-0 items-center gap-2 md:min-h-[3.75rem] md:gap-2 xl:min-h-0">
            <div className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-xl md:h-8 md:w-8 xl:h-9 xl:w-9 ${c.iconBg}`}>
              <c.icon size={18} />
            </div>
            <div className="min-w-0 flex-1">
              <p className="break-words text-xs font-semibold leading-4 text-slate-200 md:text-[11px] xl:text-sm">{c.label}</p>
              <p className="mt-0.5 block whitespace-nowrap text-2xl font-bold leading-none tabular-nums text-white md:text-[22px] xl:text-[28px]">{c.value}</p>
            </div>
          </div>
          <div className="mt-2 flex min-w-0 items-center gap-1.5 md:mt-2">
            <div className="flex min-w-0 flex-1 items-center gap-1">
              <ChangeTag value={c.change} invert={c.invert} />
              <span className="min-w-0 truncate text-[11px] text-slate-500 md:text-[10px] xl:text-xs">{c.sub}</span>
            </div>
            <div className="h-7 w-16 shrink-0 sm:w-20 md:w-7 lg:w-12 xl:w-20">
              <Sparkline values={c.spark} color={c.color} height={28} width={140} />
            </div>
          </div>
        </div>
      ))}
    </div>
  );
}
