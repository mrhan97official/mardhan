"use client";

import Link from "next/link";
import { ChevronRight, ExternalLink, RefreshCw } from "lucide-react";
import type { Environment } from "@/lib/types";
import StatusBadge from "./StatusBadge";

const DOT_COLOR: Record<string, string> = {
  Development: "bg-accent-blue",
  Preview: "bg-accent-purple",
  Staging: "bg-accent-purple",
  Production: "bg-emerald-400",
};

export default function EnvironmentStatus({ environments, loading, error, updatedAt, viewAll = false, onRefresh }: {
  environments: Environment[];
  loading: boolean;
  error: string | null;
  updatedAt: number | null;
  viewAll?: boolean;
  onRefresh?: () => void;
}) {
  const visible = viewAll ? environments.slice(0, 3) : environments;

  return (
    <div className="card min-w-0 p-2">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-base font-bold text-white sm:text-lg">Environment Status</h2>
        {viewAll ? (
          <Link href="/environments" className="flex shrink-0 items-center gap-1 text-sm font-medium text-accent-blue hover:text-blue-400">
            View All <ChevronRight size={14} />
          </Link>
        ) : (
          <button type="button" onClick={onRefresh} disabled={loading} className="flex shrink-0 items-center gap-1 text-sm font-medium text-accent-blue hover:text-blue-400 disabled:opacity-50">
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} /> Segarkan
          </button>
        )}
      </div>
      <p className="mt-1 text-xs text-slate-500">Status build terbaru dari Vercel · bukan pemantauan uptime</p>
      {error && (
        <p role="alert" className="mt-2 rounded-lg bg-amber-400/10 px-2 py-2 text-xs text-amber-300">
          {environments.length ? "Data tersimpan; status terbaru belum terverifikasi. " : "Status tidak tersedia. "}{error}
        </p>
      )}

      <div className="scroll-x mt-2">
        <table className="w-full min-w-[420px] text-left text-sm">
          <thead>
            <tr className="text-xs uppercase tracking-wide text-slate-500">
              <th className="pb-2 font-medium">Environment</th>
              <th className="pb-2 font-medium">Project</th>
              <th className="pb-2 font-medium">Revision</th>
              <th className="pb-2 font-medium">Status</th>
              <th className="pb-2" />
            </tr>
          </thead>
          <tbody className="divide-y divide-base-border/70">
            {visible.map((env) => (
              <tr key={env.id} className="text-slate-200">
                <td className="py-3 pr-2">
                  <span className="flex items-center gap-2 font-medium">
                    <span className={`h-2 w-2 shrink-0 rounded-full ${DOT_COLOR[env.name] ?? "bg-accent-blue"}`} />
                    {env.name}
                  </span>
                </td>
                <td className="max-w-32 truncate py-3 pr-2 text-slate-400" title={env.project}>{env.project}</td>
                <td className="py-3 pr-2 font-mono text-xs text-slate-400">{env.version}</td>
                <td className="py-3 pr-2"><StatusBadge status={env.status} /></td>
                <td className="py-3 text-right">
                  {env.url && <a href={env.url} target="_blank" rel="noopener noreferrer" aria-label={`Buka deployment ${env.project} ${env.name}`} className="inline-flex text-accent-blue hover:text-blue-400"><ExternalLink size={15} /></a>}
                </td>
              </tr>
            ))}
            {visible.length === 0 && (
              <tr><td colSpan={5} className="py-7 text-center text-xs text-slate-400">
                {loading ? "Memeriksa deployment Vercel…" : error ? "Tidak ada status terverifikasi." : "Belum ada deployment Vercel untuk aplikasi yang tercatat."}
              </td></tr>
            )}
          </tbody>
        </table>
      </div>
      {!error && updatedAt && <p className="mt-2 text-[11px] text-slate-500">Diperiksa {new Date(updatedAt).toLocaleTimeString("id-ID", { hour: "2-digit", minute: "2-digit", second: "2-digit" })}</p>}
    </div>
  );
}
