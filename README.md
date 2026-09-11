# FleetPulse

Plataforma self-hosted de monitorización agente-servidor para flotas heterogéneas
(Linux, Windows, Raspberry Pi) y orquestadores (Docker, Kubernetes).

Un **servidor central** (recolector + dashboard) recibe telemetría de **agentes**
que instalas en cada máquina que quieras vigilar. Un agente por máquina, un
servidor para toda la flota — el agente funciona igual en Linux, Windows,
macOS, dentro de un contenedor Docker o como Pod de Kubernetes.

La especificación original está en [config/system.md](config/system.md). Este
documento es la referencia de cómo está construido y cómo ponerlo en marcha.

## Arquitectura

```mermaid
flowchart LR
    subgraph Nodo["Nodo (Linux / Windows / macOS / K8s Pod)"]
        OS["gopsutil<br/>(CPU, RAM, disco, red)"]
        RT["Docker SDK / API de Kubelet"]
        AG["fleetpulse-agent"]
        OS --> AG
        RT --> AG
    end

    AG -- "gRPC + mTLS o token<br/>(Register, PushMetrics,<br/>StreamCommands)" --> SRV

    subgraph Servidor["Servidor central"]
        SRV["fleetpulse-server"]
        SRV --> PG["TimescaleDB / PostgreSQL<br/>(nodos, métricas, contenedores)"]
        SRV --> RD["Redis<br/>(heartbeat compartido, opcional)"]
        SRV --> WH["Telegram / Discord<br/>(webhooks de alerta)"]
    end

    SRV -- "REST + SSE" --> WEB["Dashboard Next.js"]
```

Cada agente abre **tres** canales gRPC de larga duración hacia el servidor:
`PushMetrics` (telemetría, actúa también como heartbeat), `StreamCommands`
(el servidor empuja reinicios/logs sin que el agente exponga ningún puerto
entrante) y llamadas puntuales a `Register`/`ReportCommandResult`. El
dashboard nunca habla con los agentes directamente: todo pasa por la API
HTTP/SSE del servidor. El agente solo necesita salida hacia el servidor
(puerto 50051 por defecto); no hace falta abrir nada entrante en los nodos
que monitorizas, aunque estén detrás de NAT o un firewall.

---

## 1. Levantar el servidor

Necesitas **uno** (y solo uno) en toda tu flota: es el punto central al que
todos los agentes reportan. Elige una opción.

### Opción A — Docker Compose (recomendado)

Requiere Docker. Levanta TimescaleDB, Redis, el servidor y el dashboard con
un único comando:

```bash
git clone https://github.com/<tu-usuario>/fleetpulse.git
cd fleetpulse
docker compose up -d --build
```

Dashboard en **http://localhost:3000**. Token de agente de prueba: `demo-token`
(cámbialo antes de exponer esto a nada que no sea tu propia LAN — ver
`AGENT_TOKENS` en `docker-compose.yml`).

Si algún puerto (8080, 3000, 5432, 6379) ya está en uso en tu máquina,
remapea el lado izquierdo en `docker-compose.yml` (p. ej. `"8090:8080"`) y
ajusta `NEXT_PUBLIC_API_URL` del servicio `web` para que apunte al puerto
que elijas.

### Opción B — Binario nativo (sin Docker)

Requisitos: Go 1.26+, y PostgreSQL/TimescaleDB + Redis accesibles (o arranca
en modo memoria para probar, ver más abajo).

```bash
make build-server   # o: go build -o bin/fleetpulse-server ./cmd/fleetpulse-server
./bin/fleetpulse-server \
  --database-url=postgres://usuario:pass@localhost:5432/fleetpulse \
  --redis-url=redis://localhost:6379/0 \
  --tokens=TU_TOKEN_SECRETO
```

Para probar sin levantar ninguna base de datos (todo en memoria del proceso,
se pierde al reiniciar el servidor — solo para pruebas, nunca en producción):

```bash
./bin/fleetpulse-server --storage=memory --tokens=dev-token
```

El dashboard es una app Next.js aparte; con el servidor ya escuchando en
`--http-addr` (por defecto `:8080`):

```bash
cd web
cp .env.example .env.local   # ajusta NEXT_PUBLIC_API_URL si no es localhost:8080
npm install
npm run build && npm start   # o `npm run dev` durante desarrollo
```

Todas las variables de configuración del servidor están en la
[tabla de más abajo](#configuración-del-servidor).

---

## 2. Levantar el agente — en cualquier máquina, en cualquier momento

Instala uno por cada máquina que quieras ver en el panel. Necesitas de
antemano la **dirección del servidor** (`host:puerto`, por defecto puerto
`50051`) y el **token** que configuraste en `AGENT_TOKENS`.

### Linux (systemd) — sin Docker

```bash
curl -sSL https://raw.githubusercontent.com/<tu-usuario>/fleetpulse/main/install/install.sh | sudo bash -s -- \
  --token=TU_TOKEN_SECRETO --server=IP_DEL_SERVIDOR:50051
```

Instala el binario, crea el servicio `fleetpulse-agent.service` y lo arranca
con `systemctl enable --now`. Ver estado y logs:

```bash
systemctl status fleetpulse-agent
journalctl -u fleetpulse-agent -f
```

### Windows (servicio nativo) — PowerShell como Administrador

```powershell
irm https://raw.githubusercontent.com/<tu-usuario>/fleetpulse/main/install/install.ps1 -OutFile install.ps1
.\install.ps1 -Token TU_TOKEN_SECRETO -Server IP_DEL_SERVIDOR:50051
```

Registra el servicio `FleetPulseAgent` en el Service Control Manager
(arranque automático, sobrevive a reinicios). El token se guarda en el
registro del propio servicio, no como argumento visible. Comprobar estado:

```powershell
Get-Service FleetPulseAgent
```

### Docker (cualquier SO con Docker: Linux, Windows, macOS)

```bash
git clone https://github.com/<tu-usuario>/fleetpulse.git && cd fleetpulse
docker build -f cmd/fleetpulse-agent/Dockerfile -t fleetpulse-agent .
docker run -d --name fleetpulse-agent --restart unless-stopped \
  -e FLEETPULSE_SERVER=IP_DEL_SERVIDOR:50051 \
  -e AGENT_TOKEN=TU_TOKEN_SECRETO \
  -e FLEETPULSE_HOSTNAME=$(hostname) \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v fleetpulse-agent-state:/var/lib/fleetpulse \
  fleetpulse-agent
```

`FLEETPULSE_HOSTNAME` es importante: sin él, el panel mostraría el ID del
contenedor (algo como `6931b28a9112`) en vez del nombre real de la máquina,
porque así es como Docker le asigna el hostname por defecto a un contenedor.
El montaje de `docker.sock` es opcional — solo hace falta si quieres que el
agente también reporte los contenedores de esa máquina.

### macOS / cualquier sistema con Go — manual

No hay instalador nativo (tipo `launchd`) todavía, pero el binario compila y
funciona igual que en el resto de plataformas:

```bash
git clone https://github.com/<tu-usuario>/fleetpulse.git && cd fleetpulse
go build -o fleetpulse-agent ./cmd/fleetpulse-agent
./fleetpulse-agent --server=IP_DEL_SERVIDOR:50051 --token=TU_TOKEN_SECRETO
```

Para que sobreviva a un reinicio necesitarás gestionarlo tú (un `.plist` de
`launchd`, `screen`/`tmux`, o cualquier supervisor de procesos que ya uses).

### Kubernetes (DaemonSet — un agente por nodo del clúster)

```bash
kubectl create secret generic fleetpulse-token --from-literal=token=TU_TOKEN_SECRETO
helm install fleetpulse-agent deploy/helm/fleetpulse-agent \
  --set server.address=IP_DEL_SERVIDOR:50051 \
  --set server.tokenSecretName=fleetpulse-token
```

El agente detecta automáticamente que corre dentro de un Pod
(variable `KUBERNETES_SERVICE_HOST`, que Kubernetes inyecta siempre) y usa la
API del kubelet en vez de Docker para leer los contenedores del nodo.

### Probar sin servidor (depuración local)

Una sola muestra por stdout, sin necesidad de servidor ni token — útil para
comprobar que el binario detecta bien el sistema y (si lo tiene) Docker:

```bash
./fleetpulse-agent --once --log-level debug
```

---

## Estado del roadmap

| Fase | Contenido | Estado |
|---|---|---|
| 1 | Núcleo del agente en Go (gopsutil, Docker SDK, protobuf) | ✅ |
| 2 | Servidor recolector gRPC + persistencia (TimescaleDB, Redis) | ✅ |
| 3 | Dashboard Next.js en tiempo real | ✅ |
| 4 | Alertas + modo DaemonSet de Kubernetes | ✅ |
| 5 | Empaquetado, CI/CD y documentación | ✅ |

Ver [Estado de verificación](#estado-de-verificación) para lo que se ha
probado contra infraestructura real y lo que sigue pendiente.

## Estructura del repositorio

```
cmd/fleetpulse-agent/     Binario del agente (demonio / servicio)
cmd/fleetpulse-server/    Binario del recolector central
internal/collector/       Métricas de sistema (gopsutil), Docker SDK y API de Kubelet
internal/config/          Configuración del agente (flags + env)
internal/identity/        Huella del nodo y agent_id persistente
internal/telemetry/       Dominio del agente -> protobuf
internal/transport/       Cliente gRPC del agente (Register, PushMetrics, TLS/mTLS)
internal/commands/        Ejecutor de comandos del agente (reinicio, logs bajo demanda)
internal/serverconfig/    Configuración del servidor (flags + env)
internal/store/           Interfaz de persistencia + memstore (demo/tests) + pgstore (Postgres/Timescale)
internal/heartbeat/       Watchdog de conectividad (memoria o Redis)
internal/alert/           Canales de alerta (Telegram, Discord)
internal/grpcserver/      Implementación del servicio gRPC (lado servidor)
internal/rpcauth/         Autenticación por token compartida entre agente y servidor
internal/commandbus/      Enrutado de comandos servidor -> agente
internal/hub/             Difusión en vivo (SSE) de métricas
internal/api/             API REST + SSE para el dashboard
proto/fleetpulse/v1/      Contrato gRPC/protobuf · gen/ es el código generado (no editar a mano)
web/                      Dashboard Next.js (App Router, TypeScript, Tailwind, Recharts)
deploy/helm/              Chart de Kubernetes (DaemonSet del agente)
install/                  Instaladores de una línea (Linux systemd, Windows servicio nativo)
.github/workflows/        CI (test/build/lint) y Release (binarios + imágenes)
docker-compose.yml        Stack completo de demostración local
```

## Desarrollo / compilar desde el código

Requisitos: Go 1.26+, Node.js 22+. Para regenerar el protobuf hace falta
`buf` y sus plugins (`make tools`).

```bash
make tools     # solo la primera vez
make proto     # genera gen/ a partir de proto/
make tidy
make test      # go test ./... -race (sin -race si no hay compilador de C)
make build build-server
make web-install web-build
```

`make build-linux` / `make build-windows` cross-compilan el agente para
release sin necesidad de esas plataformas.

## Configuración del agente

| Flag | Variable | Por defecto | Descripción |
|---|---|---|---|
| `--server` | `FLEETPULSE_SERVER` | — | `host:puerto` gRPC del recolector |
| `--token` | `AGENT_TOKEN` | — | Token de alta del agente |
| `--interval` | `FLEETPULSE_INTERVAL` | `15s` | Cadencia de envío (el servidor puede imponer otra al registrar) |
| `--docker` | `FLEETPULSE_DOCKER` | `auto` | `auto`, `on` u `off` |
| `--runtime` | `FLEETPULSE_RUNTIME` | `auto` | `auto`, `docker` o `kubernetes` |
| `--state-dir` | `FLEETPULSE_STATE_DIR` | `/var/lib/fleetpulse` (root en Linux) | Dónde se persiste el `agent_id` |
| `--tls-ca` | `FLEETPULSE_TLS_CA` | — | CA para verificar el servidor (activa TLS) |
| `--tls-cert` / `--tls-key` | `FLEETPULSE_TLS_CERT` / `FLEETPULSE_TLS_KEY` | — | Certificado de cliente (mTLS) |
| `--log-level` | `FLEETPULSE_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `--hostname` | `FLEETPULSE_HOSTNAME` | — | Nombre del nodo a reportar; sustituye al autodetectado |
| `--once` | — | `false` | Una muestra por stdout y salir |

> **Agente en Docker**: fija siempre `FLEETPULSE_HOSTNAME` (ver
> [sección anterior](#docker-cualquier-so-con-docker-linux-windows-macos)) —
> si no, el panel muestra el ID del contenedor en vez del nombre real de la
> máquina.

`--server` y `--token` son obligatorios salvo en modo `--once`.
`fleetpulse-agent service install|uninstall|start|stop` gestiona el
servicio nativo en Windows.

## Configuración del servidor

| Flag | Variable | Por defecto | Descripción |
|---|---|---|---|
| `--grpc-addr` | `FLEETPULSE_GRPC_ADDR` | `:50051` | Escucha gRPC (agentes) |
| `--http-addr` | `FLEETPULSE_HTTP_ADDR` | `:8080` | Escucha HTTP/SSE (dashboard) |
| `--storage` | `FLEETPULSE_STORAGE` | `postgres` | `postgres` o `memory` (demo, no persiste) |
| `--database-url` | `DATABASE_URL` | — | DSN de PostgreSQL/TimescaleDB |
| `--redis-url` | `REDIS_URL` | — | Backend de heartbeat compartido entre réplicas (opcional; por defecto en memoria del proceso) |
| `--tokens` | `AGENT_TOKENS` | — | Tokens de agente válidos, separados por comas |
| `--heartbeat-timeout` | `FLEETPULSE_HEARTBEAT_TIMEOUT` | `45s` | Tiempo sin métricas antes de marcar `Unreachable` |
| `--tls-cert` / `--tls-key` | `FLEETPULSE_TLS_CERT` / `FLEETPULSE_TLS_KEY` | — | TLS del listener gRPC |
| `--tls-client-ca` | `FLEETPULSE_TLS_CLIENT_CA` | — | Exige mTLS a los agentes |
| `--dashboard-token` | `FLEETPULSE_DASHBOARD_TOKEN` | — | Protege la API HTTP (vacío = sin auth, LAN de confianza) |
| — | `FLEETPULSE_TELEGRAM_BOT_TOKEN` / `FLEETPULSE_TELEGRAM_CHAT_ID` | — | Alertas por Telegram |
| — | `FLEETPULSE_DISCORD_WEBHOOK_URL` | — | Alertas por Discord |

## Dashboard

Variables en `web/.env.local` (ver `web/.env.example`):

| Variable | Descripción |
|---|---|
| `NEXT_PUBLIC_API_URL` | URL del servidor (`http://localhost:8080` en desarrollo) |
| `NEXT_PUBLIC_API_TOKEN` | Solo si el servidor exige `--dashboard-token` |

El dashboard habla **directamente** con la API Go desde el navegador (no hay
proxy de Next.js de por medio), así que `NEXT_PUBLIC_*` queda visible en el
bundle — modelo de amenaza correcto para una LAN de confianza; si se expone a
internet hace falta algo más (proxy con su propia autenticación, red privada).

## Decisiones de diseño

- **El `agent_id` lo genera el cliente** y se persiste en el state dir. Un
  reintento de registro tras perderse la respuesta del servidor no duplica el
  nodo en el panel.
- **La IP pública la deriva el servidor** del peer gRPC en vez de aceptarla
  del agente: un campo enviado por el cliente puede falsearse.
- **Autenticación por token en cada llamada gRPC** (metadatos, interceptor
  compartido en `internal/rpcauth`), no solo en `Register`: cubre también el
  stream de métricas y el canal de comandos con el mismo mecanismo.
- **El heartbeat vive aparte del histórico**: memoria del proceso por
  defecto, Redis opcional para varias réplicas del servidor. Mismo patrón
  `auto/on/off` que ya usa el agente para Docker, por consistencia.
- **Los comandos servidor→agente van por un stream que abre el agente**
  (`StreamCommands`), nunca al revés: el agente no necesita exponer ningún
  puerto entrante, clave para nodos detrás de NAT/firewall.
- **Kubelet y Docker comparten la misma interfaz** (`collector.ContainerSource`)
  en el agente: el colector, el estado de salud y el payload de métricas no
  saben ni les importa qué runtime hay debajo.
- **La memoria de contenedor descuenta la cache de página** (`inactive_file`
  en cgroup v2, `cache` en v1), igual que `docker stats`.

## Estado de verificación

Lo que se ha probado de verdad, no solo "compila":

- ✅ **Agente y servidor compilan y pasan sus tests** en Windows (nativo) y
  Linux (cross-compile amd64/arm64).
- ✅ **Desplegado en un escenario real de dos máquinas en red**: un servidor
  completo (Docker Compose: TimescaleDB + Redis + servidor + dashboard) y un
  segundo agente en otra máquina distinta reportando por la red — registro,
  heartbeat, streaming de métricas, listado de *todos* los contenedores
  Docker del host, comando de reinicio de contenedor (dispatch → ejecución →
  resultado reportado de vuelta) y el override de hostname, todo verificado
  en vivo contra Postgres/TimescaleDB y Redis reales (no solo en memoria).
- ✅ **Dashboard**: build de producción de Next.js (TypeScript estricto sin
  errores) sirviendo el panel en tiempo real vía SSE contra el servidor real.
- ⚠️ **El inspector de Kubelet no se ha probado contra un clúster real**: la
  lógica de mapeo (estados, CPU/memoria) tiene tests unitarios con JSON
  sintético, pero falta validar contra un kubelet de verdad y ajustar el RBAC
  si el clúster tiene políticas más estrictas que las del chart de Helm.
- ⚠️ **TLS/mTLS**: el código (servidor y cliente) está implementado y cubre
  certificados de servidor y de cliente, pero no se ha generado un par de
  certificados de prueba end-to-end — documentar el flujo de emisión (o
  integrarlo con cert-manager en K8s) queda pendiente.
- ⚠️ Las etiquetas (`labels`) de los contenedores Docker no se persisten
  todavía en `pgstore`/`memstore` (se leen del engine pero se descartan al
  guardar el snapshot); ampliarlo es sencillo si hace falta filtrar por ellas.
- ⚠️ No hay instalador nativo para macOS (`launchd`); el binario compila y
  corre igual, pero gestionar su ciclo de vida queda por tu cuenta.
- ⚠️ CI (`.github/workflows/`) sigue patrones estándar de GitHub Actions,
  pero conviene revisar su primera ejecución real al publicar el repositorio.

## Diferenciadores para la entrevista/portfolio

- **Seguridad por capas**: token compartido validado por interceptor en cada
  RPC, mTLS opcional, IP pública derivada del transporte (no del payload),
  comandos siempre iniciados por el agente.
- **Cross-platform de verdad**: mismo binario de agente para Linux, Windows,
  macOS y Kubernetes, con instaladores nativos para cada uno (`systemd`,
  servicio Windows vía SCM, DaemonSet + Helm).
- **Degradación explícita**: `auto/on/off` repetido en cada punto de
  extensión (Docker, Redis, storage, runtime) en vez de asumir que la
  infraestructura ideal siempre está disponible.

## Licencia

[MIT](LICENSE) — úsalo, modifícalo y despliégalo libremente.
