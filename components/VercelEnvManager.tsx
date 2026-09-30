"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { AlertTriangle, Check, Copy, Eye, EyeOff, KeyRound, Loader2, Pencil, Plus, RefreshCw, Rocket, Search, Trash2, X } from "lucide-react";
import SecurityEnvPanel from "@/components/SecurityEnvPanel";
import { useSession } from "@/lib/session";

type Project = { id: string; name: string; framework?: string; is_self: boolean };
type EnvVar = { id: string; key: string; type: string; target: string[]; git_branch?: string; comment?: string; updated_at?: number; value?: string };
type Target = "production" | "preview" | "development";

const TARGETS: { key: Target; label: string }[] = [
  { key: "production", label: "Production" },
  { key: "preview", label: "Preview" },
  { key: "development", label: "Development" },
];
const TYPE_LABEL: Record<string, string> = { encrypted: "Terenkripsi", sensitive: "Sensitif", plain: "Plain", secret: "Secret", system: "System" };

async function api<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, { cache: "no-store", credentials: "same-origin", ...init });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error((body as { error?: string }).error || `HTTP ${response.status}`);
  return body as T;
}

function TargetPicker({ value, onChange, sensitive }: { value: Target[]; onChange: (next: Target[]) => void; sensitive: boolean }) {
  return (
    <div className="flex flex-wrap gap-2">
      {TARGETS.map((target) => {
        const disabled = sensitive && target.key === "development";
        const checked = value.includes(target.key) && !disabled;
        return (
          <label key={target.key} className={`inline-flex items-center gap-1.5 rounded-lg border px-2 py-1.5 text-xs ${checked ? "border-accent-blue bg-accent-blue/10 text-accent-blue" : "border-base-border text-slate-300"} ${disabled ? "opacity-40" : "cursor-pointer"}`}>
            <input type="checkbox" className="accent-blue-500" disabled={disabled} checked={checked}
              onChange={(event) => onChange(event.target.checked ? [...value, target.key] : value.filter((item) => item !== target.key))} />
            {target.label}
          </label>
        );
      })}
    </div>
  );
}

function EnvForm({ initial, onCancel, onSubmit, busy }: {
  initial?: EnvVar;
  onCancel: () => void;
  onSubmit: (data: { key: string; value: string | null; target: Target[]; type: string }) => void;
  busy: boolean;
}) {
  const editing = Boolean(initial);
  const [key, setKey] = useState(initial?.key ?? "");
  const [value, setValue] = useState(initial?.type === "plain" ? initial.value ?? "" : "");
  const [keepValue, setKeepValue] = useState(editing && initial?.type !== "plain");
  const [type, setType] = useState(initial?.type && ["encrypted", "sensitive", "plain"].includes(initial.type) ? initial.type : "encrypted");
  const [target, setTarget] = useState<Target[]>((initial?.target as Target[] | undefined) ?? ["production", "preview", "development"]);
  const cleanTarget = type === "sensitive" ? target.filter((item) => item !== "development") : target;
  const keyValid = /^[A-Za-z_][A-Za-z0-9_]*$/.test(key.trim());

  return (
    <form className="space-y-2 rounded-xl border border-accent-blue/40 bg-base-850 p-2"
      onSubmit={(event) => { event.preventDefault(); onSubmit({ key: key.trim(), value: keepValue ? null : value, target: cleanTarget, type }); }}>
      <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_10rem]">
        <input value={key} onChange={(event) => setKey(event.target.value.replace(/\s/g, "_"))} disabled={editing} placeholder="NAMA_VARIABEL"
          aria-label="Nama variabel" className="rounded-lg border border-base-border bg-base-900 px-3 py-2 font-mono text-sm disabled:opacity-60" />
        <select value={type} onChange={(event) => setType(event.target.value)} aria-label="Tipe" className="rounded-lg border border-base-border bg-base-900 px-2 py-2 text-sm">
          <option value="encrypted">Terenkripsi</option>
          <option value="sensitive">Sensitif (tak bisa dibaca lagi)</option>
          <option value="plain">Plain</option>
        </select>
      </div>
      {editing && initial?.type !== "plain" && (
        <label className="flex items-center gap-2 text-xs text-slate-300">
          <input type="checkbox" className="accent-blue-500" checked={keepValue} onChange={(event) => setKeepValue(event.target.checked)} />
          Pertahankan nilai lama (hanya ubah environment/tipe)
        </label>
      )}
      {!keepValue && (
        <textarea value={value} onChange={(event) => setValue(event.target.value)} rows={3} placeholder="Nilai" aria-label="Nilai"
          className="w-full rounded-lg border border-base-border bg-base-900 px-3 py-2 font-mono text-xs" />
      )}
      <TargetPicker value={target} onChange={setTarget} sensitive={type === "sensitive"} />
      <div className="flex flex-wrap gap-2">
        <button type="submit" disabled={busy || !keyValid || cleanTarget.length === 0}
          className="inline-flex items-center gap-1.5 rounded-lg bg-accent-blue px-3 py-2 text-xs font-semibold text-white disabled:opacity-50">
          {busy ? <Loader2 size={13} className="animate-spin" /> : <Check size={13} />} {editing ? "Simpan perubahan" : "Tambah variabel"}
        </button>
        <button type="button" onClick={onCancel} className="rounded-lg px-3 py-2 text-xs text-slate-400 hover:bg-base-800">Batal</button>
      </div>
    </form>
  );
}

export default function VercelEnvManager() {
  const { role } = useSession();
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [projectID, setProjectID] = useState("");
  const [envs, setEnvs] = useState<EnvVar[] | null>(null);
  const [revealed, setRevealed] = useState<Record<string, string | null>>({});
  const [revealing, setRevealing] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [editing, setEditing] = useState<string | "new" | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [needsRedeploy, setNeedsRedeploy] = useState(false);
  const [copied, setCopied] = useState<string | null>(null);

  const project = projects?.find((item) => item.id === projectID);

  useEffect(() => {
    api<{ projects: Project[] }>("/api/vercel-env?view=projects")
      .then((body) => { setProjects(body.projects); setProjectID((current) => current || body.projects[0]?.id || ""); })
      .catch((reason) => setError(reason instanceof Error ? reason.message : "Project Vercel gagal dimuat."));
  }, []);

  const loadEnvs = useCallback(async () => {
    if (!projectID) return;
    setLoading(true);
    try {
      const body = await api<{ envs: EnvVar[] }>(`/api/vercel-env?project=${encodeURIComponent(projectID)}`);
      setEnvs(body.envs);
      setError("");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Variabel gagal dimuat.");
    } finally { setLoading(false); }
  }, [projectID]);

  useEffect(() => {
    setEnvs(null); setRevealed({}); setEditing(null); setConfirmDelete(null); setNeedsRedeploy(false); setNotice("");
    void loadEnvs();
  }, [loadEnvs]);

  const filtered = useMemo(() => {
    const term = search.trim().toLowerCase();
    return (envs ?? []).filter((item) => !term || item.key.toLowerCase().includes(term));
  }, [envs, search]);

  async function toggleReveal(item: EnvVar) {
    if (item.id in revealed) { setRevealed(({ [item.id]: _, ...rest }) => rest); return; }
    setRevealing(item.id);
    try {
      const body = await api<{ value?: string; sensitive?: boolean }>(`/api/vercel-env?project=${encodeURIComponent(projectID)}&reveal=${encodeURIComponent(item.id)}`);
      setRevealed((current) => ({ ...current, [item.id]: body.sensitive ? null : body.value ?? "" }));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Nilai gagal dibaca.");
    } finally { setRevealing(null); }
  }

  async function save(item: EnvVar | undefined, data: { key: string; value: string | null; target: Target[]; type: string }) {
    setBusy(true);
    setError("");
    try {
      await api("/api/vercel-env", {
        method: item ? "PATCH" : "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ project: projectID, id: item?.id, key: data.key, value: data.value, target: data.target, type: data.type }),
      });
      setEditing(null);
      setNeedsRedeploy(true);
      setNotice(`${data.key} disimpan.`);
      if (item) setRevealed(({ [item.id]: _, ...rest }) => rest);
      await loadEnvs();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Variabel gagal disimpan.");
    } finally { setBusy(false); }
  }

  async function remove(item: EnvVar) {
    setBusy(true);
    setError("");
    try {
      await api(`/api/vercel-env?project=${encodeURIComponent(projectID)}&id=${encodeURIComponent(item.id)}&key=${encodeURIComponent(item.key)}`, { method: "DELETE" });
      setConfirmDelete(null);
      setNeedsRedeploy(true);
      setNotice(`${item.key} dihapus.`);
      await loadEnvs();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Variabel gagal dihapus.");
    } finally { setBusy(false); }
  }

  async function redeploy() {
    setBusy(true);
    setError("");
    try {
      await api("/api/vercel-env", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ project: projectID, action: "redeploy" }) });
      setNeedsRedeploy(false);
      setNotice("Redeploy production dimulai. Variabel baru aktif setelah build selesai (±1–3 menit).");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Redeploy gagal dimulai.");
    } finally { setBusy(false); }
  }

  async function copy(id: string, value: string) {
    try { await navigator.clipboard.writeText(value); setCopied(id); setTimeout(() => setCopied(null), 1500); } catch { /* clipboard blocked */ }
  }

  return (
    <section className="card min-w-0 space-y-2 p-2" aria-labelledby="vercel-env-title">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 id="vercel-env-title" className="flex items-center gap-2 text-base font-bold text-white sm:text-lg"><KeyRound size={18} /> Environment Variables</h2>
          <p className="text-xs text-slate-500">Langsung dari Vercel. Nilai rahasia hanya dibuka saat Anda menekan ikon mata.</p>
        </div>
        <button type="button" onClick={() => void loadEnvs()} disabled={loading || !projectID} aria-label="Muat ulang"
          className="rounded-lg border border-base-border p-2 hover:bg-base-800 disabled:opacity-50"><RefreshCw size={15} className={loading ? "animate-spin" : ""} /></button>
      </div>

      {projects && projects.length > 0 && (
        <div className="flex flex-wrap gap-2">
          <select value={projectID} onChange={(event) => setProjectID(event.target.value)} aria-label="Project Vercel"
            className="min-w-0 flex-1 rounded-lg border border-base-border bg-base-900 px-2 py-2 font-mono text-sm">
            {projects.map((item) => <option key={item.id} value={item.id}>{item.name}{item.is_self ? " (DevControl ini)" : ""}</option>)}
          </select>
          <label className="relative min-w-[10rem] flex-1">
            <Search size={14} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-slate-500" />
            <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Cari nama variabel…"
              className="w-full rounded-lg border border-base-border bg-base-900 py-2 pl-8 pr-2 text-sm" />
          </label>
          <button type="button" onClick={() => setEditing("new")} disabled={!projectID}
            className="inline-flex items-center gap-1.5 rounded-lg bg-accent-blue px-3 py-2 text-xs font-semibold text-white disabled:opacity-50"><Plus size={14} /> Tambah</button>
        </div>
      )}
      {projects && projects.length === 0 && <p className="text-sm text-slate-400">Tidak ada project Vercel yang dapat diakses VERCEL_TOKEN.</p>}
      {!projects && !error && <p className="flex items-center gap-2 text-sm text-slate-400"><Loader2 size={14} className="animate-spin" /> Memuat project Vercel…</p>}

      {project?.is_self && (
        <p className="flex items-start gap-1.5 rounded-lg border border-amber-400/40 bg-amber-400/10 p-2 text-xs text-amber-200">
          <AlertTriangle size={14} className="mt-0.5 shrink-0" />
          Ini project DevControl sendiri. Mengubah CF_API_TOKEN, DEVCONTROL_ADMIN_PASSWORD, GITHUB_TOKEN, atau VERCEL_TOKEN bisa memutus akses aplikasi ini setelah redeploy.
        </p>
      )}
      {project?.is_self && role === "owner" && envs && (
        <SecurityEnvPanel envs={envs} busy={busy} onSave={(item, data) => save((envs ?? []).find((env) => env.id === item?.id), data)} />
      )}
      {needsRedeploy && (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-accent-blue/40 bg-accent-blue/10 p-2 text-xs text-slate-200">
          <span>Perubahan tersimpan di Vercel, tetapi baru aktif setelah redeploy.</span>
          <button type="button" onClick={() => void redeploy()} disabled={busy}
            className="inline-flex items-center gap-1.5 rounded-lg bg-accent-blue px-3 py-1.5 font-semibold text-white disabled:opacity-50">
            {busy ? <Loader2 size={13} className="animate-spin" /> : <Rocket size={13} />} Redeploy production
          </button>
        </div>
      )}
      {notice && !needsRedeploy && <p className="rounded-lg bg-emerald-400/10 p-2 text-xs text-emerald-300">{notice}</p>}
      {error && <p role="alert" className="rounded-lg bg-red-500/10 p-2 text-xs text-red-300">{error}</p>}

      {editing === "new" && <EnvForm busy={busy} onCancel={() => setEditing(null)} onSubmit={(data) => void save(undefined, data)} />}

      {envs && (
        <ul className="divide-y divide-base-border/70">
          {filtered.length === 0 && <li className="py-6 text-center text-xs text-slate-500">{search ? "Tidak ada variabel yang cocok." : "Project ini belum punya environment variable."}</li>}
          {filtered.map((item) => {
            const shown = item.type === "plain" ? item.value ?? "" : revealed[item.id];
            const isRevealed = item.type === "plain" || item.id in revealed;
            if (editing === item.id) {
              return <li key={item.id} className="py-2"><EnvForm initial={item} busy={busy} onCancel={() => setEditing(null)} onSubmit={(data) => void save(item, data)} /></li>;
            }
            return (
              <li key={item.id} className="space-y-1 py-2">
                <div className="flex flex-wrap items-center gap-1.5">
                  <span className="break-all font-mono text-sm font-semibold text-slate-100">{item.key}</span>
                  <span className="rounded bg-base-800 px-1.5 py-0.5 text-[10px] text-slate-400">{TYPE_LABEL[item.type] ?? item.type}</span>
                  {item.target.map((target) => (
                    <span key={target} className="rounded border border-base-border px-1.5 py-0.5 text-[10px] capitalize text-slate-300">{target}</span>
                  ))}
                  {item.git_branch && <span className="rounded border border-base-border px-1.5 py-0.5 font-mono text-[10px] text-slate-400">{item.git_branch}</span>}
                  <span className="ml-auto flex items-center gap-0.5">
                    {item.type !== "plain" && (
                      <button type="button" onClick={() => void toggleReveal(item)} aria-label={isRevealed ? "Sembunyikan nilai" : "Tampilkan nilai"}
                        className="rounded-lg p-1.5 text-slate-400 hover:bg-base-800 hover:text-white">
                        {revealing === item.id ? <Loader2 size={14} className="animate-spin" /> : isRevealed ? <EyeOff size={14} /> : <Eye size={14} />}
                      </button>
                    )}
                    {isRevealed && typeof shown === "string" && (
                      <button type="button" onClick={() => void copy(item.id, shown)} aria-label="Salin nilai" className="rounded-lg p-1.5 text-slate-400 hover:bg-base-800 hover:text-white">
                        {copied === item.id ? <Check size={14} className="text-emerald-400" /> : <Copy size={14} />}
                      </button>
                    )}
                    <button type="button" onClick={() => { setEditing(item.id); setConfirmDelete(null); }} aria-label={`Edit ${item.key}`} className="rounded-lg p-1.5 text-slate-400 hover:bg-base-800 hover:text-white"><Pencil size={14} /></button>
                    <button type="button" onClick={() => setConfirmDelete(item.id)} aria-label={`Hapus ${item.key}`} className="rounded-lg p-1.5 text-slate-400 hover:bg-red-500/10 hover:text-red-300"><Trash2 size={14} /></button>
                  </span>
                </div>
                <p className="break-all font-mono text-xs text-slate-400">
                  {!isRevealed ? "••••••••••••" : shown === null ? <span className="italic text-slate-500">Nilai sensitif tidak bisa ditampilkan; edit untuk menggantinya.</span> : shown || <span className="italic text-slate-500">(kosong)</span>}
                </p>
                {confirmDelete === item.id && (
                  <div className="flex flex-wrap items-center gap-2 rounded-lg bg-red-500/10 p-2 text-xs text-red-200">
                    Hapus {item.key} dari Vercel?
                    <button type="button" disabled={busy} onClick={() => void remove(item)} className="inline-flex items-center gap-1 rounded-lg bg-red-500/80 px-2.5 py-1.5 font-semibold text-white disabled:opacity-50">
                      {busy && <Loader2 size={12} className="animate-spin" />} Ya, hapus
                    </button>
                    <button type="button" onClick={() => setConfirmDelete(null)} className="inline-flex items-center gap-1 rounded-lg px-2 py-1.5 text-slate-300 hover:bg-base-800"><X size={12} /> Batal</button>
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
