"use client";

import { Check, ChevronRight, Database, KeyRound, Package, User } from "lucide-react";
import type { ActivityItem } from "@/lib/types";

const ICON_BY_TYPE: Record<string, JSX.Element> = {
  check: <Check size={15} />,
  key: <KeyRound size={15} />,
  database: <Database size={15} />,
  box: <Package size={15} />,
  user: <User size={15} />,
};

const COLOR_BY_TYPE: Record<string, string> = {
  check: "bg-emerald-400/15 text-emerald-400",
  key: "bg-accent-blue/15 text-accent-blue",
  database: "bg-accent-cyan/15 text-accent-cyan",
  box: "bg-accent-purple/15 text-accent-purple",
  user: "bg-slate-500/15 text-slate-300",
};

export default function RecentActivity({ items }: { items: ActivityItem[] }) {
  return (
    <div className="card min-w-0 p-4 sm:p-6">
      <div className="flex items-center justify-between">
        <h2 className="text-base font-bold text-white sm:text-lg">Recent Activity</h2>
        <button className="flex items-center gap-1 text-sm font-medium text-accent-blue hover:text-blue-400">
          View All <ChevronRight size={14} />
        </button>
      </div>

      <ul className="mt-4 space-y-4">
        {items.map((a) => (
          <li key={a.id} className="flex items-start gap-3">
            <span className={`mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-full ${COLOR_BY_TYPE[a.icon] ?? COLOR_BY_TYPE.box}`}>
              {ICON_BY_TYPE[a.icon] ?? ICON_BY_TYPE.box}
            </span>
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium text-slate-100">{a.title}</p>
              <p className="truncate text-xs text-slate-500">{a.description}</p>
            </div>
            <span className="shrink-0 text-xs text-slate-500">{a.created_at}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
