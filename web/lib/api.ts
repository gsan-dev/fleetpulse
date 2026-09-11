import type { Container, LiveEvent, MetricPoint, NodeDetail, NodeSummary } from "./types";

// NEXT_PUBLIC_* queda embebido en el bundle del navegador: el dashboard habla
// directamente con la API Go (no hay proxy de Next.js de por medio), asi que
// tanto la URL como el token del dashboard son visibles para quien abra las
// herramientas de desarrollo. Es el modelo de amenaza correcto para una
// herramienta self-hosted en una LAN de confianza; si se expone a internet,
// el token deja de ser secreto de verdad y hace falta algo mas (proxy con
// su propia autenticacion, red privada, etc. - ver README).
const API_URL = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";
const API_TOKEN = process.env.NEXT_PUBLIC_API_TOKEN ?? "";

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

function withToken(path: string): string {
  if (!API_TOKEN) return `${API_URL}${path}`;
  const separator = path.includes("?") ? "&" : "?";
  return `${API_URL}${path}${separator}token=${encodeURIComponent(API_TOKEN)}`;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if (API_TOKEN) headers.set("Authorization", `Bearer ${API_TOKEN}`);

  const res = await fetch(`${API_URL}${path}`, { ...init, headers, cache: "no-store" });
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new ApiError(res.status, body.error ?? res.statusText);
  }
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

export const api = {
  listNodes: () => request<NodeSummary[]>("/api/nodes"),
  getNode: (agentId: string) => request<NodeDetail>(`/api/nodes/${agentId}`),
  getMetrics: (agentId: string, since = "1h") =>
    request<MetricPoint[]>(`/api/nodes/${agentId}/metrics?since=${since}`),
  listContainers: (agentId: string) => request<Container[]>(`/api/nodes/${agentId}/containers`),
  restartContainer: (agentId: string, containerId: string) =>
    request<{ status: string; command_id: string }>(
      `/api/nodes/${agentId}/containers/${containerId}/restart`,
      { method: "POST" },
    ),
  containerLogs: (agentId: string, containerId: string, tail = 200) =>
    request<{ lines: string[] }>(
      `/api/nodes/${agentId}/containers/${containerId}/logs?tail=${tail}`,
    ),
  // streamUrl no pasa por `request`: EventSource abre la conexion el mismo,
  // solo necesita la URL final (con el token ya incrustado como query, unica
  // forma de autenticar un EventSource sin cabeceras personalizadas).
  streamUrl: (agentId: string) => withToken(`/api/nodes/${agentId}/stream`),
};

export type { Container, LiveEvent, MetricPoint, NodeDetail, NodeSummary };
