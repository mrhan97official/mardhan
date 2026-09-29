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

// One clear answer to "apakah ini dari DevControl atau dari ZIP saya?",
// shown before anything else in the diagnosis. Wording adapts to whether
// this run was deploying the person's own app or updating DevControl itself.
function sourceBanner(source: Diagnosis["source"] | undefined, kind?: string, logMissing?: boolean) {
  const selfUpdate = kind === "self_update";
  if (logMissing) {
    return {
      Icon: Loader2,
      tone: "border-slate-500/30 bg-slate-500/[0.06] text-slate-300",
      title: "Sumber sedang dipastikan: log build dibaca ulang dari Vercel",
      detail: "Build berhenti dengan ERROR, tetapi Vercel belum menyimpan log-nya saat itu. DevControl membacanya ulang otomatis; sumber dan letak error tampil di sini begitu log tersedia.",
    };
  }
  switch (source) {
    case "code":
      return {
        Icon: FileCode2,
        tone: "border-amber-400/30 bg-amber-500/[0.06] text-amber-200",
        title: selfUpdate ? "Sumber: kode DevControl (patch yang baru diterapkan)" : "Sumber: kode di ZIP yang Anda unggah",
        detail: selfUpdate
          ? "Bukan masalah token, izin, atau koneksi — kode DevControl sendiri yang perlu diperbaiki sebelum di-deploy ulang."
          : "Bukan masalah DevControl — perbaiki di project aplikasi ini (kode atau package.json/go.mod), lalu unggah ulang ZIP-nya.",
      };
    case "config":
      return {
        Icon: Settings,
        tone: "border-sky-400/30 bg-sky-500/[0.06] text-sky-200",
        title: "Sumber: pengaturan project Vercel aplikasi ini",
        detail: "Bukan token atau kredensial DevControl — periksa Framework/Root Directory/Output Directory atau Environment Variables project ini di dashboard Vercel, bukan kode di ZIP.",
      };
    case "platform":
      return {
        Icon: Wrench,
        tone: "border-violet-400/30 bg-violet-500/[0.06] text-violet-200",
        title: "Sumber: DevControl sendiri (token, izin, atau koneksi layanan)",
        detail: "Bukan dari kode di ZIP — periksa GITHUB_TOKEN/VERCEL_TOKEN/CF_API_TOKEN di Environment Variables Vercel milik DevControl, atau ini gangguan sementara di GitHub/Vercel/Cloudflare.",
      };
    default:
      return {
        Icon: Stethoscope,
        tone: "border-slate-500/30 bg-slate-500/[0.06] text-slate-300",
        title: "Sumber belum bisa dipastikan otomatis",
        detail: "Pola error ini belum dikenali DevControl. Lihat log dan potongan kode di bawah, atau salin prompt untuk AI.",
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

  const banner = sourceBanner(diagnosis.source, kind, diagnosis.log_missing);
  const where = diagnosis.location
    ? `${diagnosis.location.file}${diagnosis.location.line ? ` baris ${diagnosis.location.line}` : ""}${diagnosis.location.column ? `, kolom ${diagnosis.location.column}` : ""}`
    : "";

  async function copyPrompt() {
    const ok = await copyText(diagnosis?.prompt ?? "");
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

      <div className={`flex items-start gap-2 rounded-lg border px-2.5 py-2 ${banner.tone}`}>
        <banner.Icon size={16} className={`mt-0.5 shrink-0 ${diagnosis.log_missing ? "animate-spin" : ""}`} />
        <div className="text-xs">
          <p className="font-semibold">{banner.title}</p>
          <p className="mt-0.5 opacity-90">{banner.detail}</p>
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
          {copied === "fail" ? "Gagal menyalin otomatis; blok teks prompt di bawah lalu salin manual." : "Tempel ke AI, terapkan perbaikannya, ZIP ulang, lalu deploy lagi."}
        </span>
      </div>
      {copied === "fail" && (
        <textarea readOnly value={diagnosis.prompt} className="h-40 w-full rounded-lg border border-base-border bg-base-950 p-2 font-mono text-[11px] text-slate-300" />
      )}
      <p className="text-[11px] text-slate-500">ZIP yang gagal tidak disimpan. ZIP terakhir yang berhasil (jika ada) tetap aktif.</p>
    </section>
  );
}
