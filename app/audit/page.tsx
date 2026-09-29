"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { AlertTriangle, CheckCircle2, ChevronDown, Clipboard, ExternalLink, Loader2, MinusCircle, Play, ShieldAlert, ShieldCheck } from "lucide-react";
import AppShell from "@/components/AppShell";
import Sparkline from "@/components/Sparkline";

type Severity = "high" | "medium" | "low";

interface Finding {
  id: string;
  stage: number;
  category: string;
  severity: Severity;
  title: string;
  location: string;
  detail: string;
  advice: string;
}

interface Report {
  app: string;
  name: string;
  url: string;
  repo: string;
  score: number;
  findings: Finding[];
  passed: string[];
  skipped: string[];
  created_at: string;
  source: string;
  duration_ms: number;
}

interface TargetSummary {
  app: string;
  name: string;
  url: string;
  repo: string;
  self: boolean;
  score: number | null;
  high: number;
  medium: number;
  low: number;
  created_at: string;
  source: string;
  history: number[];
}

const SEVERITY: Record<Severity, { label: string; badge: string; dot: string }> = {
  high: { label: "Tinggi", badge: "border-red-400/40 bg-red-500/10 text-red-400", dot: "bg-red-400" },
  medium: { label: "Sedang", badge: "border-amber-400/40 bg-amber-400/10 text-amber-400", dot: "bg-amber-400" },
  low: { label: "Rendah", badge: "border-accent-blue/40 bg-accent-blue/10 text-accent-blue", dot: "bg-accent-blue" },
};

const STAGE: Record<number, string> = {
  0: "DevControl",
  1: "Tahap 1 · Web & konfigurasi",
  2: "Tahap 2 · Kode & dependensi",
};

function scoreTone(score: number | null) {
  if (score === null) return { text: "text-slate-400", ring: "border-base-border", color: "#64748b", label: "Belum diaudit" };
  if (score >= 85) return { text: "text-emerald-400", ring: "border-emerald-400/60", color: "#34d399", label: "Baik" };
  if (score >= 60) return { text: "text-amber-400", ring: "border-amber-400/60", color: "#fbbf24", label: "Perlu perhatian" };
  return { text: "text-red-400", ring: "border-red-400/60", color: "#f87171", label: "Berisiko" };
}

// D1 stores UTC "YYYY-MM-DD HH:MM:SS"; show it in the viewer's local time.
function when(value: string) {
  if (!value) return "";
  const parsed = new Date(`${value.replace(" ", "T")}Z`);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleString("id-ID", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
}

function host(url: string) {
  try { return new URL(url).host; } catch { return url; }
}

async function api<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, { credentials: "same-origin", cache: "no-store", ...init });
  const body = await response.json().catch(() => null);
  if (!response.ok) throw new Error(body?.error || `HTTP ${response.status}`);
  return body as T;
}

function aiPrompt(report: Report, findings: Finding[]) {
  const lines = [
    `Saya menjalankan audit keamanan pasif untuk aplikasi "${report.name}"${report.repo ? ` (repo ${report.repo})` : ""}, ${report.url}.`,
    `Skor ${report.score}/100. Bantu saya memperbaiki temuan berikut, mulai dari risiko tertinggi. Untuk setiap temuan: jelaskan penyebabnya, beri langkah perbaikan konkret (file/konfigurasi yang diubah beserta contoh kodenya), dan cara memastikan perbaikannya berhasil.`,
    "",
  ];
  findings.forEach((f, index) => {
    lines.push(`${index + 1}. [${SEVERITY[f.severity].label}] ${f.title}`);
    lines.push(`   Lokasi: ${f.location}`);
    lines.push(`   Detail: ${f.detail}`);
    lines.push(`   Saran awal: ${f.advice}`);
  });
  return lines.join("\n");
}

function ScoreBadge({ score, size = "md" }: { score: number | null; size?: "md" | "lg" }) {
  const tone = scoreTone(score);
  const box = size === "lg" ? "h-16 w-16 text-2xl" : "h-11 w-11 text-base";
  return (
    <div className={`flex shrink-0 flex-col items-center justify-center rounded-full border-2 ${tone.ring} ${box}`} title={tone.label}>
      <span className={`font-bold leading-none tabular-nums ${tone.text}`}>{score ?? "–"}</span>
    </div>
  );
}

function FindingCard({ finding, onCopy }: { finding: Finding; onCopy: (text: string) => void }) {
  const tone = SEVERITY[finding.severity];
  return (
    <article className="rounded-xl border border-base-border bg-base-900 p-2">
      <div className="flex flex-wrap items-center gap-1.5">
        <span className={`rounded-full border px-2 py-0.5 text-[11px] font-semibold ${tone.badge}`}>{tone.label}</span>
        <span className="rounded-full bg-base-800 px-2 py-0.5 text-[11px] text-slate-400">{finding.category}</span>
      </div>
      <h4 className="mt-1.5 text-sm font-semibold text-white">{finding.title}</h4>
      <p className="mt-1 break-words font-mono text-[11px] text-slate-400">{finding.location}</p>
      <p className="mt-1.5 text-xs text-slate-300">{finding.detail}</p>
      <p className="mt-1.5 rounded-lg bg-base-800/60 p-2 text-xs text-slate-200"><span className="font-semibold text-emerald-400">Saran: </span>{finding.advice}</p>
      <button type="button" onClick={() => onCopy(`[${tone.label}] ${finding.title}\nLokasi: ${finding.location}\nDetail: ${finding.detail}\nSaran awal: ${finding.advice}\n\nJelaskan penyebabnya dan beri langkah perbaikan konkret beserta contoh kodenya.`)}
        className="mt-1.5 inline-flex items-center gap-1 text-[11px] font-medium text-accent-blue hover:underline">
        <Clipboard size={12} /> Salin untuk AI
      </button>
    </article>
  );
}

export default function AuditPage() {
  const [targets, setTargets] = useState<TargetSummary[] | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [report, setReport] = useState<Report | null>(null);
  const [history, setHistory] = useState<{ score: number; created_at: string }[]>([]);
  const [running, setRunning] = useState<string | null>(null);
  const [runningAll, setRunningAll] = useState(false);
  const [filter, setFilter] = useState<"all" | Severity>("all");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [showPassed, setShowPassed] = useState(false);
  const [showSkipped, setShowSkipped] = useState(false);

  const loadTargets = useCallback(async () => {
    try {
      const data = await api<{ targets: TargetSummary[] }>("/api/app-audit");
      setTargets(data.targets);
      setError("");
      return data.targets;
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Daftar audit tidak dapat dimuat.");
      return null;
    }
  }, []);

  const loadReport = useCallback(async (app: string) => {
    try {
      const data = await api<{ report: Report | null; history: { score: number; created_at: string }[] }>(`/api/app-audit?app=${encodeURIComponent(app)}`);
      setReport(data.report);
      setHistory(data.history ?? []);
    } catch (reason) {
      setReport(null);
      setError(reason instanceof Error ? reason.message : "Hasil audit tidak dapat dimuat.");
    }
  }, []);

  // ?app=<key> (from a project card) opens that app directly.
  useEffect(() => {
    void (async () => {
      const list = await loadTargets();
      const wanted = new URLSearchParams(window.location.search).get("app");
      const first = list?.find((item) => wanted && item.app.toLowerCase() === wanted.toLowerCase()) ?? list?.[0];
      if (first) { setSelected(first.app); void loadReport(first.app); }
    })();
  }, [loadTargets, loadReport]);

  function choose(app: string) {
    setSelected(app);
    setFilter("all");
    setShowPassed(false);
    setShowSkipped(false);
    void loadReport(app);
    try { window.history.replaceState(null, "", `/audit?app=${encodeURIComponent(app)}`); } catch { /* ignore */ }
  }

  async function audit(app: string) {
    setRunning(app);
    setError("");
    setNotice("");
    try {
      const data = await api<{ report: Report; reused?: boolean }>("/api/app-audit", {
        method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ app }),
      });
      if (data.reused) setNotice("Audit baru saja dijalankan; hasil terakhir ditampilkan.");
      await loadTargets();
      if (selected === app || selected === null) { setSelected(app); await loadReport(app); }
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Audit gagal dijalankan.");
    } finally { setRunning(null); }
  }

  async function auditAll() {
    if (!targets) return;
    setRunningAll(true);
    for (const target of targets) {
      // eslint-disable-next-line no-await-in-loop
      await audit(target.app);
    }
    setRunningAll(false);
    if (selected) void loadReport(selected);
  }

  function copy(text: string) {
    void navigator.clipboard?.writeText(text).then(() => setNotice("Disalin. Tempel ke AI untuk langkah perbaikan terperinci.")).catch(() => setError("Clipboard tidak tersedia di perangkat ini."));
  }

  const current = targets?.find((item) => item.app === selected) ?? null;
  const visible = useMemo(() => (report?.findings ?? []).filter((f) => filter === "all" || f.severity === filter), [report, filter]);
  const grouped = useMemo(() => {
    const groups = new Map<number, Finding[]>();
    for (const finding of visible) groups.set(finding.stage, [...(groups.get(finding.stage) ?? []), finding]);
    return [0, 1, 2].filter((stage) => groups.has(stage)).map((stage) => ({ stage, items: groups.get(stage)! }));
  }, [visible]);
  const totals = useMemo(() => {
    const count = { high: 0, medium: 0, low: 0 };
    for (const f of report?.findings ?? []) count[f.severity] += 1;
    return count;
  }, [report]);
  const busy = running !== null || runningAll;

  return (
    <AppShell title="Audit Aplikasi" subtitle="Pemeriksaan keamanan pasif untuk aplikasi yang dikelola DevControl">
      <section className="card flex flex-wrap items-start justify-between gap-2 p-2">
        <div className="flex min-w-0 items-start gap-2">
          <div className="rounded-xl bg-accent-blue/15 p-2 text-accent-blue"><ShieldAlert size={19} /></div>
          <div className="min-w-0">
            <h2 className="font-semibold text-white">Tingkat risiko, lokasi, dan saran perbaikan</h2>
            <p className="mt-0.5 max-w-3xl text-xs text-slate-400">
              Tahap 1 memeriksa web dari luar serta konfigurasi Vercel/GitHub, tahap 2 memeriksa kode dan dependensi, dan setiap aplikasi diaudit ulang otomatis sekali sehari.
              Pemeriksaan bersifat pasif (tanpa brute force atau eksploitasi) dan hanya untuk aplikasi yang terdaftar. Hasilnya tidak menggantikan uji penetrasi: celah logika aplikasi tetap perlu ditinjau manual.
            </p>
          </div>
        </div>
        <button type="button" disabled={busy || !targets?.length} onClick={() => void auditAll()}
          className="inline-flex items-center gap-2 rounded-xl bg-accent-blue px-3 py-2 text-sm font-semibold text-white hover:bg-blue-500 disabled:opacity-50">
          {runningAll ? <Loader2 size={15} className="animate-spin" /> : <Play size={15} />} {runningAll ? "Mengaudit semua…" : "Audit semua"}
        </button>
      </section>

      {error && <p role="alert" className="rounded-xl border border-red-500/30 bg-red-500/10 p-2 text-sm text-red-300">{error}</p>}
      {notice && <p role="status" className="rounded-xl border border-emerald-400/30 bg-emerald-400/10 p-2 text-sm text-emerald-300">{notice}</p>}

      <div className="grid grid-cols-1 gap-2 lg:grid-cols-[minmax(0,20rem)_minmax(0,1fr)]">
        <div className="space-y-2">
          {targets === null && !error && Array.from({ length: 3 }).map((_, i) => <div key={i} className="card h-20 animate-pulse bg-base-800/40" />)}
          {targets?.map((target) => {
            const tone = scoreTone(target.score);
            const active = target.app === selected;
            return (
              <div key={target.app} className={`card p-2 transition-colors ${active ? "border-accent-blue/60" : ""}`}>
                <button type="button" onClick={() => choose(target.app)} className="flex w-full items-center gap-2 text-left">
                  <ScoreBadge score={target.score} />
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-semibold text-white">{target.name}{target.self && <span className="ml-1.5 rounded-full bg-base-800 px-1.5 py-0.5 text-[10px] font-medium text-slate-400">ini</span>}</p>
                    <p className="truncate text-[11px] text-slate-500">{host(target.url)}</p>
                    <div className="mt-1 flex flex-wrap items-center gap-1.5 text-[10px]">
                      {target.score === null ? <span className="text-slate-500">Belum diaudit</span> : <>
                        <span className="font-semibold text-red-400">{target.high} T</span>
                        <span className="font-semibold text-amber-400">{target.medium} S</span>
                        <span className="font-semibold text-accent-blue">{target.low} R</span>
                        <span className="text-slate-500">· {when(target.created_at)}{target.source === "schedule" ? " (terjadwal)" : ""}</span>
                      </>}
                    </div>
                  </div>
                  {target.history.length > 1 && <div className="h-7 w-14 shrink-0"><Sparkline values={target.history} color={tone.color} height={28} width={56} fill={false} /></div>}
                </button>
                <button type="button" disabled={busy} onClick={() => void audit(target.app)}
                  className="mt-1.5 inline-flex w-full items-center justify-center gap-1.5 rounded-lg border border-base-border px-2 py-1.5 text-xs font-medium text-slate-200 hover:bg-base-800 disabled:opacity-50">
                  {running === target.app ? <><Loader2 size={13} className="animate-spin" /> Mengaudit…</> : <><Play size={13} /> Audit sekarang</>}
                </button>
              </div>
            );
          })}
        </div>

        <section className="card min-w-0 p-2" aria-live="polite">
          {!current ? <p className="p-2 text-sm text-slate-400">Pilih aplikasi di kiri.</p> : !report ? (
            <div className="flex flex-col items-center gap-2 p-6 text-center">
              <ShieldCheck size={28} className="text-slate-500" />
              <p className="text-sm text-slate-300">{current.name} belum pernah diaudit.</p>
              <button type="button" disabled={busy} onClick={() => void audit(current.app)} className="inline-flex items-center gap-2 rounded-xl bg-accent-blue px-3 py-2 text-sm font-semibold text-white hover:bg-blue-500 disabled:opacity-50">
                {running === current.app ? <Loader2 size={15} className="animate-spin" /> : <Play size={15} />} Audit sekarang
              </button>
            </div>
          ) : (
            <div className="space-y-2">
              <div className="flex flex-wrap items-center gap-2">
                <ScoreBadge score={report.score} size="lg" />
                <div className="min-w-0 flex-1">
                  <h3 className="truncate text-lg font-bold text-white">{report.name}</h3>
                  <p className={`text-xs font-semibold ${scoreTone(report.score).text}`}>{scoreTone(report.score).label}</p>
                  <p className="text-[11px] text-slate-500">{when(report.created_at)} · {report.source === "schedule" ? "terjadwal" : "manual"} · {(report.duration_ms / 1000).toFixed(1)} detik</p>
                </div>
                <a href={report.url} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 text-xs text-accent-blue hover:underline">{host(report.url)} <ExternalLink size={12} /></a>
              </div>

              {history.length > 1 && (
                <div className="rounded-xl bg-base-900 p-2">
                  <p className="text-[11px] text-slate-500">Riwayat skor ({history.length} audit terakhir)</p>
                  <div className="h-10"><Sparkline values={history.map((point) => point.score)} color={scoreTone(report.score).color} height={40} width={400} /></div>
                </div>
              )}

              <div className="flex flex-wrap items-center gap-1.5">
                {([["all", `Semua (${report.findings.length})`], ["high", `Tinggi (${totals.high})`], ["medium", `Sedang (${totals.medium})`], ["low", `Rendah (${totals.low})`]] as const).map(([key, label]) => (
                  <button key={key} type="button" onClick={() => setFilter(key)} aria-pressed={filter === key}
                    className={`rounded-lg px-2.5 py-1 text-xs font-medium ${filter === key ? "bg-accent-blue text-white" : "border border-base-border text-slate-300 hover:bg-base-800"}`}>{label}</button>
                ))}
                {report.findings.length > 0 && (
                  <button type="button" onClick={() => copy(aiPrompt(report, report.findings))} className="ml-auto inline-flex items-center gap-1.5 rounded-lg border border-base-border px-2.5 py-1 text-xs font-medium text-slate-200 hover:bg-base-800">
                    <Clipboard size={13} /> Salin prompt perbaikan untuk AI
                  </button>
                )}
              </div>

              {report.findings.length === 0 && (
                <p className="flex items-center gap-2 rounded-xl bg-emerald-400/10 p-2 text-sm text-emerald-300"><CheckCircle2 size={16} /> Tidak ada temuan. Semua pemeriksaan yang bisa dijalankan lolos.</p>
              )}
              {grouped.map(({ stage, items }) => (
                <div key={stage} className="space-y-1.5">
                  <p className="text-[11px] font-semibold uppercase tracking-wide text-slate-500">{STAGE[stage] ?? `Tahap ${stage}`}</p>
                  {items.map((finding) => <FindingCard key={finding.id} finding={finding} onCopy={copy} />)}
                </div>
              ))}

              {report.passed.length > 0 && (
                <div className="rounded-xl border border-base-border">
                  <button type="button" onClick={() => setShowPassed((value) => !value)} aria-expanded={showPassed} className="flex w-full items-center gap-2 p-2 text-left text-xs font-medium text-slate-300">
                    <CheckCircle2 size={14} className="text-emerald-400" /> Lolos ({report.passed.length}) <ChevronDown size={14} className={`ml-auto transition-transform ${showPassed ? "rotate-180" : ""}`} />
                  </button>
                  {showPassed && <ul className="space-y-1 px-2 pb-2 text-xs text-slate-400">{report.passed.map((item) => <li key={item} className="flex gap-1.5"><CheckCircle2 size={12} className="mt-0.5 shrink-0 text-emerald-400" />{item}</li>)}</ul>}
                </div>
              )}
              {report.skipped.length > 0 && (
                <div className="rounded-xl border border-base-border">
                  <button type="button" onClick={() => setShowSkipped((value) => !value)} aria-expanded={showSkipped} className="flex w-full items-center gap-2 p-2 text-left text-xs font-medium text-slate-300">
                    <MinusCircle size={14} className="text-slate-500" /> Tidak dapat diperiksa ({report.skipped.length}) <ChevronDown size={14} className={`ml-auto transition-transform ${showSkipped ? "rotate-180" : ""}`} />
                  </button>
                  {showSkipped && <ul className="space-y-1 px-2 pb-2 text-xs text-slate-400">{report.skipped.map((item) => <li key={item} className="flex gap-1.5"><AlertTriangle size={12} className="mt-0.5 shrink-0 text-amber-400" />{item}</li>)}</ul>}
                </div>
              )}
            </div>
          )}
        </section>
      </div>
    </AppShell>
  );
}
