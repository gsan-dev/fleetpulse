"use client";

import { useState } from "react";
import type { Container } from "@/lib/types";
import { api, ApiError } from "@/lib/api";
import { formatBytes, formatDuration, formatPercent } from "@/lib/format";
import { LogsModal } from "./LogsModal";

const STATE_COLOR: Record<string, string> = {
  running: "text-status-good",
  restarting: "text-status-warning",
  paused: "text-ink-muted",
  exited: "text-status-critical",
  dead: "text-status-critical",
};

export function ContainerTable({ agentId, containers }: { agentId: string; containers: Container[] }) {
  const [restarting, setRestarting] = useState<string | null>(null);
  const [logsFor, setLogsFor] = useState<Container | null>(null);
  const [toast, setToast] = useState<string | null>(null);

  async function handleRestart(container: Container) {
    setRestarting(container.id);
    try {
      await api.restartContainer(agentId, container.id);
      setToast(`Reinicio solicitado para ${container.name}`);
    } catch (err) {
      setToast(err instanceof ApiError ? `Error: ${err.message}` : "No se pudo enviar el comando");
    } finally {
      setRestarting(null);
      setTimeout(() => setToast(null), 4000);
    }
  }

  if (containers.length === 0) {
    return <p className="text-sm text-ink-muted">Este nodo no tiene contenedores (o Docker esta desactivado).</p>;
  }

  return (
    <div>
      {toast && (
        <div className="mb-2 rounded-md border border-line-border bg-surface px-3 py-2 text-sm text-ink-secondary">
          {toast}
        </div>
      )}
      <div className="overflow-x-auto rounded-lg border border-line-border">
        <table className="w-full text-left text-sm">
          <thead className="bg-plane text-xs uppercase tracking-wide text-ink-muted">
            <tr>
              <th className="px-3 py-2 font-medium">Nombre</th>
              <th className="px-3 py-2 font-medium">Imagen</th>
              <th className="px-3 py-2 font-medium">Estado</th>
              <th className="px-3 py-2 font-medium">CPU</th>
              <th className="px-3 py-2 font-medium">RAM</th>
              <th className="px-3 py-2 font-medium">Arranque</th>
              <th className="px-3 py-2 font-medium">Reinicios</th>
              <th className="px-3 py-2 font-medium" />
            </tr>
          </thead>
          <tbody>
            {containers.map((c) => (
              <tr key={c.id} className="border-t border-line-grid">
                <td className="px-3 py-2 font-medium text-ink-primary">{c.name}</td>
                <td className="max-w-[16rem] truncate px-3 py-2 text-ink-secondary" title={c.image}>
                  {c.image}
                </td>
                <td className={`px-3 py-2 font-medium ${STATE_COLOR[c.state] ?? "text-ink-secondary"}`}>{c.status}</td>
                <td className="tabular px-3 py-2 text-ink-secondary">{formatPercent(c.cpu_percent)}</td>
                <td className="tabular px-3 py-2 text-ink-secondary">
                  {formatBytes(c.memory_bytes)}
                  {c.memory_limit_bytes > 0 && <span className="text-ink-muted"> / {formatBytes(c.memory_limit_bytes)}</span>}
                </td>
                <td className="tabular px-3 py-2 text-ink-secondary">
                  {c.started_at ? formatDuration((Date.now() - new Date(c.started_at).getTime()) / 1000) : "—"}
                </td>
                <td className="tabular px-3 py-2 text-ink-secondary">{c.restart_count}</td>
                <td className="px-3 py-2">
                  <div className="flex justify-end gap-2">
                    <button
                      onClick={() => setLogsFor(c)}
                      className="rounded border border-line-border px-2 py-1 text-xs text-ink-secondary hover:bg-plane"
                    >
                      Logs
                    </button>
                    <button
                      onClick={() => handleRestart(c)}
                      disabled={restarting === c.id}
                      className="rounded border border-line-border px-2 py-1 text-xs text-ink-secondary hover:bg-plane disabled:opacity-50"
                    >
                      {restarting === c.id ? "Enviando…" : "Reiniciar"}
                    </button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {logsFor && (
        <LogsModal agentId={agentId} container={logsFor} onClose={() => setLogsFor(null)} />
      )}
    </div>
  );
}
