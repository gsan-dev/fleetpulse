"use client";

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { api, ApiError } from "@/lib/api";
import type { NodeDetail } from "@/lib/types";
import { useLiveNode } from "@/hooks/useLiveNode";
import { HealthBadge } from "@/components/HealthBadge";
import { TimeSeriesChart } from "@/components/TimeSeriesChart";
import { ContainerTable } from "@/components/ContainerTable";
import { formatBytes, formatPercent, formatRelativeTime } from "@/lib/format";

export default function NodeDetailPage() {
  const params = useParams<{ id: string }>();
  const agentId = params.id;

  const [node, setNode] = useState<NodeDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const { history, containers, connected } = useLiveNode(agentId);

  useEffect(() => {
    let cancelled = false;
    api
      .getNode(agentId)
      .then((n) => !cancelled && setNode(n))
      .catch((err) => !cancelled && setError(err instanceof ApiError ? err.message : "error de conexión"));
    return () => {
      cancelled = true;
    };
  }, [agentId]);

  // La ultima muestra en memoria (SSE) manda sobre la del fetch inicial en
  // cuanto llega una: es lo que alimenta los indicadores de cabecera.
  const latest = history[history.length - 1];
  const memPercent = latest && latest.memory_total_bytes > 0 ? (latest.memory_used_bytes / latest.memory_total_bytes) * 100 : 0;

  if (error) {
    return (
      <div className="rounded-md border border-status-critical/30 bg-status-critical/5 px-3 py-2 text-sm text-status-critical">
        {error}
      </div>
    );
  }

  if (!node) {
    return <p className="text-sm text-ink-muted">Cargando nodo…</p>;
  }

  return (
    <div>
      <Link href="/" className="text-sm text-ink-muted hover:text-ink-primary">
        ← Flota
      </Link>

      <div className="mt-2 flex flex-wrap items-start justify-between gap-2">
        <div>
          <h1 className="text-xl font-semibold text-ink-primary">{node.hostname}</h1>
          <p className="text-sm text-ink-muted">
            {node.platform} {node.platform_version} · kernel {node.kernel_version} · {node.arch} · {node.cpu_cores} núcleos ·{" "}
            {formatBytes(node.memory_total_bytes)} RAM
          </p>
          <p className="text-xs text-ink-muted">
            {node.local_ip} {node.public_ip && node.public_ip !== node.local_ip ? `(pública: ${node.public_ip})` : ""} · agente{" "}
            {node.agent_version}
          </p>
        </div>
        <div className="text-right">
          <HealthBadge health={node.health} />
          <p className="mt-1 text-xs text-ink-muted">
            {connected ? "En vivo" : "Reconectando…"} · visto {formatRelativeTime(node.last_seen_at)}
          </p>
        </div>
      </div>

      <div className="mt-6 grid grid-cols-2 gap-4 sm:grid-cols-4">
        <StatTile label="CPU" value={latest ? formatPercent(latest.cpu_usage_percent) : "—"} />
        <StatTile label="RAM" value={latest ? `${formatBytes(latest.memory_used_bytes)} (${formatPercent(memPercent)})` : "—"} />
        <StatTile label="Disco" value={latest ? formatPercent(latest.disk_usage_percent) : "—"} />
        <StatTile
          label="Red"
          value={latest ? `↓${formatBytes(latest.rx_bytes_per_second)}/s ↑${formatBytes(latest.tx_bytes_per_second)}/s` : "—"}
        />
      </div>

      <div className="mt-6 grid grid-cols-1 gap-4 lg:grid-cols-2">
        <ChartPanel title="CPU">
          <TimeSeriesChart
            data={history}
            series={[{ key: "cpu_usage_percent", label: "CPU", color: "var(--series-1)" }]}
            valueFormatter={(v) => `${v.toFixed(0)}%`}
          />
        </ChartPanel>
        <ChartPanel title="Memoria">
          <TimeSeriesChart
            data={history}
            series={[{ key: "memory_used_bytes", label: "RAM usada", color: "var(--series-2)" }]}
            valueFormatter={(v) => formatBytes(v)}
          />
        </ChartPanel>
        <ChartPanel title="Disco (raíz)">
          <TimeSeriesChart
            data={history}
            series={[{ key: "disk_usage_percent", label: "Disco", color: "var(--series-3)" }]}
            valueFormatter={(v) => `${v.toFixed(0)}%`}
          />
        </ChartPanel>
        <ChartPanel title="Red">
          <TimeSeriesChart
            data={history}
            series={[
              { key: "rx_bytes_per_second", label: "Entrada", color: "var(--series-1)" },
              { key: "tx_bytes_per_second", label: "Salida", color: "var(--series-2)" },
            ]}
            valueFormatter={(v) => `${formatBytes(v)}/s`}
          />
        </ChartPanel>
      </div>

      <div className="mt-6">
        <h2 className="mb-2 text-sm font-semibold text-ink-primary">Contenedores</h2>
        <ContainerTable agentId={agentId} containers={containers} />
      </div>
    </div>
  );
}

function StatTile({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-line-border bg-surface p-3">
      <p className="text-xs text-ink-muted">{label}</p>
      <p className="tabular mt-1 text-lg font-semibold text-ink-primary">{value}</p>
    </div>
  );
}

function ChartPanel({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="rounded-lg border border-line-border bg-surface p-4">
      <h3 className="mb-2 text-sm font-medium text-ink-secondary">{title}</h3>
      {children}
    </div>
  );
}
