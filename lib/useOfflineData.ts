"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { cacheGet, cacheSet } from "./db";
import { subscribeDataChanges } from "./liveUpdates";

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
  const cacheLoaded = useRef(false);
  const requestID = useRef(0);

  const load = useCallback(async () => {
    const id = ++requestID.current;
    if (!cacheLoaded.current) {
      cacheLoaded.current = true;
      // Read the offline snapshot only on the first load. Polls and change
      // notifications must not repaint deleted items from an old snapshot.
      const cached = await cacheGet<T>(key).catch(() => undefined);
      if (cached && mounted.current && id === requestID.current) {
        setData(cached.data);
        setUpdatedAt(cached.updatedAt);
        setLoading(false);
      }
    }
    if (!mounted.current || id !== requestID.current) return;

    let httpFailure = false;
    try {
      const res = await fetch(endpoint, { cache: "no-store" });
      if (!res.ok) {
        httpFailure = true;
        const body = await res.json().catch(() => null);
        throw new Error((body && typeof body.error === "string" && body.error) || `HTTP ${res.status}`);
      }
      const json = (await res.json()) as T;
      if (!mounted.current || id !== requestID.current) return;
      setData(json);
      setIsOffline(false);
      setError(null);
      setUpdatedAt(Date.now());
      await cacheSet(key, json).catch(() => {});
    } catch (err) {
      if (!mounted.current || id !== requestID.current) return;
      setIsOffline(!httpFailure);
      setError(err instanceof Error ? err.message : "Gagal memuat data.");
    } finally {
      if (mounted.current && id === requestID.current) setLoading(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, endpoint]);

  useEffect(() => {
    mounted.current = true;
    void load();
    const onOnline = () => { void load(); };
    const onVisible = () => { if (document.visibilityState === "visible") void load(); };
    window.addEventListener("online", onOnline);
    document.addEventListener("visibilitychange", onVisible);
    const unsubscribe = subscribeDataChanges(onOnline);
    let interval: ReturnType<typeof setInterval> | undefined;
    if (pollMs) interval = setInterval(onVisible, pollMs);
    return () => {
      mounted.current = false;
      requestID.current++;
      cacheLoaded.current = false;
      window.removeEventListener("online", onOnline);
      document.removeEventListener("visibilitychange", onVisible);
      unsubscribe();
      if (interval) clearInterval(interval);
    };
  }, [load, pollMs]);

  return { data, loading, isOffline, error, updatedAt, reload: load };
}
