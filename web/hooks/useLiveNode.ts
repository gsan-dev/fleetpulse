"use client";

import { useEffect, useRef, useState } from "react";
import { api, type Container, type MetricPoint } from "@/lib/api";

// Tope de puntos que se mantienen en memoria para la grafica en vivo. A un
// evento cada ~15s (la cadencia por defecto del agente), 240 puntos cubren
// una hora sin que el array crezca sin limite mientras la pestana esta abierta.
const MAX_HISTORY = 240;

interface LiveNodeState {
  history: MetricPoint[];
  containers: Container[];
  connected: boolean;
}

/**
 * useLiveNode combina un historico inicial (REST) con el stream SSE del
 * servidor: la grafica no arranca vacia y, en cuanto llega el primer evento
 * en vivo, sigue creciendo sin volver a pedir el rango completo.
 */
export function useLiveNode(agentId: string, since = "1h"): LiveNodeState {
  const [history, setHistory] = useState<MetricPoint[]>([]);
  const [containers, setContainers] = useState<Container[]>([]);
  const [connected, setConnected] = useState(false);
  const seeded = useRef(false);

  useEffect(() => {
    let cancelled = false;
    seeded.current = false;

    api
      .getMetrics(agentId, since)
      .then((points) => {
        if (!cancelled) setHistory(points.slice(-MAX_HISTORY));
      })
      .catch(() => {
        /* la grafica simplemente arranca vacia; el SSE la ira rellenando */
      })
      .finally(() => {
        seeded.current = true;
      });

    api
      .listContainers(agentId)
      .then((list) => {
        if (!cancelled) setContainers(list);
      })
      .catch(() => {});

    const source = new EventSource(api.streamUrl(agentId));
    source.onopen = () => !cancelled && setConnected(true);
    source.onerror = () => !cancelled && setConnected(false);
    source.onmessage = (event) => {
      if (cancelled) return;
      try {
        const payload = JSON.parse(event.data) as { metric: MetricPoint; containers: Container[] };
        setHistory((prev) => [...prev, payload.metric].slice(-MAX_HISTORY));
        setContainers(payload.containers);
      } catch {
        /* evento malformado: se ignora, el siguiente llegara en ~15s */
      }
    };

    return () => {
      cancelled = true;
      source.close();
    };
  }, [agentId, since]);

  return { history, containers, connected };
}
