"use client";

import { useEffect, useState } from "react";
import type { Container } from "@/lib/types";
import { api, ApiError } from "@/lib/api";

export function LogsModal({
  agentId,
  container,
  onClose,
}: {
  agentId: string;
  container: Container;
  onClose: () => void;
}) {
  const [lines, setLines] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLines(null);
    setError(null);

    api
      .containerLogs(agentId, container.id, 300)
      .then((res) => !cancelled && setLines(res.lines))
      .catch((err) => {
        if (cancelled) return;
        // El agente pudo tardar (comando sincrono con timeout de 8s en el
        // servidor): se distingue de un error de aplicacion para que el
        // mensaje tenga sentido.
        if (err instanceof ApiError && err.status === 504) {
          setError("El agente no respondio a tiempo. Puede estar desconectado.");
        } else if (err instanceof ApiError && err.status === 409) {
          setError("El agente no tiene el canal de comandos abierto ahora mismo.");
        } else {
          setError(err instanceof ApiError ? err.message : "No se pudieron obtener los logs");
        }
      });

    return () => {
      cancelled = true;
    };
  }, [agentId, container.id]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      onClick={onClose}
      role="dialog"
      aria-modal="true"
    >
      <div
        className="flex max-h-[80vh] w-full max-w-3xl flex-col rounded-lg border border-line-border bg-surface shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-line-grid px-4 py-3">
          <h2 className="font-semibold text-ink-primary">Logs · {container.name}</h2>
          <button onClick={onClose} className="text-ink-muted hover:text-ink-primary" aria-label="Cerrar">
            ✕
          </button>
        </div>
        <div className="overflow-auto p-4">
          {error && <p className="text-sm text-status-critical">{error}</p>}
          {!error && lines === null && <p className="text-sm text-ink-muted">Pidiendo logs al agente…</p>}
          {!error && lines !== null && lines.length === 0 && (
            <p className="text-sm text-ink-muted">Este contenedor no tiene logs recientes.</p>
          )}
          {!error && lines !== null && lines.length > 0 && (
            <pre className="whitespace-pre-wrap break-all font-mono text-xs text-ink-secondary">{lines.join("\n")}</pre>
          )}
        </div>
      </div>
    </div>
  );
}
