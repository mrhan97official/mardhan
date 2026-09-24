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
        <div key={c.label} className="card p-4 sm:p-5">
          <div className="flex items-start justify-between">
            <div className={`flex h-9 w-9 items-center justify-center rounded-xl ${c.iconBg}`}>
              <c.icon size={18} />
            </div>
          </div>
          <p className="mt-3 text-sm text-slate-400">{c.label}</p>
          <p className="mt-1 text-2xl font-bold text-white sm:text-[28px]">{c.value}</p>
          <div className="mt-2 flex items-center gap-2">
            <ChangeTag value={c.change} invert={c.invert} />
            <span className="text-xs text-slate-500">{c.sub}</span>
          </div>
          <div className="mt-3 -mb-1 h-10">
            <Sparkline values={c.spark} color={c.color} height={40} width={140} />
          </div>
        </div>
      ))}
    </div>
  );
}
