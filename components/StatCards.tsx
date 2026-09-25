"use client";

import { AlertTriangle, Boxes, FolderOpen, TrendingDown, TrendingUp, Activity } from "lucide-react";
import Sparkline from "./Sparkline";
import type { OverviewStats } from "@/lib/types";
import { summarySparklines } from "@/lib/fallbackData";

function ChangeTag({ value, invert = false }: { value: number; invert?: boolean }) {
  const positive = invert ? value < 0 : value >= 0;
  const Icon = value >= 0 ? TrendingUp : TrendingDown;
  return (
    <span
      className={`inline-flex items-center gap-1 text-xs font-semibold ${
        positive ? "text-emerald-400" : "text-red-400"
      }`}
    >
      <Icon size={12} />
      {value >= 0 ? "+" : ""}
      {value}%
    </span>
  );
}

export default function StatCards({ stats }: { stats: OverviewStats }) {
  const cards = [
    {
      label: "Active Projects",
      value: stats.active_projects,
      change: stats.active_projects_change,
      icon: FolderOpen,
      color: "#3B82F6",
      iconBg: "bg-accent-blue/15 text-accent-blue",
      sub: "vs. bulan lalu",
      spark: summarySparklines.projects,
    },
    {
      label: "Deployments Today",
      value: stats.deployments_today,
      change: stats.deployments_change,
      icon: Boxes,
      color: "#A855F7",
      iconBg: "bg-accent-purple/15 text-accent-purple",
      sub: "vs. kemarin",
      spark: summarySparklines.deployments,
    },
    {
      label: "Uptime",
      value: `${stats.uptime}%`,
      change: stats.uptime_change,
      icon: Activity,
      color: "#22D3EE",
      iconBg: "bg-accent-cyan/15 text-accent-cyan",
      sub: "vs. bulan lalu",
      spark: summarySparklines.uptime,
    },
    {
      label: "Open Incidents",
      value: stats.open_incidents,
      change: stats.incidents_change,
      icon: AlertTriangle,
      color: "#EF4444",
      iconBg: "bg-accent-red/15 text-accent-red",
      sub: "vs. minggu lalu",
      spark: summarySparklines.incidents,
      invert: true,
    },
  ];

  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 md:grid-cols-4 md:gap-3 xl:gap-4">
      {cards.map((c) => (
        <div key={c.label} className="card p-4 md:p-3 xl:p-4">
          <div className="flex min-w-0 items-center gap-2.5 md:min-h-11 xl:gap-3">
            <div className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-xl ${c.iconBg}`}>
              <c.icon size={18} />
            </div>
            <div className="min-w-0">
              <p className="text-xs font-semibold leading-4 text-slate-200 xl:text-sm">{c.label}</p>
              <p className="mt-0.5 text-[11px] leading-4 text-slate-500">{c.sub}</p>
            </div>
          </div>
          <div className="mt-3 flex flex-wrap items-baseline justify-between gap-x-2 gap-y-1">
            <p className="text-2xl font-bold leading-none text-white md:text-[22px] xl:text-[28px]">{c.value}</p>
            <ChangeTag value={c.change} invert={c.invert} />
          </div>
          <div className="mt-2 -mb-1 h-7">
            <Sparkline values={c.spark} color={c.color} height={28} width={140} />
          </div>
        </div>
      ))}
    </div>
  );
}
