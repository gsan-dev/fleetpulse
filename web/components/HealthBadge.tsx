import type { Health } from "@/lib/types";

// El color nunca es la unica senal (regla de accesibilidad de la guia de
// dataviz): siempre va acompanado de un icono y una etiqueta de texto.
const HEALTH_CONFIG: Record<Health, { label: string; dot: string; text: string; icon: string }> = {
  healthy: { label: "Saludable", dot: "bg-status-good", text: "text-status-good", icon: "●" },
  warning: { label: "Alerta de recursos", dot: "bg-status-warning", text: "text-[#9a6a00] dark:text-status-warning", icon: "▲" },
  unreachable: { label: "Desconectado", dot: "bg-status-critical", text: "text-status-critical", icon: "✕" },
  unknown: { label: "Sin datos", dot: "bg-ink-muted", text: "text-ink-muted", icon: "?" },
};

export function HealthDot({ health }: { health: Health }) {
  const config = HEALTH_CONFIG[health];
  return <span className={`inline-block h-2.5 w-2.5 rounded-full ${config.dot}`} aria-hidden="true" />;
}

export function HealthBadge({ health }: { health: Health }) {
  const config = HEALTH_CONFIG[health];
  return (
    <span className={`inline-flex items-center gap-1.5 text-sm font-medium ${config.text}`}>
      <HealthDot health={health} />
      {config.label}
    </span>
  );
}
