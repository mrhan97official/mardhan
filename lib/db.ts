import { openDB, type DBSchema, type IDBPDatabase } from "idb";

interface DevControlDB extends DBSchema {
  cache: {
    key: string;
    value: { data: unknown; updatedAt: number };
  };
}

const DB_NAME = "devcontrol-db";
const DB_VERSION = 1;

let dbPromise: Promise<IDBPDatabase<DevControlDB>> | null = null;

function getDB() {
  if (typeof window === "undefined") return null;
  if (!dbPromise) {
    dbPromise = openDB<DevControlDB>(DB_NAME, DB_VERSION, {
      upgrade(db) {
        if (!db.objectStoreNames.contains("cache")) {
          db.createObjectStore("cache");
        }
      },
    });
  }
  return dbPromise;
}

export async function cacheSet<T>(key: string, data: T): Promise<void> {
  const db = await getDB();
  if (!db) return;
  await db.put("cache", { data, updatedAt: Date.now() }, key);
}

export async function cacheGet<T>(
  key: string
): Promise<{ data: T; updatedAt: number } | undefined> {
  const db = await getDB();
  if (!db) return undefined;
  return (await db.get("cache", key)) as
    | { data: T; updatedAt: number }
    | undefined;
}

export async function cacheClear(): Promise<void> {
  const db = await getDB();
  if (!db) return;
  await db.clear("cache");
}
