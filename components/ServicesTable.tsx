"use client";

import { ChevronRight, Gauge, KeyRound, HardDrive, Layers3, MoreHorizontal, Rows3 } from "lucide-react";
import type { Service } from "@/lib/types";
import StatusBadge from "./StatusBadge";

const ICON_BY_NAME: Record<string, JSX.Element> = {
  "Auth Service": <KeyRound size={15} />,
  "API Gateway": <Gauge size={15} />,
  Database: <Layers3 size={15} />,
  Storage: <HardDrive size={15} />,
  Queue: <Rows3 size={15} />,
};

export default function ServicesTable({ services }: { services: Service[] }) {
  return (
    <div className="card flex h-full min-w-0 flex-col p-4 sm:p-6">
      <div className="flex items-center justify-between">
        <h2 className="text-base font-bold text-white sm:text-lg">Services</h2>
        <button className="flex items-center gap-1 text-sm font-medium text-accent-blue hover:text-blue-400">
          View All <ChevronRight size={14} />
        </button>
      </div>

      <div className="scroll-x mt-4 max-h-64 flex-1 overflow-y-auto">
        <table className="w-full min-w-[420px] text-left text-sm">
          <thead>
            <tr className="text-xs uppercase tracking-wide text-slate-500">
              <th className="pb-2 font-medium">Service</th>
              <th className="pb-2 font-medium">Status</th>
              <th className="pb-2 font-medium">Uptime</th>
              <th className="pb-2 font-medium">Version</th>
              <th className="pb-2" />
            </tr>
          </thead>
          <tbody className="divide-y divide-base-border/70">
            {services.map((s) => (
              <tr key={s.id} className="text-slate-200">
                <td className="py-3 pr-2">
                  <span className="flex items-center gap-2 font-medium">
                    <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-base-800 text-slate-400">
                      {ICON_BY_NAME[s.name] ?? <Layers3 size={15} />}
                    </span>
                    {s.name}
                  </span>
                </td>
                <td className="py-3 pr-2">
                  <StatusBadge status={s.status} />
                </td>
                <td className="py-3 pr-2 text-slate-400">{s.uptime}%</td>
                <td className="py-3 pr-2 text-slate-400">{s.version}</td>
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
