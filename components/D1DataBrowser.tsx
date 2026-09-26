"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ArrowDown, ArrowUp, ChevronLeft, ChevronRight, Copy, Download, KeyRound, Loader2, RefreshCw, Search, Table2, X } from "lucide-react";

type TableCount = { name: string; rows: number };
type Column = { name: string; type: string; pk: boolean; masked: boolean };
type Row = Record<string, unknown>;
type Page = { table: string; columns: Column[]; rows: Row[]; total: number; offset: number; limit: number };

async function getJSON<T>(url: string): Promise<T> {
  const response = await fetch(url, { cache: "no-store", credentials: "same-origin" });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error((body as { error?: string }).error || `HTTP ${response.status}`);
  return body as T;
}

function display(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

function csvCell(value: unknown): string {
  const text = display(value);
  return /[",\n\r]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text;
}

export default function D1DataBrowser() {
  const [tables, setTables] = useState<TableCount[] | null>(null);
  const [table, setTable] = useState("");
  const [page, setPage] = useState<Page | null>(null);
  const [search, setSearch] = useState("");
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState<{ column: string; dir: "asc" | "desc" } | null>(null);
  const [offset, setOffset] = useState(0);
  const [limit, setLimit] = useState(50);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [detail, setDetail] = useState<Row | null>(null);
  const [copied, setCopied] = useState(false);
  const request = useRef(0);

  const loadTables = useCallback(async () => {
    try {
      const body = await getJSON<{ tables: TableCount[] }>("/api/databases?view=tables");
      setTables(body.tables);
      setTable((current) => current || body.tables.find((item) => item.rows > 0)?.name || body.tables[0]?.name || "");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Daftar tabel gagal dimuat.");
    }
  }, []);

  const loadRows = useCallback(async () => {
    if (!table) return;
    const id = ++request.current;
    setLoading(true);
    const params = new URLSearchParams({ table, offset: String(offset), limit: String(limit) });
    if (query) params.set("q", query);
    if (sort) { params.set("sort", sort.column); params.set("dir", sort.dir); }
    try {
      const body = await getJSON<Page>(`/api/databases?${params.toString()}`);
      if (id !== request.current) return; // a newer request superseded this one
      setPage(body);
      setError("");
    } catch (reason) {
      if (id === request.current) setError(reason instanceof Error ? reason.message : "Data gagal dimuat.");
    } finally {
      if (id === request.current) setLoading(false);
    }
  }, [table, offset, limit, query, sort]);

  useEffect(() => { void loadTables(); }, [loadTables]);
  useEffect(() => { void loadRows(); }, [loadRows]);

  // Debounce typing so each keystroke doesn't hit D1.
  useEffect(() => {
    const timer = setTimeout(() => { setOffset(0); setQuery(search.trim()); }, 400);
    return () => clearTimeout(timer);
  }, [search]);

  function pickTable(name: string) {
    if (name === table) return;
    setTable(name);
    setPage(null);
    setOffset(0);
    setSort(null);
    setSearch("");
    setQuery("");
  }

  function toggleSort(column: string) {
    setOffset(0);
    setSort((current) => current?.column !== column ? { column, dir: "asc" } : current.dir === "asc" ? { column, dir: "desc" } : null);
  }

  function exportCSV() {
    if (!page) return;
    const header = page.columns.map((column) => csvCell(column.name)).join(",");
    const body = page.rows.map((row) => page.columns.map((column) => csvCell(row[column.name])).join(",")).join("\n");
    const blob = new Blob([`${header}\n${body}\n`], { type: "text/csv;charset=utf-8" });
    const link = document.createElement("a");
    link.href = URL.createObjectURL(blob);
    link.download = `${page.table}-${page.offset + 1}-${page.offset + page.rows.length}.csv`;
    link.click();
    setTimeout(() => URL.revokeObjectURL(link.href), 1000);
  }

  async function copyRow() {
    if (!detail) return;
    try { await navigator.clipboard.writeText(JSON.stringify(detail, null, 2)); setCopied(true); setTimeout(() => setCopied(false), 2000); } catch { /* clipboard blocked */ }
  }

  const range = useMemo(() => {
    if (!page || page.total === 0) return "0 baris";
    return `${page.offset + 1}–${page.offset + page.rows.length} dari ${page.total.toLocaleString("id-ID")}`;
  }, [page]);

  return (
    <section className="card min-w-0 space-y-2 p-2" aria-labelledby="data-browser-title">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 id="data-browser-title" className="flex items-center gap-2 text-base font-semibold"><Table2 size={18} /> Data tabel D1</h2>
          <p className="mt-0.5 text-xs text-slate-400">Hanya-baca. Kolom rahasia (hash, token, kata sandi, tiket) disamarkan.</p>
        </div>
        <button type="button" onClick={() => { void loadTables(); void loadRows(); }} aria-label="Muat ulang data"
          className="rounded-lg border border-base-border p-2 hover:bg-base-800"><RefreshCw size={15} className={loading ? "animate-spin" : ""} /></button>
      </div>

      {!tables && !error && <p className="text-sm text-slate-400">Membaca daftar tabel…</p>}
      {tables && tables.length === 0 && <p className="text-sm text-slate-400">Database belum memiliki tabel. Jalankan "Siapkan D1 + R2" terlebih dahulu.</p>}

      {tables && tables.length > 0 && (
        <>
          <select value={table} onChange={(event) => pickTable(event.target.value)} aria-label="Pilih tabel"
            className="w-full rounded-lg border border-base-border bg-base-900 p-2 font-mono text-sm sm:hidden">
            {tables.map((item) => <option key={item.name} value={item.name}>{item.name} ({item.rows < 0 ? "?" : item.rows})</option>)}
          </select>
          <div className="hidden flex-wrap gap-1.5 sm:flex" role="tablist" aria-label="Tabel">
            {tables.map((item) => (
              <button key={item.name} type="button" role="tab" aria-selected={item.name === table} onClick={() => pickTable(item.name)}
                className={`rounded-lg border px-2.5 py-1 font-mono text-xs ${item.name === table ? "border-accent-blue bg-accent-blue/15 text-accent-blue" : "border-base-border text-slate-300 hover:bg-base-800"}`}>
                {item.name} <span className="text-slate-500">{item.rows < 0 ? "?" : item.rows.toLocaleString("id-ID")}</span>
              </button>
            ))}
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <label className="relative min-w-0 flex-1">
              <Search size={14} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-slate-500" />
              <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Cari di semua kolom…"
                className="w-full rounded-lg border border-base-border bg-base-900 py-2 pl-8 pr-2 text-sm" />
            </label>
            <select value={limit} onChange={(event) => { setOffset(0); setLimit(Number(event.target.value)); }} aria-label="Baris per halaman"
              className="rounded-lg border border-base-border bg-base-900 p-2 text-sm">
              {[25, 50, 100, 200].map((size) => <option key={size} value={size}>{size} / hal</option>)}
            </select>
            <button type="button" onClick={exportCSV} disabled={!page?.rows.length}
              className="inline-flex items-center gap-1.5 rounded-lg border border-base-border px-2.5 py-2 text-xs font-semibold disabled:opacity-40">
              <Download size={14} /> CSV
            </button>
          </div>
        </>
      )}

      {error && <p role="alert" className="rounded-lg bg-red-500/10 p-2 text-sm text-red-300">{error}</p>}

      {page && (
        <div className="relative overflow-x-auto rounded-lg border border-base-border">
          {loading && <div className="absolute inset-0 z-10 flex items-center justify-center bg-base-950/40"><Loader2 size={20} className="animate-spin text-accent-blue" /></div>}
          <table className="w-full min-w-max border-collapse text-left text-xs">
            <thead className="sticky top-0 bg-base-900">
              <tr>
                {page.columns.map((column) => (
                  <th key={column.name} scope="col" className="border-b border-base-border px-2.5 py-2 font-semibold text-slate-300">
                    <button type="button" onClick={() => toggleSort(column.name)} className="inline-flex items-center gap-1 hover:text-white" title={column.type || "tanpa tipe"}>
                      {column.pk && <KeyRound size={11} className="text-amber-300" />}
                      <span className="font-mono">{column.name}</span>
                      {sort?.column === column.name && (sort.dir === "asc" ? <ArrowUp size={11} /> : <ArrowDown size={11} />)}
                    </button>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {page.rows.length === 0 && (
                <tr><td colSpan={page.columns.length} className="px-2.5 py-6 text-center text-slate-500">{query ? "Tidak ada baris yang cocok." : "Tabel masih kosong."}</td></tr>
              )}
              {page.rows.map((row, index) => (
                <tr key={index} onClick={() => setDetail(row)} className="cursor-pointer border-b border-base-border/60 odd:bg-base-900/40 hover:bg-accent-blue/10">
                  {page.columns.map((column) => {
                    const value = row[column.name];
                    return (
                      <td key={column.name} className="max-w-[18rem] truncate px-2.5 py-1.5 font-mono text-slate-200" title={display(value)}>
                        {value === null || value === undefined ? <span className="italic text-slate-500">NULL</span> : display(value)}
                      </td>
                    );
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {page && (
        <div className="flex items-center justify-between gap-2 text-xs text-slate-400">
          <span>{range}</span>
          <div className="flex gap-1.5">
            <button type="button" disabled={offset === 0 || loading} onClick={() => setOffset(Math.max(0, offset - limit))} aria-label="Halaman sebelumnya"
              className="rounded-lg border border-base-border p-1.5 disabled:opacity-40"><ChevronLeft size={15} /></button>
            <button type="button" disabled={offset + limit >= page.total || loading} onClick={() => setOffset(offset + limit)} aria-label="Halaman berikutnya"
              className="rounded-lg border border-base-border p-1.5 disabled:opacity-40"><ChevronRight size={15} /></button>
          </div>
        </div>
      )}

      {detail && page && (
        <div className="fixed inset-0 z-[60] flex items-end justify-center p-2 sm:items-center" role="dialog" aria-modal="true" aria-label="Detail baris">
          <button aria-label="Tutup" onClick={() => setDetail(null)} className="absolute inset-0 bg-black/70 backdrop-blur-sm" />
          <div className="card relative max-h-[85vh] w-full max-w-2xl overflow-y-auto p-2">
            <div className="mb-2 flex items-center justify-between gap-2">
              <h3 className="font-mono text-sm font-bold text-white">{page.table}</h3>
              <div className="flex items-center gap-1">
                <button type="button" onClick={() => void copyRow()} className="inline-flex items-center gap-1 rounded-lg px-2 py-1.5 text-xs font-semibold text-accent-blue hover:bg-base-800">
                  <Copy size={13} /> {copied ? "Tersalin" : "Salin JSON"}
                </button>
                <button type="button" aria-label="Tutup" onClick={() => setDetail(null)} className="rounded-lg p-1.5 text-slate-400 hover:bg-base-800"><X size={17} /></button>
              </div>
            </div>
            <dl className="space-y-2">
              {page.columns.map((column) => (
                <div key={column.name} className="rounded-lg bg-base-900/60 p-2">
                  <dt className="flex items-center gap-1 font-mono text-[11px] text-slate-500">{column.pk && <KeyRound size={10} className="text-amber-300" />}{column.name} <span className="text-slate-600">{column.type}</span></dt>
                  <dd className="mt-0.5 whitespace-pre-wrap break-words font-mono text-xs text-slate-100">
                    {detail[column.name] === null || detail[column.name] === undefined ? <span className="italic text-slate-500">NULL</span> : display(detail[column.name])}
                  </dd>
                </div>
              ))}
            </dl>
          </div>
        </div>
      )}
    </section>
  );
}
