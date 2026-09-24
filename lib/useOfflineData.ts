"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { cacheGet, cacheSet } from "./db";

interface OfflineDataState<T> {
  data: T;
  loading: boolean;
  isOffline: boolean;
  error: string | null;
  updatedAt: number | null;
  reload: () => void;
}

/**
 * Offline-first fetch:
 * 1. Paint instantly from IndexedDB cache (if present).
 * 2. Attempt a network fetch; on success, update state + cache.
 * 3. On network failure, silently keep serving cached/fallback data
 *    and flag `isOffline` so the UI can show a subtle indicator.
 */
export function useOfflineData<T>(
  key: string,
  endpoint: string,
  fallback: T,
  pollMs?: number
): OfflineDataState<T> {
  const [data, setData] = useState<T>(fallback);
  const [loading, setLoading] = useState(true);
  const [isOffline, setIsOffline] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [updatedAt, setUpdatedAt] = useState<number | null>(null);
  const mounted = useRef(true);

  const load = useCallback(async () => {
    // IndexedDB can be disabled by the browser. It must never prevent the
    // request to GitHub (or any other live endpoint) from running.
    const cached = await cacheGet<T>(key).catch(() => undefined);
    if (cached && mounted.current) {
      setData(cached.data);
      setUpdatedAt(cached.updatedAt);
      setLoading(false);
    }

    let httpFailure = false;
    try {
      const res = await fetch(endpoint, { cache: "no-store" });
      if (!res.ok) {
        httpFailure = true;
        const body = await res.json().catch(() => null);
        throw new Error((body && typeof body.error === "string" && body.error) || `HTTP ${res.status}`);
      }
      const json = (await res.json()) as T;
      if (!mounted.current) return;
      setData(json);
      setIsOffline(false);
      setError(null);
      setUpdatedAt(Date.now());
      await cacheSet(key, json).catch(() => {});
    } catch (err) {
      if (!mounted.current) return;
      setIsOffline(!httpFailure);
      setError(err instanceof Error ? err.message : "Gagal memuat data.");
      if (!cached) setData(fallback);
    } finally {
      if (mounted.current) setLoading(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, endpoint]);

  useEffect(() => {
    mounted.current = true;
    load();
    const onOnline = () => load();
    window.addEventListener("online", onOnline);
    let interval: ReturnType<typeof setInterval> | undefined;
    if (pollMs) interval = setInterval(load, pollMs);
    return () => {
      mounted.current = false;
      window.removeEventListener("online", onOnline);
      if (interval) clearInterval(interval);
    };
  }, [load, pollMs]);

  return { data, loading, isOffline, error, updatedAt, reload: load };
}
