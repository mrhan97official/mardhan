"use client";

import { createContext, useContext, useEffect, useState, type ReactNode } from "react";

type Theme = "dark" | "light";
type ThemeContextValue = { theme: Theme; setTheme: (next: Theme) => void };
const ThemeContext = createContext<ThemeContextValue | null>(null);
const storageKey = "devcontrol-theme";

function applyTheme(theme: Theme) {
  const root = document.documentElement;
  root.classList.toggle("dark", theme === "dark");
  root.classList.toggle("light", theme === "light");
  document.querySelector('meta[name="theme-color"]')?.setAttribute("content", theme === "light" ? "#F8FCFF" : "#080D17");
  document.querySelector('meta[name="apple-mobile-web-app-status-bar-style"]')?.setAttribute("content", theme === "light" ? "default" : "black");
}

export function useTheme() {
  const context = useContext(ThemeContext);
  if (!context) throw new Error("ThemeProvider belum tersedia");
  return context;
}

export default function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, updateTheme] = useState<Theme>("dark");

  useEffect(() => {
    const stored = (() => { try { return localStorage.getItem(storageKey); } catch { return null; } })();
    const initial: Theme = stored === "light" ? "light" : "dark";
    updateTheme(initial);
    applyTheme(initial);
    const onStorage = (event: StorageEvent) => {
      if (event.key !== storageKey) return;
      const next: Theme = event.newValue === "light" ? "light" : "dark";
      updateTheme(next);
      applyTheme(next);
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  function setTheme(next: Theme) {
    updateTheme(next);
    applyTheme(next);
    try { localStorage.setItem(storageKey, next); } catch { /* The choice still works for this tab. */ }
  }

  return <ThemeContext.Provider value={{ theme, setTheme }}>{children}</ThemeContext.Provider>;
}
