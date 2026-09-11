import Link from "next/link";
import type { NodeSummary } from "@/lib/types";
import { HealthBadge } from "./HealthBadge";
import { StatBar } from "./StatBar";
import { formatBytes, formatPercent, formatRelativeTime } from "@/lib/format";

const OS_ICON: Record<string, string> = { linux: "🐧", windows: "🪟", darwin: "🍎" };

export function NodeCard({ node }: { node: NodeSummary }) {
  const metric = node.latest_metric;
  const memPercent = metric && metric.memory_total_bytes > 0 ? (metric.memory_used_bytes / metric.memory_total_bytes) * 100 : 0;

  return (
    <Link
      href={`/nodes/${node.agent_id}`}
      className="block rounded-lg border border-line-border bg-surface p-4 shadow-sm transition-shadow hover:shadow-md"
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h3 className="truncate font-semibold text-ink-primary">{node.hostname || node.agent_id.slice(0, 12)}</h3>
          <p className="truncate text-xs text-ink-muted">
            {OS_ICON[node.os] ?? "🖥️"} {node.platform || node.os} · {node.arch} · {node.cpu_cores} núcleos
          </p>
        </div>
        <HealthBadge health={node.health} />
      </div>

      <div className="mt-3 space-y-1.5">
        {metric ? (
          <>
            <StatBar label="CPU" metric="cpu" percent={metric.cpu_usage_percent} valueLabel={formatPercent(metric.cpu_usage_percent)} />
            <StatBar label="RAM" metric="memory" percent={memPercent} valueLabel={formatBytes(metric.memory_used_bytes)} />
            <StatBar label="Disco" metric="disk" percent={metric.disk_usage_percent} valueLabel={formatPercent(metric.disk_usage_percent)} />
          </>
        ) : (
          <p className="text-xs text-ink-muted">Sin metricas todavia</p>
        )}
      </div>

      <p className="mt-3 text-xs text-ink-muted">
        {node.local_ip && <span className="tabular">{node.local_ip} · </span>}
        Visto {formatRelativeTime(node.last_seen_at)}
      </p>
    </Link>
  );
}
