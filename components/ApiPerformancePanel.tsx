"use client";

import { AlertTriangle, BarChart3, Clock, TrendingDown, TrendingUp } from "lucide-react";
import Sparkline from "./Sparkline";
import type { ApiPerformance } from "@/lib/types";

function Change({ value, invert = false, unit = "%" }: { value: number; invert?: boolean; unit?: string }) {
  const positive = invert ? value < 0 : value >= 0;
  const Icon = value >= 0 ? TrendingUp : TrendingDown;
  return (
    <span className={`inline-flex items-center gap-1 text-xs font-semibold ${value === 0 ? "text-slate-400" : positive ? "text-emerald-400" : "text-red-400"}`}>
      <Icon size={12} />
      {value >= 0 ? "+" : ""}
      {value}{unit}
    </span>
  );
}

export default function ApiPerformancePanel({ perf, onRangeChange, range }: { perf: ApiPerformance; range?: "24h" | "7d" | "30d"; onRangeChange?: (range: "24h" | "7d" | "30d") => void }) {
  const rows = [
    {
      label: "Rata-rata waktu uji",
      value: `${perf.response_time_ms} ms`,
      change: perf.response_time_change,
      invert: true,
      icon: Clock,
      unit: "%",
      spark: perf.series_latency || [],
      color: "#3B82F6",
    },
    {
      label: "Jumlah pemeriksaan",
      value: `${perf.request_volume}`,
      change: perf.request_volume_change,
      icon: BarChart3,
      unit: "%",
      spark: perf.series_volume || [],
      color: "#A855F7",
    },
    {
      label: "Pemeriksaan gagal",
      value: `${perf.error_rate}%`,
      change: perf.error_rate_change,
      invert: true,
      icon: AlertTriangle,
      unit: " pp",
      spark: perf.series_errors || [],
      color: "#EF4444",
    },
  ];

  return (
    <div className="card min-w-0 p-2">
      <div className="flex items-center justify-between">
        <h2 className="text-base font-bold text-white sm:text-lg">Hasil Pemeriksaan API</h2>
        {onRangeChange ? <select aria-label="Rentang pemeriksaan" value={range || perf.range || "24h"} onChange={(event) => onRangeChange(event.target.value as "24h" | "7d" | "30d")}
          className="rounded-lg border border-base-border bg-base-850 px-2.5 py-1.5 text-xs font-medium text-slate-300">
          <option value="24h">24 jam</option><option value="7d">7 hari</option><option value="30d">30 hari</option>
        </select> : <span className="text-xs text-slate-400">24 jam</span>}
      </div>
      {perf.request_volume === 0 && <p className="mt-2 text-sm text-slate-400">Belum ada uji endpoint dalam rentang ini. Grafik tidak memakai data contoh.</p>}
      {perf.request_volume > 0 && <div className="mt-2 divide-y divide-base-border/70">
        {rows.map((r) => (
          <div key={r.label} className="flex items-center gap-2 py-2 first:pt-0 last:pb-0">
            <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-base-800 text-slate-300">
              <r.icon size={16} />
            </div>
            <div className="min-w-0 flex-1">
              <p className="text-sm text-slate-400">{r.label}</p>
              <div className="flex items-center gap-2">
                <span className="text-lg font-bold text-white">{r.value}</span>
                {perf.has_comparison && <Change value={r.change} invert={r.invert} unit={r.unit} />}
              </div>
            </div>
            <div className="hidden h-10 w-24 shrink-0 sm:block">
              <Sparkline values={r.spark} color={r.color} height={40} width={96} fill={false} />
            </div>
          </div>
        ))}
      </div>}
      <p className="mt-2 text-xs text-slate-500">Metrik ini berasal dari tombol Uji API yang dijalankan DevControl; bukan total trafik aplikasi lain.</p>
    </div>
  );
}
