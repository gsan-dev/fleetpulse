# Documento de Especificación Técnica: FleetPulse

**Nombre del Proyecto:** FleetPulse (Distributed Infrastructure & Container Telemetry)
**Tipo de Sistema:** Plataforma Distribuida de Monitorización Agente-Servidor
**Perfil Profesional:** DevOps / Platform Engineering / Fullstack

## 1. Visión General y Justificación

FleetPulse es una plataforma self-hosted ligera diseñada para monitorizar la salud de flotas heterogéneas de servidores (Linux bare-metal, máquinas virtuales, Raspberry Pi) y orquestadores (Docker, Kubernetes).

A diferencia de soluciones pesadas como Prometheus/Grafana o Datadog, FleetPulse utiliza un agente binario ultraligero que se instala mediante una línea de comandos, registra automáticamente el nodo en un panel central mediante gRPC y transmite telemetría en tiempo real con un consumo ínfimo de recursos.

## 2. Arquitectura del Sistema

```
┌─────────────────────────────────────────────────────────┐
│                    NODO CLIENTE (xN)                    │
│                                                         │
│  [ OS Metrics ]      [ Docker API ]     [ K8s API ]     │
│        │                   │                 │          │
│        └─────────┬─────────┴─────────────────┘          │
│                  ▼                                      │
│         [ FleetPulse Agent ] (Go Daemon)                │
└──────────────────┬──────────────────────────────────────┘
                   │
                   │ gRPC / Protobuf (mTLS / Agent Token)
                   ▼
┌─────────────────────────────────────────────────────────┐
│                    SERVIDOR CENTRAL                     │
│                                                         │
│        [ FleetPulse Collector ] (Go / Fastify)          │
│                  │                                      │
│                  ├──► [ TimescaleDB / VictoriaMetrics ] │
│                  └──► [ Redis (Heartbeat & Cache) ]     │
│                                                         │
│        [ FleetPulse Web Dashboard ] (Next.js)           │
└─────────────────────────────────────────────────────────┘
```

## 3. Especificación del Stack Tecnológico

| Capa | Tecnología | Función |
|---|---|---|
| Agente (Cliente) | Go (Golang) | Binario estático sin dependencias, consumo de RAM <15MB. |
| Transporte | gRPC / Protocol Buffers | Comunicación binaria comprimida, baja latencia y tipado estricto. |
| Servidor Recolector | Go o Node.js (Fastify) | Procesamiento concurrente de ingesta de métricas. |
| Base de Datos | TimescaleDB (PostgreSQL) | Almacenamiento optimizado de series temporales. |
| Caché / Estado | Redis | Control de estados online/offline y colas de alertas. |
| Dashboard (UI) | Next.js (React), TailwindCSS, Recharts | Interfaz web responsiva con streaming de datos en vivo. |
| Despliegue | Docker, Systemd, Helm Charts | Distribución del agente y servidor central. |

## 4. Funcionalidades Detalladas

### Agente (FleetPulse Agent)

- **Auto-Discovery:** Al arrancar por primera vez con la variable `AGENT_TOKEN` y la URL del servidor, se registra automáticamente enviando su hostname, arquitectura, IP local/pública y sistema operativo.
- **Colector de Sistema Operativo:** Extrae frecuencia/uso de CPU, saturación de RAM, I/O de discos montados y tráfico de red entrante/saliente.
- **Inspector de Contenedores (Docker & K8s):**
  - Docker: Conexión a `/var/run/docker.sock` para listar contenedores, estados (running, paused, restarting), métricas individuales de CPU/RAM y cola de logs tail.
  - Kubernetes: Modo DaemonSet que consulta la API de Kubelet para medir recursos por Pod y Node.
- **mTLS & Autenticación:** Cifrado punto a punto en las transmisiones gRPC mediante token de rotación.

### Servidor Central y Dashboard

- **Vista de Flota (Fleet Grid):** Panel estilo "Matriz de Nodos" que refleja el estado de salud de todos los agentes registrados mediante códigos de color (Verde = Saludable, Amarillo = Alerta de Recursos, Rojo = Desconectado).
- **Inspector Individual de Nodo:** Gráficos temporales interactivos de uso de recursos, lista de contenedores en ejecución con posibilidad de enviar señales de reinicio y visor de logs.
- **Control de Desconexión (Heartbeat Watchdog):** Si un agente no emite una ráfaga de métricas en 45 segundos, se marca como Unreachable y se dispara un evento de alerta.
- **Canales de Alerta:** Integración nativa con Webhooks de Telegram y Discord.

## 5. Esquema de Datos Principal (Protobuf)

```protobuf
syntax = "proto3";

package fleetpulse;

message MetricPayload {
  string agent_id = 1;
  int64 timestamp = 2;
  SystemMetrics system = 3;
  repeated ContainerMetrics containers = 4;
}

message SystemMetrics {
  float cpu_usage_percent = 1;
  uint64 memory_used_bytes = 2;
  uint64 memory_total_bytes = 3;
  float disk_usage_percent = 4;
}

message ContainerMetrics {
  string id = 1;
  string name = 2;
  string image = 3;
  string status = 4;
  float cpu_percent = 5;
  uint64 memory_bytes = 6;
}
```

## 6. Roadmap de Desarrollo por Fases

### Fase 1: Núcleo del Agente en Go (Semanas 1-2)
- [x] Creación del proyecto Go y conexión con librerías del sistema (gopsutil).
- [x] Implementación del cliente de Docker SDK para leer `/var/run/docker.sock`.
- [x] Definición del archivo `.proto` para los mensajes de métricas y compilación con `protoc`.

### Fase 2: Servidor Recolector y Persistencia (Semanas 3-4)
- [x] Desarrollo del servidor gRPC de ingesta.
- [x] Configuración del contenedor TimescaleDB y esquemas de tablas para métricas (`internal/store/pgstore`; no probado contra una instancia real en este entorno, ver README).
- [x] Lógica de registro de nodos (Auto-Discovery) e identificación por Token.

### Fase 3: Dashboard Web en Tiempo Real (Semanas 5-6)
- [x] Inicialización del proyecto Next.js con TailwindCSS.
- [x] Creación de componentes gráficos de rendimiento (Recharts).
- [x] Implementación de Server-Sent Events (SSE) para conectar la base de datos con la interfaz de usuario.

### Fase 4: Alertas y Orquestación K8s (Semanas 7-8)
- [x] Creación del servicio de alertas por Telegram (y Discord) ante fallos de respuesta.
- [x] Adaptación del agente para funcionar como DaemonSet en Kubernetes leyendo la API de Kubelet (`internal/collector/kubelet.go`; no probado contra un cluster real, ver README).

### Fase 5: Empaquetado, CI/CD y Publicación (Semanas 9-10)
- [x] Creación del script de instalación de una sola línea (`install.sh`) que configura el servicio systemd en Linux, más `install.ps1` equivalente para Windows (servicio nativo).
- [x] Automatización de builds del binario del agente para arquitecturas amd64 y arm64 (Linux) y amd64 (Windows) mediante GitHub Actions.
- [x] Redacción de la documentación completa del repositorio y despliegue de la versión de demostración (`docker-compose.yml`).

## 7. Estrategia de Instalación (User Experience)

Para garantizar un impacto profesional en el portfolio, el agente debe poder instalarse en cualquier máquina remota ejecutando un comando único:

```bash
curl -sSL https://fleetpulse.tu-dominio.com/install.sh | sudo bash -s -- --token=TU_TOKEN_SECRETO --server=fleetpulse.tu-dominio.com:50051
```

El script `install.sh` se encargará de:
- Detectar la arquitectura de la máquina (x86_64, aarch64).
- Descargar el binario compilado adecuado desde las Releases de GitHub.
- Crear el servicio de sistema `/etc/systemd/system/fleetpulse-agent.service`.
- Iniciar y habilitar el servicio automáticamente en el sistema operativo host.

## 8. Elementos de Diferenciación para Entrevistas

Para maximizar el valor de este proyecto frente a reclutadores o líderes técnicos, asegúrate de documentar:

- **Benchmark de Rendimiento:** Incluir una tabla que demuestre el consumo del agente (ej. "Consumo de CPU <0.5%, Memoria RAM constante en 12MB").
- **Seguridad Integrada:** Explicar cómo el sistema valida el origen de los agentes para evitar inyección de métricas falsas.
- **Mapeo de Arquitectura:** Incluir un diagrama explicativo en el README.md del repositorio hecho con Excalidraw o Mermaid.js.