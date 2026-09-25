import type { DeploymentJob } from "./types";

// This only observes D1. The Cloudflare runner advances the job even if the
// browser closes, loses connectivity or this polling loop stops.
export async function pollDeploymentJob(
  id: string,
  onProgress: (job: DeploymentJob, elapsed: number) => void,
): Promise<DeploymentJob> {
  const started = Date.now();
  let failures = 0;
  while (true) {
    await new Promise((resolve) => setTimeout(resolve, 5000));
    try {
      const response = await fetch("/api/deployments", { cache: "no-store" });
      const body = await response.json().catch(() => null);
      if (!response.ok || !Array.isArray(body)) throw new Error(body?.error ?? `HTTP ${response.status}`);
      const job = (body as DeploymentJob[]).find((item) => item.id === id);
      if (!job) throw new Error("Pipeline sudah tidak tersedia; periksa arsip dan proyek.");
      failures = 0;
      onProgress(job, Math.floor((Date.now() - started) / 1000));
      if (job.status !== "Running") return job;
    } catch (error) {
      failures++;
      if (failures >= 12) {
        throw new Error(`Status tidak dapat dipantau (${error instanceof Error ? error.message : "koneksi terputus"}). Deployment tetap berjalan di server; periksa Pipeline kembali.`);
      }
    }
  }
}
