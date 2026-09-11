"use client";

import { useFleet } from "@/hooks/useFleet";
import { NodeCard } from "@/components/NodeCard";

export default function FleetGridPage() {
  const { nodes, loading, error } = useFleet();

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-lg font-semibold text-ink-primary">Flota</h1>
        <p className="text-sm text-ink-muted">
          {nodes.length} {nodes.length === 1 ? "nodo" : "nodos"}
        </p>
      </div>

      {error && (
        <div className="mb-4 rounded-md border border-status-critical/30 bg-status-critical/5 px-3 py-2 text-sm text-status-critical">
          No se pudo conectar con el servidor: {error}
        </div>
      )}

      {loading && nodes.length === 0 && <p className="text-sm text-ink-muted">Cargando flota…</p>}

      {!loading && nodes.length === 0 && !error && (
        <div className="rounded-lg border border-dashed border-line-border p-8 text-center text-sm text-ink-muted">
          Todavía no se ha registrado ningún nodo. Instala el agente en una máquina y apúntalo a este servidor.
        </div>
      )}

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {nodes.map((node) => (
          <NodeCard key={node.agent_id} node={node} />
        ))}
      </div>
    </div>
  );
}
