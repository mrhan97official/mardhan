import type { Config } from "tailwindcss";

const config: Config = {
  darkMode: "class",
  content: ["./app/**/*.{ts,tsx}", "./components/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        base: {
          950: "#080D17",
          900: "#0B1220",
          850: "#0E1626",
          800: "#111B2E",
          700: "#182338",
          600: "#243450",
          border: "#1D2A40",
        },
        accent: {
          blue: "#3B82F6",
          cyan: "#22D3EE",
          purple: "#A855F7",
          green: "#22C55E",
          amber: "#F59E0B",
          red: "#EF4444",
        },
      },
      fontFamily: {
        sans: ["Inter", "ui-sans-serif", "system-ui", "sans-serif"],
        mono: ["JetBrains Mono", "ui-monospace", "SFMono-Regular", "monospace"],
      },
      boxShadow: {
        glow: "0 0 0 1px rgba(255,255,255,0.03), 0 8px 24px -8px rgba(0,0,0,0.6)",
      },
      borderRadius: {
        xl2: "1rem",
      },
    },
  },
  plugins: [],
};

export default config;
