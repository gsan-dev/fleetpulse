import type { Config } from "tailwindcss";

// Los valores en si (hex) viven como custom properties en app/globals.css,
// ya separados por modo claro/oscuro; aqui solo se nombran los roles para
// poder escribir `bg-surface`, `text-ink-secondary`, `text-status-good`, etc.
// Paleta y umbrales tomados de la guia de dataviz del proyecto (categorica,
// secuencial y de estado ya validadas para accesibilidad CVD/contraste).
const config: Config = {
  darkMode: "media",
  content: ["./app/**/*.{ts,tsx}", "./components/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        surface: "var(--surface-1)",
        plane: "var(--plane)",
        ink: {
          primary: "var(--text-primary)",
          secondary: "var(--text-secondary)",
          muted: "var(--text-muted)",
        },
        line: {
          grid: "var(--gridline)",
          axis: "var(--baseline)",
          border: "var(--border)",
        },
        status: {
          good: "var(--status-good)",
          warning: "var(--status-warning)",
          serious: "var(--status-serious)",
          critical: "var(--status-critical)",
        },
        series: {
          1: "var(--series-1)", // azul: CPU
          2: "var(--series-2)", // naranja: memoria
          3: "var(--series-3)", // aguamarina: disco
        },
      },
      fontFamily: {
        sans: [
          "system-ui",
          "-apple-system",
          "Segoe UI",
          "sans-serif",
        ],
      },
    },
  },
  plugins: [],
};

export default config;
