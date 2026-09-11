// Barra fina con el mismo espaciador de 2px que separa la barra del fondo
// que usan las graficas (marks-and-anatomy.md), para que la lectura visual
// sea consistente entre la Fleet Grid y las graficas de detalle.
const SERIES_COLOR: Record<"cpu" | "memory" | "disk", string> = {
  cpu: "var(--series-1)",
  memory: "var(--series-2)",
  disk: "var(--series-3)",
};

export function StatBar({
  label,
  percent,
  metric,
  valueLabel,
}: {
  label: string;
  percent: number;
  metric: "cpu" | "memory" | "disk";
  valueLabel: string;
}) {
  const clamped = Math.max(0, Math.min(100, percent));
  return (
    <div className="flex items-center gap-2 text-xs">
      <span className="w-14 shrink-0 text-ink-muted">{label}</span>
      <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-line-grid" role="progressbar" aria-valuenow={Math.round(clamped)} aria-valuemin={0} aria-valuemax={100} aria-label={`${label}: ${valueLabel}`}>
        <div
          className="h-full rounded-full transition-[width]"
          style={{ width: `${clamped}%`, backgroundColor: SERIES_COLOR[metric] }}
        />
      </div>
      <span className="tabular w-14 shrink-0 text-right text-ink-secondary">{valueLabel}</span>
    </div>
  );
}
