# FleetPulse

Self-hosted agent-server monitoring platform for heterogeneous fleets
(Linux, Windows, Raspberry Pi) and orchestrators (Docker, Kubernetes).

A **central server** (collector + dashboard) receives telemetry from
**agents** you install on every machine you want to watch. One agent per
machine, one server for the whole fleet — the agent works the same way on
Linux, Windows, macOS, inside a Docker container, or as a Kubernetes Pod.

Repository: **https://github.com/gsan-dev/fleetpulse**

The original spec lives in
[config/system.md](https://github.com/gsan-dev/fleetpulse/blob/main/config/system.md).
This document is the reference for how it's built and how to run it.

## Architecture

```mermaid
flowchart LR
    subgraph Node["Node (Linux / Windows / macOS / K8s Pod)"]
        OS["gopsutil<br/>(CPU, RAM, disk, network)"]
        RT["Docker SDK / Kubelet API"]
        AG["fleetpulse-agent"]
        OS --> AG
        RT --> AG
    end

    AG -- "gRPC + mTLS or token<br/>(Register, PushMetrics,<br/>StreamCommands)" --> SRV

    subgraph Server["Central server"]
        SRV["fleetpulse-server"]
        SRV --> PG["TimescaleDB / PostgreSQL<br/>(nodes, metrics, containers)"]
        SRV --> RD["Redis<br/>(shared heartbeat, optional)"]
        SRV --> WH["Telegram / Discord<br/>(alert webhooks)"]
    end

    SRV -- "REST + SSE" --> WEB["Next.js dashboard"]
```

Each agent opens **three** long-lived gRPC channels to the server:
`PushMetrics` (telemetry, which also doubles as a heartbeat), `StreamCommands`
(the server pushes restarts/logs without the agent ever exposing an inbound
port) and one-off calls to `Register`/`ReportCommandResult`. The dashboard
never talks to agents directly: everything goes through the server's
HTTP/SSE API. The agent only needs outbound access to the server (port
50051 by default); nothing needs to be opened inbound on the machines you
monitor, even behind NAT or a firewall.

---

## 1. Launch the server

You need **one** (and only one) for your whole fleet: it's the central
point every agent reports to. Pick one option.

### Option A — Docker Compose (recommended)

Requires Docker. Brings up TimescaleDB, Redis, the server, and the dashboard
with a single command:

```bash
git clone https://github.com/gsan-dev/fleetpulse.git
cd fleetpulse
docker compose up -d --build
```

Dashboard at **http://localhost:3000**. Test agent token: `demo-token`
(change it before exposing this to anything beyond your own LAN — see
`AGENT_TOKENS` in
[docker-compose.yml](https://github.com/gsan-dev/fleetpulse/blob/main/docker-compose.yml)).

If any port (8080, 3000, 5432, 6379) is already in use on your machine,
remap the left-hand side in `docker-compose.yml` (e.g. `"8090:8080"`) and
point the `web` service's `NEXT_PUBLIC_API_URL` at whichever port you pick.

### Option B — Native binary (no Docker)

Requirements: Go 1.26+, and a reachable PostgreSQL/TimescaleDB + Redis (or
start in memory mode to try it out, see below).

```bash
make build-server   # or: go build -o bin/fleetpulse-server ./cmd/fleetpulse-server
./bin/fleetpulse-server \
  --database-url=postgres://user:pass@localhost:5432/fleetpulse \
  --redis-url=redis://localhost:6379/0 \
  --tokens=YOUR_SECRET_TOKEN
```

To try it without any database at all (everything kept in the process's
memory, lost on restart — for testing only, never for production):

```bash
./bin/fleetpulse-server --storage=memory --tokens=dev-token
```

The dashboard is a separate Next.js app; once the server is listening on
`--http-addr` (`:8080` by default):

```bash
cd web
cp .env.example .env.local   # adjust NEXT_PUBLIC_API_URL if it isn't localhost:8080
npm install
npm run build && npm start   # or `npm run dev` during development
```

All server configuration variables are in the
[table further down](#server-configuration).

---

## 2. Launch the agent — on any machine, at any time

Install one per machine you want to see in the dashboard. You'll need
beforehand the **server address** (`host:port`, port `50051` by default) and
the **token** you configured in `AGENT_TOKENS`.

### Linux (systemd) — no Docker

```bash
curl -sSL https://raw.githubusercontent.com/gsan-dev/fleetpulse/main/install/install.sh | sudo bash -s -- \
  --token=YOUR_SECRET_TOKEN --server=YOUR_SERVER_IP:50051
```

Installs the binary, creates the `fleetpulse-agent.service` unit, and starts
it with `systemctl enable --now`. Check status and logs:

```bash
systemctl status fleetpulse-agent
journalctl -u fleetpulse-agent -f
```

### Windows (native service) — PowerShell as Administrator

```powershell
irm https://raw.githubusercontent.com/gsan-dev/fleetpulse/main/install/install.ps1 -OutFile install.ps1
.\install.ps1 -Token YOUR_SECRET_TOKEN -Server YOUR_SERVER_IP:50051
```

Registers the `FleetPulseAgent` service with the Service Control Manager
(starts automatically, survives reboots). The token is stored in the
service's own registry key, never as a visible argument. Check status:

```powershell
Get-Service FleetPulseAgent
```

### Docker (any OS with Docker: Linux, Windows, macOS)

```bash
git clone https://github.com/gsan-dev/fleetpulse.git && cd fleetpulse
docker build -f cmd/fleetpulse-agent/Dockerfile -t fleetpulse-agent .
docker run -d --name fleetpulse-agent --restart unless-stopped \
  -e FLEETPULSE_SERVER=YOUR_SERVER_IP:50051 \
  -e AGENT_TOKEN=YOUR_SECRET_TOKEN \
  -e FLEETPULSE_HOSTNAME=$(hostname) \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v fleetpulse-agent-state:/var/lib/fleetpulse \
  fleetpulse-agent
```

`FLEETPULSE_HOSTNAME` matters: without it, the dashboard would show the
container's ID (something like `6931b28a9112`) as the node name, because
that's the hostname Docker assigns a container by default. Mounting
`docker.sock` is optional — only needed if you also want the agent to
report that machine's containers.

### macOS / any system with Go — manual

There's no native installer (`launchd`) yet, but the binary compiles and
runs the same as on every other platform:

```bash
git clone https://github.com/gsan-dev/fleetpulse.git && cd fleetpulse
go build -o fleetpulse-agent ./cmd/fleetpulse-agent
./fleetpulse-agent --server=YOUR_SERVER_IP:50051 --token=YOUR_SECRET_TOKEN
```

To survive a reboot you'll need to manage that yourself (a `launchd`
`.plist`, `screen`/`tmux`, or whatever process supervisor you already use).

### Kubernetes (DaemonSet — one agent per cluster node)

```bash
kubectl create secret generic fleetpulse-token --from-literal=token=YOUR_SECRET_TOKEN
helm install fleetpulse-agent deploy/helm/fleetpulse-agent \
  --set server.address=YOUR_SERVER_IP:50051 \
  --set server.tokenSecretName=fleetpulse-token
```

Chart:
[deploy/helm/fleetpulse-agent](https://github.com/gsan-dev/fleetpulse/tree/main/deploy/helm/fleetpulse-agent).
The agent automatically detects that it's running inside a Pod (the
`KUBERNETES_SERVICE_HOST` variable, which Kubernetes always injects) and
uses the kubelet API instead of Docker to read the node's containers.

### Try it without a server (local debugging)

A single sample printed to stdout, no server or token required — useful to
confirm the binary detects the system (and Docker, if present) correctly:

```bash
./fleetpulse-agent --once --log-level debug
```

---

## Roadmap status

| Phase | Content | Status |
|---|---|---|
| 1 | Go agent core (gopsutil, Docker SDK, protobuf) | ✅ |
| 2 | gRPC collector server + persistence (TimescaleDB, Redis) | ✅ |
| 3 | Real-time Next.js dashboard | ✅ |
| 4 | Alerting + Kubernetes DaemonSet mode | ✅ |
| 5 | Packaging, CI/CD, and documentation | ✅ |

See [Verification status](#verification-status) for what's been tested
against real infrastructure and what's still pending.

## Repository layout

```
cmd/fleetpulse-agent/     Agent binary (daemon / service)
cmd/fleetpulse-server/    Central collector binary
internal/collector/       System metrics (gopsutil), Docker SDK, and Kubelet API
internal/config/          Agent configuration (flags + env)
internal/identity/        Node fingerprint and persistent agent_id
internal/telemetry/       Agent domain -> protobuf
internal/transport/       Agent's gRPC client (Register, PushMetrics, TLS/mTLS)
internal/commands/        Agent's remote command executor (restart, on-demand logs)
internal/serverconfig/    Server configuration (flags + env)
internal/store/           Persistence interface + memstore (demo/tests) + pgstore (Postgres/Timescale)
internal/heartbeat/       Connectivity watchdog (memory or Redis)
internal/alert/           Alert channels (Telegram, Discord)
internal/grpcserver/      gRPC service implementation (server side)
internal/rpcauth/         Shared token authentication between agent and server
internal/commandbus/      Server -> agent command routing
internal/hub/             Live (SSE) metrics broadcast
internal/api/             REST + SSE API for the dashboard
proto/fleetpulse/v1/      gRPC/protobuf contract · gen/ is generated code (do not edit by hand)
web/                      Next.js dashboard (App Router, TypeScript, Tailwind, Recharts)
deploy/helm/              Kubernetes chart (agent DaemonSet)
install/                  One-line installers (Linux systemd, native Windows service)
.github/workflows/        CI (test/build/lint) and Release (binaries + images)
docker-compose.yml        Full local demo stack
```

## Development / building from source

Requirements: Go 1.26+, Node.js 22+. Regenerating the protobuf needs `buf`
and its plugins (`make tools`).

```bash
make tools     # first time only
make proto     # generates gen/ from proto/
make tidy
make test      # go test ./... -race (drop -race if no C compiler is available)
make build build-server
make web-install web-build
```

`make build-linux` / `make build-windows` cross-compile the agent for
release without needing those platforms.

## Agent configuration

| Flag | Variable | Default | Description |
|---|---|---|---|
| `--server` | `FLEETPULSE_SERVER` | — | `host:port` of the gRPC collector |
| `--token` | `AGENT_TOKEN` | — | Agent enrollment token |
| `--interval` | `FLEETPULSE_INTERVAL` | `15s` | Push cadence (the server may impose a different one on registration) |
| `--docker` | `FLEETPULSE_DOCKER` | `auto` | `auto`, `on`, or `off` |
| `--runtime` | `FLEETPULSE_RUNTIME` | `auto` | `auto`, `docker`, or `kubernetes` |
| `--state-dir` | `FLEETPULSE_STATE_DIR` | `/var/lib/fleetpulse` (root on Linux) | Where the `agent_id` is persisted |
| `--tls-ca` | `FLEETPULSE_TLS_CA` | — | CA to verify the server (enables TLS) |
| `--tls-cert` / `--tls-key` | `FLEETPULSE_TLS_CERT` / `FLEETPULSE_TLS_KEY` | — | Client certificate (mTLS) |
| `--log-level` | `FLEETPULSE_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `--hostname` | `FLEETPULSE_HOSTNAME` | — | Node name to report; overrides autodetection |
| `--once` | — | `false` | Print one sample to stdout and exit |

> **Agent in Docker**: always set `FLEETPULSE_HOSTNAME` (see the
> [section above](#docker-any-os-with-docker-linux-windows-macos)) —
> otherwise the dashboard shows the container's ID instead of the machine's
> real name.

`--server` and `--token` are required except in `--once` mode.
`fleetpulse-agent service install|uninstall|start|stop` manages the native
Windows service.

## Server configuration

| Flag | Variable | Default | Description |
|---|---|---|---|
| `--grpc-addr` | `FLEETPULSE_GRPC_ADDR` | `:50051` | gRPC listener (agents) |
| `--http-addr` | `FLEETPULSE_HTTP_ADDR` | `:8080` | HTTP/SSE listener (dashboard) |
| `--storage` | `FLEETPULSE_STORAGE` | `postgres` | `postgres` or `memory` (demo, non-persistent) |
| `--database-url` | `DATABASE_URL` | — | PostgreSQL/TimescaleDB DSN |
| `--redis-url` | `REDIS_URL` | — | Shared heartbeat backend across replicas (optional; defaults to in-process memory) |
| `--tokens` | `AGENT_TOKENS` | — | Valid agent tokens, comma-separated |
| `--heartbeat-timeout` | `FLEETPULSE_HEARTBEAT_TIMEOUT` | `45s` | Time without metrics before marking `Unreachable` |
| `--tls-cert` / `--tls-key` | `FLEETPULSE_TLS_CERT` / `FLEETPULSE_TLS_KEY` | — | TLS for the gRPC listener |
| `--tls-client-ca` | `FLEETPULSE_TLS_CLIENT_CA` | — | Require mTLS from agents |
| `--dashboard-token` | `FLEETPULSE_DASHBOARD_TOKEN` | — | Protects the HTTP API (empty = no auth, trusted LAN) |
| — | `FLEETPULSE_TELEGRAM_BOT_TOKEN` / `FLEETPULSE_TELEGRAM_CHAT_ID` | — | Telegram alerts |
| — | `FLEETPULSE_DISCORD_WEBHOOK_URL` | — | Discord alerts |

## Dashboard

Variables in `web/.env.local` (see
[web/.env.example](https://github.com/gsan-dev/fleetpulse/blob/main/web/.env.example)):

| Variable | Description |
|---|---|
| `NEXT_PUBLIC_API_URL` | Server URL (`http://localhost:8080` in development) |
| `NEXT_PUBLIC_API_TOKEN` | Only if the server requires `--dashboard-token` |

The dashboard talks **directly** to the Go API from the browser (there's no
Next.js proxy in between), so `NEXT_PUBLIC_*` is visible in the bundle —
the right threat model for a trusted LAN; exposing this to the internet
needs something more (a proxy with its own auth, a private network).

## Design decisions

- **The `agent_id` is generated by the client** and persisted in the state
  dir. Retrying registration after a lost server response doesn't duplicate
  the node in the dashboard.
- **The public IP is derived by the server** from the gRPC peer instead of
  trusting the agent's own report: a field sent by the client can be spoofed.
- **Token authentication on every gRPC call** (metadata, shared interceptor
  in `internal/rpcauth`), not just on `Register`: it also covers the metrics
  stream and the command channel with the same mechanism.
- **Heartbeat lives apart from the history**: in-process memory by default,
  optional Redis for multiple server replicas. Same `auto/on/off` pattern
  the agent already uses for Docker, for consistency.
- **Server → agent commands travel over a stream the agent opens**
  (`StreamCommands`), never the other way around: the agent never needs to
  expose an inbound port, which matters behind NAT/firewalls.
- **Kubelet and Docker share the same interface**
  (`collector.ContainerSource`) in the agent: the collector, the health
  state, and the metrics payload neither know nor care which runtime is
  underneath.
- **Container memory subtracts page cache** (`inactive_file` on cgroup v2,
  `cache` on v1), the same way `docker stats` does.

## Verification status

What's actually been tested, not just "it compiles":

- ✅ **Agent and server compile and pass their tests** on Windows (native)
  and Linux (cross-compiled amd64/arm64).
- ✅ **Deployed in a real two-machine networked scenario**: a full server
  (Docker Compose: TimescaleDB + Redis + server + dashboard) and a second
  agent on a different machine reporting over the network — registration,
  heartbeat, metrics streaming, listing *every* Docker container on the
  host, a container restart command (dispatch → execution → result
  reported back), and the hostname override, all verified live against
  real Postgres/TimescaleDB and Redis (not just in-memory).
- ✅ **Dashboard**: Next.js production build (strict TypeScript, no errors)
  serving the live panel over SSE against the real server.
- ⚠️ **The Kubelet inspector hasn't been tested against a real cluster**:
  the mapping logic (states, CPU/memory) has unit tests against synthetic
  JSON, but it still needs validation against a real kubelet, and the RBAC
  may need adjusting if the cluster has stricter policies than the Helm
  chart's.
- ⚠️ **TLS/mTLS**: the code (server and client) is implemented and covers
  both server and client certificates, but no end-to-end test certificate
  pair has been generated yet — documenting the issuance flow (or
  integrating with cert-manager on K8s) is still pending.
- ⚠️ Docker container labels aren't persisted yet in `pgstore`/`memstore`
  (they're read from the engine but discarded when the snapshot is saved);
  extending this is straightforward if filtering by them is ever needed.
- ⚠️ There's no native macOS installer (`launchd`); the binary compiles and
  runs the same, but managing its lifecycle is left up to you for now.
- ⚠️ CI (`.github/workflows/`) follows standard GitHub Actions patterns,
  but its first real run is worth double-checking now that the repository
  is public.

## Portfolio/interview differentiators

- **Layered security**: a shared token validated by an interceptor on every
  RPC, optional mTLS, public IP derived from the transport (not the
  payload), commands always initiated by the agent.
- **Genuinely cross-platform**: the same agent binary for Linux, Windows,
  macOS, and Kubernetes, with native installers for each
  (`systemd`, a Windows service via the SCM, DaemonSet + Helm).
- **Explicit degradation**: `auto/on/off` repeated at every extension point
  (Docker, Redis, storage, runtime) instead of assuming the ideal
  infrastructure is always available.

## License

[MIT](https://github.com/gsan-dev/fleetpulse/blob/main/LICENSE) — use it,
modify it, and deploy it freely.
