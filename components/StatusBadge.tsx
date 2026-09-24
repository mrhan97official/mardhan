const STATUS_STYLES: Record<string, string> = {
  Healthy: "text-emerald-400",
  Online: "text-emerald-400",
  Success: "text-emerald-400",
  Running: "text-purple-400",
  Degraded: "text-amber-400",
  Down: "text-red-400",
  Failed: "text-red-400",
  Pending: "text-slate-400",
};

const DOT_STYLES: Record<string, string> = {
  Healthy: "bg-emerald-400",
  Online: "bg-emerald-400",
  Success: "bg-emerald-400",
  Running: "bg-purple-400 animate-pulse",
  Degraded: "bg-amber-400",
  Down: "bg-red-400",
  Failed: "bg-red-400",
  Pending: "bg-slate-500",
};

export default function StatusBadge({ status }: { status: string }) {
  const text = STATUS_STYLES[status] ?? "text-slate-400";
  const dot = DOT_STYLES[status] ?? "bg-slate-500";
  return (
    <span className={`inline-flex items-center gap-1.5 text-sm font-medium ${text}`}>
      <span className={`h-1.5 w-1.5 rounded-full ${dot}`} />
      {status}
    </span>
  );
}
