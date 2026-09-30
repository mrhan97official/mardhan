"use client";

import { useCallback, useEffect, useState } from "react";
import { AlertTriangle, Check, Copy, KeyRound, Loader2, LogOut, Plus, RefreshCw, RotateCw, ShieldCheck, ShieldOff, Trash2, UserPlus, X } from "lucide-react";
import { ROLE_LABEL, type Role } from "@/lib/session";

type MemberRole = Exclude<Role, "owner">;
type Member = {
  id: string; name: string; role: MemberRole; token_prefix: string; ip_allowlist: string[];
  created_at: string; last_login_at: string; last_ip: string; revoked: boolean;
  expired?: boolean; expires_at?: string; apps?: string[] | null; signature_ok?: boolean;
};
type AuditEvent = { action: string; target: string; created_at: string };
type LockedIP = { ip: string; failures: number; locked_until: string };

const ROLE_INFO: Record<MemberRole, string> = {
  admin: "Semua fitur kecuali mengelola member, 2FA, dan Update Diri: deploy, database, API, environment variable, pengaturan.",
  operator: "Lihat dashboard + Aplikasi Baru / Update Aplikasi dan menutup proses gagal. Bisa dibatasi ke aplikasi tertentu. Tanpa database, env, pengaturan, hapus aplikasi.",
  viewer: "Hanya melihat dashboard, pipeline, environment status, log, dan riwayat. Tidak bisa mengubah apa pun.",
};

const EVENT_LABEL: Record<string, string> = {
  login_owner: "Owner masuk", login_member: "Member masuk", login_failed: "Login gagal",
  member_create: "Member dibuat", member_delete: "Member dihapus", member_rotate: "Token diganti", member_revoke: "Akses dicabut",
  member_restore: "Akses dipulihkan", member_role: "Role diubah", member_ip: "IP diubah", member_unlock_ip: "IP dibuka blokirnya",
  member_apps: "Cakupan aplikasi diubah", member_resign_all: "Tanda tangan member diperbarui",
  sessions_revoke_all: "Semua sesi dikeluarkan",
};

function when(value: string): string {
  if (!value) return "belum pernah";
  const parsed = Date.parse(value.includes("T") ? value : `${value.replace(" ", "T")}Z`);
  return Number.isFinite(parsed) ? new Date(parsed).toLocaleString("id-ID", { dateStyle: "medium", timeStyle: "short" }) : value;
}

function expiryText(member: Member): string {
  if (member.expired) return "Token kedaluwarsa — ganti token";
  if (!member.expires_at) return "Token tanpa masa berlaku — ganti token untuk memberi masa berlaku 90 hari";
  return `Token berlaku s.d. ${when(member.expires_at)}`;
}

// Picks the apps an operator may update ("all" = every app).
function AppScopeEditor({ apps, value, onSave, onCancel, saving, live = false }: {
  apps: string[]; value: string[] | null; saving: boolean; live?: boolean;
  onSave: (all: boolean, chosen: string[]) => void; onCancel?: () => void;
}) {
  const [all, setAll] = useState(value === null);
  const [chosen, setChosen] = useState<string[]>(value ?? []);
  // In the create form the choice applies as it is made (no separate Save).
  useEffect(() => { if (live) onSave(all, chosen); }, [live, all, chosen]); // eslint-disable-line react-hooks/exhaustive-deps
  const toggle = (app: string) => setChosen((list) => list.includes(app) ? list.filter((item) => item !== app) : [...list, app]);
  return (
    <div className="space-y-1.5 rounded-lg border border-base-border bg-base-900 p-2 text-xs">
      <label className="flex items-center gap-1.5 font-medium text-slate-200"><input type="checkbox" checked={all} onChange={(event) => setAll(event.target.checked)} /> Semua aplikasi (termasuk membuat Aplikasi Baru)</label>
      {!all && (
        <div className="grid max-h-40 gap-1 overflow-y-auto sm:grid-cols-2">
          {apps.length === 0 && <p className="text-slate-500">Belum ada aplikasi terdaftar.</p>}
          {apps.map((app) => <label key={app} className="flex items-center gap-1.5 text-slate-300"><input type="checkbox" checked={chosen.includes(app)} onChange={() => toggle(app)} /> <span className="truncate font-mono">{app}</span></label>)}
        </div>
      )}
      {!all && <p className="text-slate-500">Operator dengan daftar ini hanya bisa menekan Update pada aplikasi terpilih dan tidak bisa membuat Aplikasi Baru.</p>}
      {!live && <div className="flex gap-2">
        <button type="button" disabled={saving} onClick={() => onSave(all, chosen)} className="rounded-lg bg-accent-blue px-2.5 py-1.5 font-semibold text-white disabled:opacity-50">Simpan</button>
        {onCancel && <button type="button" onClick={onCancel} className="rounded-lg px-2 py-1.5 text-slate-400 hover:bg-base-800">Batal</button>}
      </div>}
    </div>
  );
}

async function api<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, { cache: "no-store", credentials: "same-origin", ...init });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error((body as { error?: string }).error || `HTTP ${response.status}`);
  return body as T;
}

const send = (method: string, body: unknown) => api<{ token?: string }>("/api/members", { method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });

function TokenReveal({ name, token, onDone }: { name: string; token: string; onDone: () => void }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="space-y-2 rounded-xl border border-emerald-400/40 bg-emerald-400/10 p-2">
      <p className="flex items-center gap-1.5 text-sm font-semibold text-emerald-200"><KeyRound size={15} /> Token akses untuk {name}</p>
      <p className="break-all rounded-lg bg-base-950 p-2 font-mono text-xs text-slate-100">{token}</p>
      <p className="flex items-start gap-1.5 text-xs text-amber-200"><AlertTriangle size={13} className="mt-0.5 shrink-0" /> Token hanya ditampilkan sekali. Kirim lewat saluran pribadi; siapa pun yang memegangnya bisa masuk sesuai role.</p>
      <div className="flex flex-wrap gap-2">
        <button type="button" onClick={() => { void navigator.clipboard.writeText(token).then(() => setCopied(true)).catch(() => {}); }}
          className="inline-flex items-center gap-1.5 rounded-lg bg-accent-blue px-3 py-2 text-xs font-semibold text-white">
          {copied ? <Check size={13} /> : <Copy size={13} />} {copied ? "Tersalin" : "Salin token"}
        </button>
        <button type="button" onClick={onDone} className="rounded-lg px-3 py-2 text-xs text-slate-300 hover:bg-base-800">Sudah saya simpan</button>
      </div>
    </div>
  );
}

export default function MembersManager() {
  const [members, setMembers] = useState<Member[] | null>(null);
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [locked, setLocked] = useState<LockedIP[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [role, setRole] = useState<MemberRole>("viewer");
  const [ips, setIps] = useState("");
  const [issued, setIssued] = useState<{ name: string; token: string } | null>(null);
  const [confirm, setConfirm] = useState<{ id: string; action: "delete" | "revoke" | "rotate" } | null>(null);
  const [editIPs, setEditIPs] = useState<{ id: string; value: string } | null>(null);
  const [confirmLogoutAll, setConfirmLogoutAll] = useState(false);
  const [signatureIssues, setSignatureIssues] = useState(0);
  const [appNames, setAppNames] = useState<string[]>([]);
  const [editApps, setEditApps] = useState<string | null>(null);
  const [newScope, setNewScope] = useState<{ all: boolean; apps: string[] }>({ all: true, apps: [] });

  useEffect(() => {
    void api<{ name: string }[]>("/api/services").then((list) => {
      setAppNames(Array.from(new Set((Array.isArray(list) ? list : []).map((item) => item.name).filter(Boolean))).sort());
    }).catch(() => {});
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const body = await api<{ members: Member[]; events: AuditEvent[]; locked_ips: LockedIP[]; signature_issues?: number }>("/api/members");
      setMembers(body.members); setEvents(body.events); setLocked(body.locked_ips); setSignatureIssues(body.signature_issues ?? 0); setError("");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Data member gagal dimuat.");
    } finally { setLoading(false); }
  }, []);

  useEffect(() => { void load(); }, [load]);

  async function run(key: string, action: () => Promise<void>) {
    setBusy(key); setError("");
    try { await action(); await load(); } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Aksi gagal.");
    } finally { setBusy(null); }
  }

  const create = () => run("create", async () => {
    const scope = role === "operator" && !newScope.all ? { set_apps: true, all_apps: false, apps: newScope.apps } : {};
    const body = await send("POST", { name, role, ip_allowlist: [ips], ...scope });
    if (body.token) setIssued({ name, token: body.token });
    setCreating(false); setName(""); setIps(""); setRole("viewer"); setNewScope({ all: true, apps: [] });
  });

  const act = (member: Member, action: "delete" | "revoke" | "restore" | "rotate") => run(member.id + action, async () => {
    if (action === "delete") {
      await api(`/api/members?id=${member.id}`, { method: "DELETE" });
    } else {
      const body = await send("PATCH", { id: member.id, action });
      if (action === "rotate" && body.token) setIssued({ name: member.name, token: body.token });
    }
    setConfirm(null);
  });

  const logoutAll = () => run("logout-all", async () => {
    await send("POST", { action: "revoke_all_sessions" });
    window.dispatchEvent(new Event("devcontrol:logout"));
  });

  return (
    <div className="space-y-2">
      <section className="card space-y-2 p-2" aria-labelledby="members-title">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h2 id="members-title" className="flex items-center gap-2 text-base font-bold text-white sm:text-lg"><ShieldCheck size={18} /> Member & role akses</h2>
            <p className="text-xs text-slate-500">Member masuk dengan token akses panjang (256-bit). Bisa dikunci ke IP/perangkat tertentu.</p>
          </div>
          <div className="flex gap-2">
            <button type="button" onClick={() => void load()} aria-label="Muat ulang" className="rounded-lg border border-base-border p-2 hover:bg-base-800"><RefreshCw size={15} className={loading ? "animate-spin" : ""} /></button>
            <button type="button" onClick={() => setCreating((value) => !value)} className="inline-flex items-center gap-1.5 rounded-lg bg-accent-blue px-3 py-2 text-xs font-semibold text-white"><UserPlus size={14} /> Tambah member</button>
          </div>
        </div>

        {error && <p role="alert" className="rounded-lg bg-red-500/10 p-2 text-xs text-red-300">{error}</p>}
        {signatureIssues > 0 && (
          <div role="alert" className="space-y-1.5 rounded-xl border border-red-500/50 bg-red-600/10 p-2 text-xs text-red-200">
            <p className="font-semibold">{signatureIssues} baris member tidak cocok dengan tanda tangannya, sehingga login-nya ditolak.</p>
            <p>Kalau Anda baru saja mengganti DEVCONTROL_SESSION_SECRET, tandatangani ulang. Kalau tidak, periksa dulu member yang ditandai — kemungkinan database diubah dari luar DevControl, dan member asing sebaiknya dihapus.</p>
            <button type="button" disabled={busy !== null} onClick={() => void run("resign", async () => { await send("POST", { action: "resign_all" }); })}
              className="rounded-lg bg-red-600 px-2.5 py-1.5 font-semibold text-white disabled:opacity-50">{busy === "resign" ? "Menandatangani…" : "Tandatangani ulang semua"}</button>
          </div>
        )}
        {issued && <TokenReveal name={issued.name} token={issued.token} onDone={() => setIssued(null)} />}

        {creating && (
          <form onSubmit={(event) => { event.preventDefault(); void create(); }} className="space-y-2 rounded-xl border border-accent-blue/40 bg-base-850 p-2">
            <input value={name} onChange={(event) => setName(event.target.value)} maxLength={60} required placeholder="Nama member (mis. Budi – tim frontend)"
              className="w-full rounded-lg border border-base-border bg-base-900 px-3 py-2 text-sm" />
            <div className="grid gap-2 sm:grid-cols-3">
              {(Object.keys(ROLE_INFO) as MemberRole[]).map((item) => (
                <label key={item} className={`cursor-pointer rounded-xl border p-2 text-xs ${role === item ? "border-accent-blue bg-accent-blue/10" : "border-base-border"}`}>
                  <span className="flex items-center gap-1.5 font-semibold text-slate-100"><input type="radio" name="role" className="accent-blue-500" checked={role === item} onChange={() => setRole(item)} /> {ROLE_LABEL[item]}</span>
                  <span className="mt-1 block text-slate-400">{ROLE_INFO[item]}</span>
                </label>
              ))}
            </div>
            {role === "operator" && (
              <AppScopeEditor live apps={appNames} value={newScope.all ? null : newScope.apps} saving={false}
                onSave={(all, chosen) => setNewScope({ all, apps: chosen })} />
            )}
            <textarea value={ips} onChange={(event) => setIps(event.target.value)} rows={2}
              placeholder="Opsional: kunci ke IP/perangkat, mis. 36.73.12.4 atau 103.10.0.0/24 (pisahkan koma/baris)"
              className="w-full rounded-lg border border-base-border bg-base-900 px-3 py-2 font-mono text-xs" />
            <div className="flex gap-2">
              <button type="submit" disabled={busy === "create" || !name.trim()} className="inline-flex items-center gap-1.5 rounded-lg bg-accent-blue px-3 py-2 text-xs font-semibold text-white disabled:opacity-50">
                {busy === "create" ? <Loader2 size={13} className="animate-spin" /> : <Plus size={13} />} Buat & tampilkan token
              </button>
              <button type="button" onClick={() => setCreating(false)} className="rounded-lg px-3 py-2 text-xs text-slate-400 hover:bg-base-800">Batal</button>
            </div>
          </form>
        )}

        {members && members.length === 0 && !creating && <p className="rounded-xl border border-base-border bg-base-850 p-2 text-sm text-slate-400">Belum ada member. Hanya owner (kata sandi admin) yang bisa masuk.</p>}

        {members && members.length > 0 && (
          <ul className="divide-y divide-base-border/70">
            {members.map((member) => (
              <li key={member.id} className={`space-y-1.5 py-2 ${member.revoked ? "opacity-60" : ""}`}>
                <div className="flex flex-wrap items-center gap-1.5">
                  <span className="text-sm font-semibold text-slate-100">{member.name}</span>
                  {member.revoked && !member.expired && <span className="rounded-full bg-red-500/15 px-2 py-0.5 text-[10px] font-semibold text-red-300">Dicabut</span>}
                  {member.expired && <span className="rounded-full bg-amber-400/15 px-2 py-0.5 text-[10px] font-semibold text-amber-300">Kedaluwarsa</span>}
                  {member.signature_ok === false && <span className="rounded-full bg-red-600/20 px-2 py-0.5 text-[10px] font-semibold text-red-300">Tanda tangan tidak sah</span>}
                  <select value={member.role} disabled={busy !== null || member.revoked} aria-label={`Role ${member.name}`}
                    onChange={(event) => void run(member.id + "role", async () => { await send("PATCH", { id: member.id, role: event.target.value }); })}
                    className="rounded-lg border border-base-border bg-base-900 px-2 py-1 text-xs">
                    {(Object.keys(ROLE_INFO) as MemberRole[]).map((item) => <option key={item} value={item}>{ROLE_LABEL[item]}</option>)}
                  </select>
                  <span className="font-mono text-[11px] text-slate-500">{member.token_prefix}…</span>
                </div>
                <p className="text-xs text-slate-400">Login terakhir: {when(member.last_login_at)}{member.last_ip && <> dari <span className="font-mono">{member.last_ip}</span></>}</p>
                <p className={`text-xs ${member.expired || !member.expires_at ? "text-amber-300" : "text-slate-400"}`}>{expiryText(member)}</p>
                {member.role === "operator" && (editApps === member.id ? (
                  <AppScopeEditor apps={appNames} value={member.apps ?? null} saving={busy !== null} onCancel={() => setEditApps(null)}
                    onSave={(all, chosen) => void run(member.id + "apps", async () => { await send("PATCH", { id: member.id, set_apps: true, all_apps: all, apps: chosen }); setEditApps(null); })} />
                ) : (
                  <p className="text-xs text-slate-400">
                    Aplikasi: {member.apps ? (member.apps.length ? <span className="font-mono text-slate-300">{member.apps.join(", ")}</span> : "tidak ada") : "semua"}
                    <button type="button" onClick={() => setEditApps(member.id)} className="ml-2 text-accent-blue hover:underline">ubah</button>
                  </p>
                ))}
                {editIPs?.id === member.id ? (
                  <div className="flex flex-wrap gap-2">
                    <input value={editIPs.value} onChange={(event) => setEditIPs({ id: member.id, value: event.target.value })} placeholder="Kosongkan = semua IP"
                      className="min-w-0 flex-1 rounded-lg border border-base-border bg-base-900 px-2 py-1.5 font-mono text-xs" />
                    <button type="button" onClick={() => void run(member.id + "ip", async () => { await send("PATCH", { id: member.id, set_ip_allowlist: true, ip_allowlist: [editIPs.value] }); setEditIPs(null); })}
                      className="rounded-lg bg-accent-blue px-2.5 py-1.5 text-xs font-semibold text-white">Simpan</button>
                    <button type="button" onClick={() => setEditIPs(null)} className="rounded-lg px-2 py-1.5 text-xs text-slate-400 hover:bg-base-800"><X size={13} /></button>
                  </div>
                ) : (
                  <p className="text-xs text-slate-400">
                    IP diizinkan: {member.ip_allowlist.length ? <span className="font-mono text-slate-300">{member.ip_allowlist.join(", ")}</span> : "semua"}
                    <button type="button" onClick={() => setEditIPs({ id: member.id, value: member.ip_allowlist.join(", ") })} className="ml-2 text-accent-blue hover:underline">ubah</button>
                  </p>
                )}
                {confirm?.id === member.id ? (
                  <div className="flex flex-wrap items-center gap-2 rounded-lg bg-red-500/10 p-2 text-xs text-red-200">
                    {confirm.action === "delete" ? "Hapus member ini permanen?" : confirm.action === "revoke" ? "Cabut akses & keluarkan dari semua perangkat?" : "Ganti token? Token lama langsung tidak berlaku."}
                    <button type="button" disabled={busy !== null} onClick={() => void act(member, confirm.action)} className="inline-flex items-center gap-1 rounded-lg bg-red-500/80 px-2.5 py-1.5 font-semibold text-white disabled:opacity-50">
                      {busy === member.id + confirm.action && <Loader2 size={12} className="animate-spin" />} Ya
                    </button>
                    <button type="button" onClick={() => setConfirm(null)} className="rounded-lg px-2 py-1.5 text-slate-300 hover:bg-base-800">Batal</button>
                  </div>
                ) : (
                  <div className="flex flex-wrap gap-1.5">
                    <button type="button" onClick={() => setConfirm({ id: member.id, action: "rotate" })} className="inline-flex items-center gap-1 rounded-lg border border-base-border px-2 py-1 text-xs text-slate-300 hover:bg-base-800"><RotateCw size={12} /> Ganti token</button>
                    {member.revoked ? (
                      <button type="button" onClick={() => void act(member, "restore")} className="inline-flex items-center gap-1 rounded-lg border border-base-border px-2 py-1 text-xs text-emerald-300 hover:bg-base-800"><ShieldCheck size={12} /> Pulihkan</button>
                    ) : (
                      <button type="button" onClick={() => setConfirm({ id: member.id, action: "revoke" })} className="inline-flex items-center gap-1 rounded-lg border border-base-border px-2 py-1 text-xs text-amber-300 hover:bg-base-800"><ShieldOff size={12} /> Cabut akses</button>
                    )}
                    <button type="button" onClick={() => setConfirm({ id: member.id, action: "delete" })} className="inline-flex items-center gap-1 rounded-lg border border-base-border px-2 py-1 text-xs text-red-300 hover:bg-red-500/10"><Trash2 size={12} /> Hapus</button>
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="card space-y-2 p-2" aria-labelledby="security-title">
        <h2 id="security-title" className="flex items-center gap-2 text-base font-bold text-white"><ShieldCheck size={18} /> Keamanan login</h2>
        <p className="text-xs text-slate-400">5 kali gagal login dari satu IP → IP dikunci 1, 2, 4 … hingga 60 menit. Semua login tercatat.</p>
        {locked.length > 0 && (
          <ul className="space-y-1">
            {locked.map((item) => (
              <li key={item.ip} className="flex flex-wrap items-center gap-2 rounded-lg bg-amber-400/10 p-2 text-xs text-amber-200">
                <span className="font-mono">{item.ip}</span> · {item.failures} gagal · terkunci s.d. {when(item.locked_until)}
                <button type="button" onClick={() => void run("unlock" + item.ip, async () => { await send("POST", { action: "unlock_ip", ip: item.ip }); })} className="ml-auto text-accent-blue hover:underline">Buka blokir</button>
              </li>
            ))}
          </ul>
        )}
        {confirmLogoutAll ? (
          <div className="flex flex-wrap items-center gap-2 rounded-lg bg-red-500/10 p-2 text-xs text-red-200">
            Keluarkan semua orang (termasuk Anda) dari semua perangkat?
            <button type="button" disabled={busy !== null} onClick={() => void logoutAll()} className="rounded-lg bg-red-500/80 px-2.5 py-1.5 font-semibold text-white disabled:opacity-50">Ya, keluarkan semua</button>
            <button type="button" onClick={() => setConfirmLogoutAll(false)} className="rounded-lg px-2 py-1.5 text-slate-300 hover:bg-base-800">Batal</button>
          </div>
        ) : (
          <button type="button" onClick={() => setConfirmLogoutAll(true)} className="inline-flex items-center gap-1.5 rounded-lg border border-red-400/40 px-3 py-2 text-xs font-semibold text-red-300 hover:bg-red-500/10"><LogOut size={13} /> Keluarkan semua sesi</button>
        )}
        {events.length > 0 && (
          <div>
            <p className="mb-1 text-xs font-semibold text-slate-300">Aktivitas akses terbaru</p>
            <ul className="max-h-72 space-y-1 overflow-y-auto">
              {events.map((event, index) => (
                <li key={index} className="flex flex-wrap gap-x-2 text-xs">
                  <span className={event.action === "login_failed" ? "text-red-300" : "text-slate-200"}>{EVENT_LABEL[event.action] ?? event.action}</span>
                  <span className="break-all font-mono text-slate-400">{event.target}</span>
                  <span className="ml-auto text-slate-500">{when(event.created_at)}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
      </section>
    </div>
  );
}
