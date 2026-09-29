"use client";

import { useEffect, useState } from "react";
import { Check, ChevronDown, Copy, FileCode2, Loader2, RotateCcw, Settings, Stethoscope, Wrench } from "lucide-react";
import type { Diagnosis } from "@/lib/types";

async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // Older mobile browsers: fall back to a temporary selection.
    try {
      const area = document.createElement("textarea");
      area.value = text;
      area.setAttribute("readonly", "");
      area.style.position = "fixed";
      area.style.opacity = "0";
      document.body.appendChild(area);
      area.select();
      const ok = document.execCommand("copy");
      document.body.removeChild(area);
      return ok;
    } catch {
      return false;
    }
  }
}

// One clear command, shown first: fix the ZIP, fix the app's settings, or
// fix DevControl. The same sentence opens the AI prompt, so a copied prompt
// never asks for a ZIP patch when DevControl is at fault.
export const FIX_DIRECTIVE_MARKER = "PENTING — YANG HARUS DIPERBAIKI:";

type Directive = {
  Icon: typeof FileCode2;
  tone: string;
  chipTone: string;
  headline: string;
  detail: string;
  chip: string;
  afterCopy: string;
  promptLine: string;
};

export function fixDirective(source: Diagnosis["source"] | undefined, kind?: string, logMissing?: boolean): Directive {
  if (logMissing) {
    return {
      Icon: Loader2,
      tone: "border-slate-500/40 bg-slate-500/[0.08] text-slate-200",
      chipTone: "border-slate-500/40 text-slate-300",
      headline: "Belum pasti — tunggu log build",
      detail: "Build berhenti dengan ERROR, tetapi Vercel belum menyimpan log-nya. DevControl membacanya ulang otomatis; perintah perbaikan muncul di sini begitu log tersedia. Jangan mengubah ZIP atau DevControl dulu.",
      chip: "Menunggu log",
      afterCopy: "Tunggu log terbaca dulu sebelum mengubah apa pun.",
      promptLine: "BELUM PASTI. Log build belum terbaca, jadi belum diketahui apakah ZIP atau DevControl yang harus diperbaiki; jangan mengubah apa pun sebelum log tersedia.",
    };
  }
  switch (source) {
    case "code":
      return kind === "self_update" ? {
        Icon: FileCode2,
        tone: "border-amber-400/50 bg-amber-500/[0.1] text-amber-100",
        chipTone: "border-amber-400/50 text-amber-200",
        headline: "Perbaiki: ZIP Update Diri (kode DevControl)",
        detail: "Kode DevControl di ZIP Update Diri ini yang salah. Perbaiki ZIP-nya, lalu jalankan Update Diri lagi. DevControl yang sedang berjalan tidak berubah.",
        chip: "Perbaiki ZIP Update Diri",
        afterCopy: "Tempel ke AI, perbaiki ZIP Update Diri, lalu jalankan Update Diri lagi.",
        promptLine: "KODE DEVCONTROL di ZIP Update Diri ini. Perbaiki ZIP Update Diri tersebut, lalu jalankan Update Diri lagi.",
      } : {
        Icon: FileCode2,
        tone: "border-amber-400/50 bg-amber-500/[0.1] text-amber-100",
        chipTone: "border-amber-400/50 text-amber-200",
        headline: "Perbaiki: ZIP aplikasi",
        detail: "Kesalahan ada di kode/berkas proyek di dalam ZIP. DevControl tidak perlu diubah — perbaiki ZIP-nya, lalu deploy ulang.",
        chip: "Perbaiki ZIP",
        afterCopy: "Tempel ke AI, perbaiki ZIP, lalu deploy lagi.",
        promptLine: "ZIP APLIKASI. Kesalahan ada di kode/berkas proyek di dalam ZIP; DevControl tidak perlu diubah. Perbaiki ZIP-nya, lalu deploy ulang.",
      };
    case "config":
      return {
        Icon: Settings,
        tone: "border-sky-400/50 bg-sky-500/[0.1] text-sky-100",
        chipTone: "border-sky-400/50 text-sky-200",
        headline: "Perbaiki: pengaturan aplikasi (bukan DevControl)",
        detail: "Perbaiki lewat berkas konfigurasi di ZIP (vercel.json, package.json, next.config) atau pengaturan project Vercel aplikasi ini: Framework, Root/Output Directory, Environment Variables. DevControl tidak perlu diubah.",
        chip: "Perbaiki pengaturan aplikasi",
        afterCopy: "Tempel ke AI, terapkan perbaikan pengaturannya, lalu deploy lagi.",
        promptLine: "PENGATURAN APLIKASI, bukan DevControl. Perbaiki lewat berkas konfigurasi di ZIP (mis. vercel.json, package.json, next.config) atau pengaturan project Vercel aplikasi ini (Framework, Root/Output Directory, Environment Variables).",
      };
    case "platform":
      return {
        Icon: Wrench,
        tone: "border-violet-400/50 bg-violet-500/[0.1] text-violet-100",
        chipTone: "border-violet-400/50 text-violet-200",
        headline: "Perbaiki: DevControl — ZIP tidak perlu diubah",
        detail: "Kode di ZIP tidak salah. Periksa GITHUB_TOKEN, VERCEL_TOKEN, CF_API_TOKEN, dan VERCEL_TEAM_ID di Environment Variables Vercel milik DevControl, tunggu bila GitHub/Vercel/Cloudflare sedang gangguan, atau perbarui DevControl lewat Update Diri. Setelah beres, deploy ulang dengan ZIP yang sama.",
        chip: "Perbaiki DevControl",
        afterCopy: "Tempel ke AI untuk memperbaiki DevControl; deploy ulang dengan ZIP yang sama setelah beres.",
        promptLine: "DEVCONTROL, BUKAN ZIP. Kode di ZIP aplikasi tidak salah dan tidak perlu diubah. Yang harus diperbaiki adalah DevControl: token/izin (GITHUB_TOKEN, VERCEL_TOKEN, CF_API_TOKEN, VERCEL_TEAM_ID), gangguan layanan, atau kode DevControl sendiri.",
      };
    default:
      return {
        Icon: Stethoscope,
        tone: "border-slate-500/40 bg-slate-500/[0.08] text-slate-200",
        chipTone: "border-slate-500/40 text-slate-300",
        headline: "Belum pasti: ZIP atau DevControl",
        detail: "Tidak ada bukti yang cukup untuk memastikan tempat perbaikannya. Lihat log dan potongan kode di bawah, atau salin prompt untuk AI agar ditentukan dari log.",
        chip: "Sumber belum pasti",
        afterCopy: "Tempel ke AI untuk menentukan apakah ZIP atau DevControl yang diperbaiki.",
        promptLine: "BELUM PASTI. Tentukan dulu dari log apakah yang harus diperbaiki kode di ZIP, pengaturan Vercel aplikasi, atau DevControl.",
      };
  }
}

export default function ErrorDiagnosis({
  jobId,
  diagnosis: provided,
  message,
  kind,
  target,
  stage,
}: {
  jobId?: string;
  /** "self_update" changes what a "code" source means below: DevControl's
   * own source, not a deployed application's. */
  diagnosis?: Diagnosis | null;
  message?: string;
  kind?: string;
  target?: string;
  stage?: string;
}) {
  const [diagnosis, setDiagnosis] = useState<Diagnosis | null>(provided ?? null);
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState("");
  const [copied, setCopied] = useState<"" | "ok" | "fail">("");
  const [showLog, setShowLog] = useState(false);

  useEffect(() => { if (provided) setDiagnosis(provided); }, [provided]);

  // When the build log was not available yet, ask the server to read it
  // again every 10 seconds for about two minutes; it rebuilds the diagnosis
  // (real error, source, location) as soon as Vercel returns the log.
  const waitingForLog = !!jobId && !!diagnosis?.log_missing;
  useEffect(() => {
    if (!waitingForLog || !jobId) return;
    let cancelled = false;
    let attempts = 0;
    const timer = setInterval(async () => {
      attempts += 1;
      if (attempts > 12) { clearInterval(timer); return; }
      try {
        const response = await fetch("/api/diagnose", {
          method: "POST", headers: { "Content-Type": "application/json" }, credentials: "same-origin", cache: "no-store",
          body: JSON.stringify({ job_id: jobId }),
        });
        if (!response.ok) return;
        const body = (await response.json()) as Diagnosis;
        if (!cancelled && body && !body.log_missing) { setDiagnosis(body); clearInterval(timer); }
      } catch { /* try again on the next tick */ }
    }, 10000);
    return () => { cancelled = true; clearInterval(timer); };
  }, [waitingForLog, jobId]);

  useEffect(() => {
    if (provided || (!jobId && !message)) return;
    let cancelled = false;
    const load = async () => {
      setLoading(true);
      try {
        const response = await fetch("/api/diagnose", {
          method: "POST", headers: { "Content-Type": "application/json" }, credentials: "same-origin", cache: "no-store",
          body: JSON.stringify(jobId ? { job_id: jobId, message } : { message, kind, target, stage }),
        });
        const body = await response.json().catch(() => ({}));
        if (!response.ok) throw new Error(body.error || `HTTP ${response.status}`);
        if (!cancelled) { setDiagnosis(body as Diagnosis); setFailed(""); }
      } catch (reason) {
        if (!cancelled) setFailed(reason instanceof Error ? reason.message : "Diagnosis tidak tersedia.");
      } finally {
        if (!cancelled) setLoading(false);
      }
    };
    void load();
    // The runner stores the full diagnosis (with code snippet) a moment after
    // the pipeline turns red; fetch once more to pick it up.
    const retry = jobId ? setTimeout(() => { void load(); }, 4000) : undefined;
    return () => { cancelled = true; if (retry) clearTimeout(retry); };
  }, [jobId, message, kind, target, stage, provided]);

  if (loading && !diagnosis) {
    return <p className="flex items-center gap-2 text-xs text-slate-400"><Loader2 size={14} className="animate-spin" /> Menganalisis error…</p>;
  }
  if (!diagnosis) {
    return failed ? <p className="text-xs text-slate-500">Diagnosis otomatis belum tersedia: {failed}</p> : null;
  }

  const directive = fixDirective(diagnosis.source, kind, diagnosis.log_missing);
  // Prompts stored before the directive existed get the same first line.
  const prompt = diagnosis.prompt.startsWith(FIX_DIRECTIVE_MARKER)
    ? diagnosis.prompt
    : `${FIX_DIRECTIVE_MARKER} ${directive.promptLine}\n\n${diagnosis.prompt}`;
  const where = diagnosis.location
    ? `${diagnosis.location.file}${diagnosis.location.line ? ` baris ${diagnosis.location.line}` : ""}${diagnosis.location.column ? `, kolom ${diagnosis.location.column}` : ""}`
    : "";

  async function copyPrompt() {
    const ok = await copyText(prompt);
    setCopied(ok ? "ok" : "fail");
    setTimeout(() => setCopied(""), 2500);
  }

  return (
    <section className="space-y-2 rounded-xl border border-red-500/30 bg-red-500/[0.04] p-2" aria-label="Diagnosis error">
      <div className="flex flex-wrap items-center gap-2">
        <Stethoscope size={16} className="text-red-300" />
        <h3 className="text-sm font-bold text-white">Diagnosis error</h3>
        <span className="rounded-full border border-red-400/40 px-2 py-0.5 text-[11px] font-medium text-red-300">{diagnosis.category}</span>
      </div>

      <div role="note" aria-label="Yang harus diperbaiki" className={`flex items-start gap-2.5 rounded-lg border-2 px-3 py-2.5 ${directive.tone}`}>
        <directive.Icon size={20} className={`mt-0.5 shrink-0 ${diagnosis.log_missing ? "animate-spin" : ""}`} />
        <div className="min-w-0">
          <p className="text-[10px] font-semibold uppercase tracking-wider opacity-80">Yang harus diperbaiki</p>
          <p className="text-sm font-bold">{directive.headline}</p>
          <p className="mt-1 text-xs opacity-90">{directive.detail}</p>
        </div>
      </div>

      <dl className="space-y-1.5 text-xs">
        <div className="flex gap-2"><dt className="w-16 shrink-0 text-slate-500">Tahap</dt><dd className="text-slate-200">{diagnosis.stage}</dd></div>
        {where && (
          <div className="flex gap-2"><dt className="w-16 shrink-0 text-slate-500">Lokasi</dt>
            <dd className="flex min-w-0 items-center gap-1 font-mono text-amber-200"><FileCode2 size={13} className="shrink-0" /><span className="break-all">{where}</span></dd></div>
        )}
        <div className="flex gap-2"><dt className="w-16 shrink-0 text-slate-500">Error</dt><dd className="min-w-0 break-words font-mono text-red-300">{diagnosis.summary}</dd></div>
      </dl>

      {diagnosis.snippet && (
        <pre className="max-h-64 overflow-auto rounded-lg border border-base-border bg-base-950 p-2 font-mono text-[11px] leading-relaxed text-slate-300">{diagnosis.snippet}</pre>
      )}

      <div className="space-y-1.5">
        <p className="text-xs text-slate-300"><span className="font-semibold text-slate-100">Penyebab: </span>{diagnosis.cause}</p>
        <p className="text-xs font-semibold text-slate-100">Saran perbaikan</p>
        <ol className="list-decimal space-y-1 pl-2 text-xs text-slate-300">
          {diagnosis.fixes.map((fix, index) => <li key={index}>{fix}</li>)}
        </ol>
        {diagnosis.retryable && (
          <p className="flex items-center gap-1.5 text-xs text-amber-300"><RotateCcw size={13} /> Kemungkinan gangguan sementara — coba jalankan ulang dengan ZIP yang sama.</p>
        )}
      </div>

      {diagnosis.log_excerpt && (
        <div>
          <button type="button" onClick={() => setShowLog((value) => !value)} className="inline-flex items-center gap-1 text-xs font-medium text-slate-400 hover:text-white">
            <ChevronDown size={14} className={showLog ? "rotate-180 transition-transform" : "transition-transform"} /> {showLog ? "Sembunyikan log" : "Lihat log build"}
          </button>
          {showLog && <pre className="mt-2 max-h-56 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-base-950 p-2 text-[11px] text-slate-300">{diagnosis.log_excerpt}</pre>}
        </div>
      )}

      <div className="flex flex-wrap items-center gap-2 border-t border-base-border pt-2">
        <button type="button" onClick={() => void copyPrompt()}
          className="inline-flex items-center gap-1.5 rounded-lg bg-accent-blue px-3 py-2 text-xs font-semibold text-white">
          {copied === "ok" ? <Check size={14} /> : <Copy size={14} />}
          {copied === "ok" ? "Prompt tersalin" : "Salin prompt untuk AI"}
        </button>
        <span className="text-[11px] text-slate-500">
          {copied === "fail" ? "Gagal menyalin otomatis; blok teks prompt di bawah lalu salin manual." : directive.afterCopy}
        </span>
      </div>
      {copied === "fail" && (
        <textarea readOnly value={prompt} className="h-40 w-full rounded-lg border border-base-border bg-base-950 p-2 font-mono text-[11px] text-slate-300" />
      )}
      <p className="text-[11px] text-slate-500">ZIP yang gagal tidak disimpan. ZIP terakhir yang berhasil (jika ada) tetap aktif.</p>
    </section>
  );
}
