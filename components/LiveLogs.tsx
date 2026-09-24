"use client";

import { useState } from "react";
import { ChevronDown, Maximize2 } from "lucide-react";
import type { LiveLog } from "@/lib/types";

const FILTERS = ["All", "Info", "Warning", "Error"] as const;
type Filter = (typeof FILTERS)[number];

const LEVEL_COLOR: Record<LiveLog["level"], string> = {
  INFO: "bg-accent-blue/20 text-accent-blue",
  WARN: "bg-amber-400/20 text-amber-400",
  ERROR: "bg-red-400/20 text-red-400",
};

const MESSAGE_COLOR: Record<LiveLog["level"], string> = {
  INFO: "text-slate-300",
  WARN: "text-amber-300",
  ERROR: "text-red-300",
};

function matches(filter: Filter, level: LiveLog["level"]) {
  if (filter === "All") return true;
  if (filter === "Info") return level === "INFO";
  if (filter === "Warning") return level === "WARN";
  return level === "ERROR";
}

export default function LiveLogs({ logs }: { logs: LiveLog[] }) {
  const [filter, setFilter] = useState<Filter>("All");
  const visible = logs.filter((l) => matches(filter, l.level));

  return (
    <div className="card flex flex-col p-4 sm:p-6">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-base font-bold text-white sm:text-lg">Live Logs</h2>
        <button className="rounded-lg border border-base-border bg-base-850 p-1.5 text-slate-400 hover:text-slate-200">
          <Maximize2 size={14} />
        </button>
      </div>

      <div className="mt-3 flex flex-wrap items-center gap-2">
        <div className="flex flex-wrap gap-1 rounded-lg border border-base-border bg-base-850 p-1">
          {FILTERS.map((f) => (
            <button
              key={f}
              onClick={() => setFilter(f)}
              className={`rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
                filter === f ? "bg-accent-blue text-white" : "text-slate-400 hover:text-slate-200"
              }`}
            >
              {f}
            </button>
          ))}
        </div>
        <button className="ml-auto flex items-center gap-1 rounded-lg border border-base-border bg-base-850 px-2.5 py-1.5 text-xs font-medium text-slate-300">
          Last 1 hour <ChevronDown size={12} />
        </button>
      </div>

      <div className="scroll-x mt-3 max-h-64 flex-1 overflow-y-auto rounded-xl bg-base-950/60 p-3 font-mono text-xs leading-relaxed">
        {visible.length === 0 && (
          <p className="text-slate-600">Tidak ada log untuk filter ini.</p>
        )}
        {visible.map((log) => (
          <div key={log.id} className="flex gap-2 whitespace-nowrap py-0.5">
            <span className="text-slate-600">{log.created_at}</span>
            <span className={`rounded px-1.5 font-semibold ${LEVEL_COLOR[log.level]}`}>
              {log.level}
            </span>
            <span className={MESSAGE_COLOR[log.level]}>{log.message}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
