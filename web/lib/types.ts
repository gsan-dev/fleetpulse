// Estos tipos reflejan uno a uno los DTO de internal/api/dto.go en el
// servidor. Mantenerlos a mano (en vez de generarlos) es aceptable aqui: son
// pocos campos y el contrato es estable; si crecen, valdria la pena generar
// este fichero desde el .proto o desde un esquema OpenAPI.

export type Health = "healthy" | "warning" | "unreachable" | "unknown";

export interface MetricPoint {
  timestamp: string; // ISO 8601
  cpu_usage_percent: number;
  memory_used_bytes: number;
  memory_total_bytes: number;
  disk_usage_percent: number;
  rx_bytes_per_second: number;
  tx_bytes_per_second: number;
  load1: number;
}

export interface NodeSummary {
  agent_id: string;
  hostname: string;
  os: string;
  platform: string;
  arch: string;
  local_ip: string;
  public_ip: string;
  agent_version: string;
  cpu_cores: number;
  memory_total_bytes: number;
  registered_at: string;
  last_seen_at: string;
  health: Health;
  latest_metric?: MetricPoint;
}

export interface NodeDetail extends NodeSummary {
  kernel_version: string;
  platform_version: string;
}

export interface Container {
  id: string;
  name: string;
  image: string;
  status: string;
  state: string;
  cpu_percent: number;
  memory_bytes: number;
  memory_limit_bytes: number;
  started_at: string;
  restart_count: number;
  rx_bytes: number;
  tx_bytes: number;
}

export interface LiveEvent {
  metric: MetricPoint;
  containers: Container[];
}
