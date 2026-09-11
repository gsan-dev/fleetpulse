"use client";

import { useEffect, useState } from "react";
import { api, ApiError, type NodeSummary } from "@/lib/api";

const POLL_INTERVAL_MS = 5000;

interface FleetState {
  nodes: NodeSummary[];
  loading: boolean;
  error: string | null;
}

/**
 * useFleet sondea la lista de nodos cada pocos segundos. Un sondeo simple
 * basta para la Fleet Grid (decenas de nodos, no miles) y evita abrir una
 * conexion SSE por fila; el detalle de un nodo si usa SSE (useLiveNode) para
 * las graficas en vivo, donde la latencia si importa.
 */
export function useFleet(): FleetState {
  const [nodes, setNodes] = useState<NodeSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function poll() {
      try {
        const data = await api.listNodes();
        if (!cancelled) {
          setNodes(data);
          setError(null);
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof ApiError ? err.message : "no se pudo conectar con el servidor");
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    }

    poll();
    const id = setInterval(poll, POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  return { nodes, loading, error };
}
