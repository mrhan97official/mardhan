/** Poll a deployment with short requests so Vercel's function duration does not limit build time. */
export interface BuildProgress {
  step: string;
  ok: boolean;
  message: string;
  status?: string;
  ticket?: string;
}

export async function pollVercelBuild<T extends BuildProgress>(
  endpoint: string,
  fields: Record<string, string>,
  initial: T,
  onProgress: (value: T, elapsedSeconds: number) => void
): Promise<T> {
  let current = initial;
  let failedChecks = 0;
  const started = Date.now();

  while (current.ok && current.status === "pending") {
    if (!current.ticket) throw new Error("Vercel tidak mengembalikan sesi pemantauan build.");
    onProgress(current, Math.floor((Date.now() - started) / 1000));
    await new Promise((resolve) => setTimeout(resolve, 5000));

    try {
      const form = new FormData();
      for (const [key, value] of Object.entries(fields)) form.append(key, value);
      form.append("ticket", current.ticket);
      const response = await fetch(endpoint, { method: "POST", body: form, cache: "no-store" });
      const body = await response.json().catch(() => null);
      if (!response.ok) throw new Error((body && body.error) || `HTTP ${response.status}`);
      if (!body || typeof body.ok !== "boolean" || typeof body.step !== "string") {
        throw new Error("Respons status Vercel tidak valid.");
      }
      current = body as T;
      failedChecks = 0;
    } catch (err) {
      failedChecks++;
      if (failedChecks >= 12) {
        const detail = err instanceof Error ? err.message : "Koneksi terputus.";
        throw new Error(`Status build tidak dapat diperiksa (${detail}). Build mungkin masih berjalan di Vercel; periksa dashboard Vercel sebelum mengulang unggahan.`);
      }
      onProgress({ ...current, message: `Koneksi pemeriksaan status terputus; mencoba lagi (${failedChecks}/12)...` }, Math.floor((Date.now() - started) / 1000));
    }
  }
  onProgress(current, Math.floor((Date.now() - started) / 1000));
  return current;
}
