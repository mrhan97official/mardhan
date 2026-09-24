"use client";

import { ChevronRight, MoreHorizontal } from "lucide-react";
import type { Environment } from "@/lib/types";
import StatusBadge from "./StatusBadge";

const DOT_COLOR: Record<string, string> = {
  Development: "bg-accent-blue",
  Staging: "bg-accent-purple",
  Production: "bg-emerald-400",
};

export default function EnvironmentStatus({ environments }: { environments: Environment[] }) {
  return (
    <div className="card min-w-0 p-4 sm:p-6">
      <div className="flex items-center justify-between">
        <h2 className="text-base font-bold text-white sm:text-lg">Environment Status</h2>
        <button className="flex items-center gap-1 text-sm font-medium text-accent-blue hover:text-blue-400">
          View All <ChevronRight size={14} />
        </button>
      </div>

      <div className="scroll-x mt-4">
        <table className="w-full min-w-[420px] text-left text-sm">
          <thead>
            <tr className="text-xs uppercase tracking-wide text-slate-500">
              <th className="pb-2 font-medium">Environment</th>
              <th className="pb-2 font-medium">Region</th>
              <th className="pb-2 font-medium">Version</th>
              <th className="pb-2 font-medium">Status</th>
              <th className="pb-2" />
            </tr>
          </thead>
          <tbody className="divide-y divide-base-border/70">
            {environments.map((env) => (
              <tr key={env.id} className="text-slate-200">
                <td className="py-3 pr-2">
                  <span className="flex items-center gap-2 font-medium">
                    <span className={`h-2 w-2 rounded-full ${DOT_COLOR[env.name] ?? "bg-slate-500"}`} />
                    {env.name}
                  </span>
                </td>
                <td className="py-3 pr-2 text-slate-400">{env.region}</td>
                <td className="py-3 pr-2 text-slate-400">{env.version}</td>
                <td className="py-3 pr-2">
                  <StatusBadge status={env.status} />
                </td>
                <td className="py-3 text-right text-slate-500">
                  <MoreHorizontal size={16} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
