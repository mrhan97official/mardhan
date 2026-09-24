"use client";

import { useEffect, useState } from "react";
import { Circle, CheckCircle2, Loader2, RefreshCw, UploadCloud, XCircle } from "lucide-react";
import Modal from "./Modal";
import { useOfflineData } from "@/lib/useOfflineData";
import { fallbackGithubRepos } from "@/lib/fallbackData";
import type { GithubRepo } from "@/lib/types";
import { pollVercelBuild } from "@/lib/pollVercelBuild";
import Link from "next/link";

interface SelfUpdateResponse {
  step: "extract" | "vercel-test" | "github-push" | "vercel-production" | "done";
  ok: boolean;
  message: string;
  build_log?: string;
  preview_url?: string;
  status?: "pending" | "ready";
  ticket?: string;
  archive_id?: string;
}

const STEP_ORDER = ["extract", "vercel-test", "github-push", "vercel-production"] as const;
type StepKey = (typeof STEP_ORDER)[number];
type StepState = "pending" | "active" | "done" | "failed";

const STEPS: { key: StepKey; label: string }[] = [
  { key: "extract", label: "Ekstrak file zip" },
  { key: "vercel-test", label: "Uji coba build di Vercel" },
  { key: "github-push", label: "Perbarui repo GitHub" },
  { key: "vercel-production", label: "Verifikasi production Vercel" },
];

export default function SelfUpdateModal({
  hidden,
  onHide,
  onClose,
  onSuccess,
  onProgress,
}: {
  hidden: boolean;
  onHide: () => void;
  onClose: () => void;
  onSuccess: () => void;
  onProgress: (progress: { running: boolean; failed: boolean; message: string }) => void;
}) {
  const repos = useOfflineData<GithubRepo[]>("github-repos", "/api/github-repos", fallbackGithubRepos);
  const [repo, setRepo] = useState("");
  const [branch, setBranch] = useState("main");
  const [zipFile, setZipFile] = useState<File | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [result, setResult] = useState<SelfUpdateResponse | null>(null);
  const [archiveSaved, setArchiveSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [activeStep, setActiveStep] = useState<StepKey | null>(null);
  const [elapsedSeconds, setElapsedSeconds] = useState(0);

  useEffect(() => {
    const stage = STEPS.find((step) => step.key === activeStep)?.label;
    onProgress({
      running: submitting,
      failed: Boolean(error || (result && !result.ok)),
      message: error ?? (submitting && activeStep && result?.step !== activeStep ? `${stage}...` : result?.message) ?? (stage ? `${stage}...` : "Menunggu proses."),
    });
  }, [submitting, activeStep, result, error, onProgress]);

  // Branches for whichever repo is currently selected, read straight from
  // GitHub — so this field offers the branches that actually exist in that
  // repo instead of a free-text guess. Falls back to manual entry below if
  // the lookup fails (e.g. GITHUB_TOKEN missing, repo not yet picked).
  const [branches, setBranches] = useState<string[]>([]);
  const [branchesLoading, setBranchesLoading] = useState(false);
  const [branchesError, setBranchesError] = useState<string | null>(null);
  const [branchesReloadKey, setBranchesReloadKey] = useState(0);

  useEffect(() => {
    if (!repo.includes("/")) {
      setBranches([]);
      setBranchesError(null);
      setBranchesLoading(false);
      return;
    }
    let cancelled = false;
    setBranchesLoading(true);
    setBranchesError(null);
    fetch(`/api/github-branches?repo=${encodeURIComponent(repo)}`, { cache: "no-store" })
      .then(async (res) => {
        const body = await res.json().catch(() => null);
        if (!res.ok) throw new Error((body && body.error) || `HTTP ${res.status}`);
        return (body ?? []) as string[];
      })
      .then((list) => {
        if (cancelled) return;
        setBranches(Array.isArray(list) ? list : []);
      })
      .catch((err) => {
        if (cancelled) return;
        setBranches([]);
        setBranchesError(err instanceof Error ? err.message : "Gagal memuat daftar branch.");
      })
      .finally(() => {
        if (!cancelled) setBranchesLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [repo, branchesReloadKey]);

  function handleRepoSelect(fullName: string) {
    setRepo(fullName);
    const found = repos.data.find((r) => r.full_name === fullName);
    if (found?.default_branch) setBranch(found.default_branch);
  }

  function stepState(step: StepKey): StepState {
    if (submitting && step === activeStep) return "active";
    if (!result) return "pending";
    if (result.step === "done") return "done";
    const currentIndex = STEP_ORDER.indexOf(result.step);
    const thisIndex = STEP_ORDER.indexOf(step);
    if (thisIndex < currentIndex) return "done";
    if (thisIndex === currentIndex) return result.ok ? result.status === "pending" ? "active" : "done" : "failed";
    return "pending";
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);

    if (!repo.trim().includes("/")) {
      setError('Format repo harus "owner/repo".');
      return;
    }
    if (!zipFile) {
      setError("Unggah file .zip terlebih dahulu.");
      return;
    }
    if (!zipFile.name.toLowerCase().endsWith(".zip")) {
      setError("File harus berformat .zip.");
      return;
    }
    if (zipFile.size > 4 * 1024 * 1024) {
      setError("File ZIP maksimal 4 MiB agar bisa disimpan dan diunduh kembali melalui Vercel.");
      return;
    }

    setSubmitting(true);
    setResult(null);
    setArchiveSaved(false);
    setActiveStep("extract");
    setElapsedSeconds(0);
    try {
      const form = new FormData();
      form.append("repo", repo.trim());
      form.append("branch", branch.trim() || "main");
      form.append("zip", zipFile);

      const res = await fetch("/api/self-update", { method: "POST", body: form, cache: "no-store" });
      const body = await res.json().catch(() => null);

      if (!res.ok) {
        throw new Error((body && body.error) || `Gagal (HTTP ${res.status})`);
      }
      let parsed = body as SelfUpdateResponse;
      setResult(parsed);
      if (parsed.archive_id) setArchiveSaved(true);
      if (!parsed.ok) return;
      setActiveStep("vercel-test");
      if (parsed.status !== "pending") throw new Error("Sesi build Vercel tidak tersedia.");
      parsed = await pollVercelBuild<SelfUpdateResponse>("/api/self-update", {
        phase: "status", repo: repo.trim(), branch: branch.trim() || "main",
      }, parsed, (progress, elapsed) => {
        setResult(progress);
        setElapsedSeconds(elapsed);
      });
      if (!parsed.ok) return;
      if (!parsed.ticket || parsed.status !== "ready") throw new Error("Hasil uji build Vercel tidak valid.");
      setActiveStep("github-push");
      const commit = new FormData();
      commit.append("phase", "commit");
      commit.append("repo", repo.trim());
      commit.append("branch", branch.trim() || "main");
      commit.append("zip", zipFile);
      commit.append("ticket", parsed.ticket);
      if (parsed.archive_id) commit.append("archive_id", parsed.archive_id);
      const commitRes = await fetch("/api/self-update", { method: "POST", body: commit, cache: "no-store" });
      const commitBody = await commitRes.json().catch(() => null);
      if (!commitRes.ok) throw new Error((commitBody && commitBody.error) || `Gagal memperbarui GitHub (HTTP ${commitRes.status})`);
      parsed = commitBody as SelfUpdateResponse;
      setResult(parsed);
      if (!parsed.ok) return;
      if (parsed.step !== "vercel-production" || parsed.status !== "pending" || !parsed.ticket) {
        throw new Error("Sesi verifikasi deployment production Vercel tidak tersedia.");
      }
      setActiveStep("vercel-production");
      parsed = await pollVercelBuild<SelfUpdateResponse>("/api/self-update", {
        phase: "production-status", repo: repo.trim(), branch: branch.trim() || "main",
      }, parsed, (progress, elapsed) => {
        setResult(progress);
        setElapsedSeconds(elapsed);
      });
      setResult(parsed);
      if (parsed.ok && parsed.step === "done") onSuccess();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Terjadi kesalahan tak terduga.");
    } finally {
      setActiveStep(null);
      setSubmitting(false);
    }
  }

  const finished = result?.ok === true && result.step === "done";

  return (
    <Modal title="Update Diri" hidden={hidden} onHide={submitting ? onHide : undefined} onClose={submitting ? onHide : onClose} widthClassName="max-w-lg">
      <form onSubmit={handleSubmit} className="space-y-4">
        <p className="text-xs text-slate-500">
          ZIP disimpan sebelum build diuji di Vercel. Versi sebelumnya tetap bisa diunduh jika update gagal;
          perubahan dikirim ke GitHub hanya setelah uji build lulus, lalu dinyatakan berhasil setelah deployment production Vercel siap.
        </p>

        <div>
          <div className="mb-1.5 flex items-center justify-between">
            <label className="block text-xs font-medium text-slate-400">Repo GitHub</label>
            {!repos.loading && repos.data.length > 0 && !(submitting || finished) && (
              <button
                type="button"
                onClick={() => repos.reload()}
                className="flex items-center gap-1 text-xs font-medium text-accent-blue hover:underline"
              >
                <RefreshCw size={12} />
                Segarkan
              </button>
            )}
          </div>
          <select
            value={repo}
            onChange={(e) => handleRepoSelect(e.target.value)}
            disabled={submitting || finished || repos.loading || repos.data.length === 0}
            className="w-full rounded-xl border border-base-border bg-base-850 px-3 py-2.5 text-sm text-slate-200 focus:border-accent-blue/60 disabled:opacity-60"
          >
            <option value="">
              {repos.loading
                ? "Memuat daftar repo dari GitHub..."
                : repos.data.length === 0
                ? "Tidak ada repo ditemukan"
                : "Pilih repo..."}
            </option>
            {repos.data.map((r) => (
              <option key={r.full_name} value={r.full_name}>
                {r.full_name}
                {r.private ? " (private)" : ""}
              </option>
            ))}
          </select>
          {!repos.loading && repos.data.length === 0 && (
            <p className="mt-1.5 text-xs text-slate-500">
              {repos.isOffline
                ? `Gagal memuat repo dari GitHub: ${repos.error ?? "periksa koneksi atau GITHUB_TOKEN"}.`
                : "Belum ada repo yang terbaca dari GitHub."}{" "}
              <button
                type="button"
                onClick={() => repos.reload()}
                disabled={submitting || finished}
                className="font-medium text-accent-blue hover:underline disabled:opacity-60"
              >
                Coba lagi
              </button>
            </p>
          )}
        </div>

        <div>
          <div className="mb-1.5 flex items-center justify-between">
            <label className="block text-xs font-medium text-slate-400">Branch</label>
            {repo.includes("/") && branches.length > 0 && !(submitting || finished) && (
              <button
                type="button"
                onClick={() => setBranchesReloadKey((k) => k + 1)}
                className="flex items-center gap-1 text-xs font-medium text-accent-blue hover:underline"
              >
                <RefreshCw size={12} />
                Segarkan
              </button>
            )}
          </div>
          {branches.length > 0 ? (
            <select
              value={branch}
              onChange={(e) => setBranch(e.target.value)}
              disabled={submitting || finished}
              className="w-full rounded-xl border border-base-border bg-base-850 px-3 py-2.5 text-sm text-slate-200 focus:border-accent-blue/60 disabled:opacity-60"
            >
              {!branches.includes(branch) && branch && <option value={branch}>{branch}</option>}
              {branches.map((b) => (
                <option key={b} value={b}>
                  {b}
                </option>
              ))}
            </select>
          ) : (
            <input
              value={branch}
              onChange={(e) => setBranch(e.target.value)}
              disabled={submitting || finished || branchesLoading}
              placeholder={branchesLoading ? "Memuat branch dari GitHub..." : "main"}
              className="w-full rounded-xl border border-base-border bg-base-850 px-3 py-2.5 text-sm text-slate-200 focus:border-accent-blue/60 disabled:opacity-60"
            />
          )}
          {!branchesLoading && branchesError && (
            <p className="mt-1.5 text-xs text-slate-500">
              Gagal memuat daftar branch dari GitHub ({branchesError}) — ketik nama branch secara manual.
            </p>
          )}
          {!branchesLoading && !branchesError && repo.includes("/") && branches.length === 0 && (
            <p className="mt-1.5 text-xs text-slate-500">Tidak ada branch terbaca; ketik nama branch secara manual.</p>
          )}
        </div>

        <div>
          <label className="mb-1.5 block text-xs font-medium text-slate-400">File .zip</label>
          <label
            className={`flex items-center gap-3 rounded-xl border border-dashed border-base-border bg-base-850 px-3 py-3 text-sm text-slate-400 hover:border-accent-blue/60 ${
              submitting || finished ? "pointer-events-none opacity-60" : "cursor-pointer"
            }`}
          >
            <UploadCloud size={18} className="shrink-0 text-accent-blue" />
            <span className="truncate">{zipFile ? zipFile.name : "Pilih file .zip untuk diunggah"}</span>
            <input
              type="file"
              accept=".zip"
              className="hidden"
              disabled={submitting || finished}
              onChange={(e) => setZipFile(e.target.files?.[0] ?? null)}
            />
          </label>
        </div>

        <div className="space-y-2 rounded-xl border border-base-border bg-base-850/60 p-3">
          {STEPS.map((s) => {
            const state = stepState(s.key);
            return (
              <div key={s.key} className="flex items-center gap-2 text-sm">
                {state === "done" && <CheckCircle2 size={16} className="text-emerald-400" />}
                {state === "failed" && <XCircle size={16} className="text-red-400" />}
                {state === "active" && <Loader2 size={16} className="animate-spin text-accent-blue" />}
                {state === "pending" && <Circle size={16} className="text-slate-600" />}
                <span
                  className={
                    state === "done"
                      ? "text-emerald-400"
                      : state === "failed"
                      ? "text-red-400"
                      : state === "active"
                      ? "text-slate-200"
                      : "text-slate-500"
                  }
                >
                  {s.label}
                </span>
              </div>
            );
          })}
        </div>

        {result && (
          <div role="status" aria-live="polite" className="space-y-2">
            <p className={`text-xs font-medium ${result.ok ? result.status === "pending" ? "text-accent-blue" : "text-emerald-400" : "text-red-400"}`}>
              {result.message}{result.status === "pending" && ` · ${Math.floor(elapsedSeconds / 60)}m ${elapsedSeconds % 60}s`}
            </p>
            {result.build_log && (
              <div className="rounded-lg border border-red-500/30 bg-base-950 p-3">
                <p className="mb-2 text-xs font-semibold text-red-300">Detail deployment Vercel</p>
                <pre className="max-h-56 overflow-auto whitespace-pre-wrap break-words text-xs text-slate-300">{result.build_log}</pre>
              </div>
            )}
          </div>
        )}
        {submitting && result?.status === "pending" && (
          <p className="text-xs text-slate-500">Status diperiksa otomatis setiap 5 detik. Tekan Hide untuk berpindah halaman dan pantau di Pipeline; biarkan tab tetap terbuka.</p>
        )}
        {error && <p className="text-xs font-medium text-red-400">{error}</p>}
        {archiveSaved && (
          <Link href="/projects#zip-archives" className="text-xs font-medium text-accent-blue hover:underline">
            Lihat dan unduh ZIP tersimpan di Projects
          </Link>
        )}

        <div className="flex justify-end gap-2 pt-1">
          <button
            type="button"
            onClick={onClose}
            disabled={submitting}
            className="rounded-xl border border-base-border px-3.5 py-2 text-sm font-medium text-slate-300 hover:bg-base-800 disabled:opacity-60"
          >
            {finished ? "Tutup" : "Batal"}
          </button>
          {!finished && (
            <button
              type="submit"
              disabled={submitting}
              className="flex items-center gap-2 rounded-xl bg-accent-blue px-3.5 py-2 text-sm font-semibold text-white hover:bg-blue-500 disabled:opacity-60"
            >
              {submitting && <Loader2 size={14} className="animate-spin" />}
              {submitting ? "Memproses..." : "Mulai Update"}
            </button>
          )}
        </div>
      </form>
    </Modal>
  );
}
