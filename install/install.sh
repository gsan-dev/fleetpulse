#!/usr/bin/env bash
# Instalador de una sola linea del agente FleetPulse para Linux (systemd).
#
#   curl -sSL https://fleetpulse.tu-dominio.com/install.sh | sudo bash -s -- \
#     --token=TU_TOKEN_SECRETO --server=fleetpulse.tu-dominio.com:50051
#
# Descarga el binario adecuado desde las Releases de GitHub, crea el servicio
# systemd y lo arranca. Pensado para ejecutarse como root (necesita escribir
# en /usr/local/bin, /etc/systemd/system y, salvo --docker=off, leer
# /var/run/docker.sock).
set -euo pipefail

REPO="gdev/fleetpulse"
BIN_NAME="fleetpulse-agent"
INSTALL_DIR="/usr/local/bin"
ENV_FILE="/etc/fleetpulse/agent.env"
SERVICE_FILE="/etc/systemd/system/fleetpulse-agent.service"
STATE_DIR="/var/lib/fleetpulse"

VERSION="latest"
TOKEN=""
SERVER=""
INTERVAL="15s"
DOCKER_MODE="auto"
RUNTIME="auto"
LOG_LEVEL="info"
TLS_CA=""
TLS_CERT=""
TLS_KEY=""

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Uso: install.sh --token=TOKEN --server=HOST:PUERTO [opciones]

Obligatorios:
  --token=TOKEN            AGENT_TOKEN dado de alta en el servidor
  --server=HOST:PUERTO     Direccion gRPC del recolector central

Opcionales:
  --version=vX.Y.Z         Version a instalar (por defecto: la ultima release)
  --interval=15s           Cadencia de envio de metricas
  --docker=auto|on|off     Inspeccion de contenedores Docker (por defecto: auto)
  --runtime=auto|docker|kubernetes  Fuente de contenedores (por defecto: auto)
  --log-level=info         debug, info, warn o error
  --tls-ca=/ruta/ca.crt        CA para verificar el servidor (activa TLS)
  --tls-cert=/ruta/cliente.crt Certificado de cliente (mTLS)
  --tls-key=/ruta/cliente.key  Clave de cliente (mTLS)
  -h, --help                Muestra esta ayuda
EOF
}

for arg in "$@"; do
  case "$arg" in
    --token=*) TOKEN="${arg#*=}" ;;
    --server=*) SERVER="${arg#*=}" ;;
    --version=*) VERSION="${arg#*=}" ;;
    --interval=*) INTERVAL="${arg#*=}" ;;
    --docker=*) DOCKER_MODE="${arg#*=}" ;;
    --runtime=*) RUNTIME="${arg#*=}" ;;
    --log-level=*) LOG_LEVEL="${arg#*=}" ;;
    --tls-ca=*) TLS_CA="${arg#*=}" ;;
    --tls-cert=*) TLS_CERT="${arg#*=}" ;;
    --tls-key=*) TLS_KEY="${arg#*=}" ;;
    -h|--help) usage; exit 0 ;;
    *) die "opcion desconocida: $arg (usa --help)" ;;
  esac
done

[[ "$(id -u)" -eq 0 ]] || die "este instalador necesita ejecutarse como root (usa sudo)"
[[ -n "$TOKEN" ]] || die "falta --token=TU_TOKEN_SECRETO"
[[ -n "$SERVER" ]] || die "falta --server=host:puerto"
command -v curl >/dev/null 2>&1 || die "hace falta curl"
command -v systemctl >/dev/null 2>&1 || die "este instalador requiere systemd"

case "$(uname -m)" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) die "arquitectura no soportada: $(uname -m) (solo amd64 y arm64)" ;;
esac
[[ "$(uname -s)" == "Linux" ]] || die "este instalador es solo para Linux; en Windows usa install.ps1"

if [[ "$VERSION" == "latest" ]]; then
  ASSET_URL="https://github.com/${REPO}/releases/latest/download/${BIN_NAME}-linux-${ARCH}"
else
  ASSET_URL="https://github.com/${REPO}/releases/download/${VERSION}/${BIN_NAME}-linux-${ARCH}"
fi

log "Descargando ${BIN_NAME} (${ARCH}, ${VERSION}) desde GitHub Releases..."
TMP_BIN="$(mktemp)"
trap 'rm -f "$TMP_BIN"' EXIT
curl -fsSL "$ASSET_URL" -o "$TMP_BIN" || die "no se pudo descargar $ASSET_URL (¿existe esa version/arquitectura?)"

install -m 0755 "$TMP_BIN" "${INSTALL_DIR}/${BIN_NAME}"
log "Binario instalado en ${INSTALL_DIR}/${BIN_NAME}"

mkdir -p "$(dirname "$ENV_FILE")" "$STATE_DIR"
{
  echo "AGENT_TOKEN=${TOKEN}"
  echo "FLEETPULSE_SERVER=${SERVER}"
  echo "FLEETPULSE_INTERVAL=${INTERVAL}"
  echo "FLEETPULSE_DOCKER=${DOCKER_MODE}"
  echo "FLEETPULSE_RUNTIME=${RUNTIME}"
  echo "FLEETPULSE_LOG_LEVEL=${LOG_LEVEL}"
  echo "FLEETPULSE_STATE_DIR=${STATE_DIR}"
  [[ -n "$TLS_CA" ]] && echo "FLEETPULSE_TLS_CA=${TLS_CA}"
  [[ -n "$TLS_CERT" ]] && echo "FLEETPULSE_TLS_CERT=${TLS_CERT}"
  [[ -n "$TLS_KEY" ]] && echo "FLEETPULSE_TLS_KEY=${TLS_KEY}"
} > "$ENV_FILE"
chmod 0600 "$ENV_FILE" # el token queda ahi dentro: solo root debe poder leerlo
log "Configuracion escrita en ${ENV_FILE}"

cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=FleetPulse Agent (telemetria de sistema y contenedores)
Documentation=https://github.com/${REPO}
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=${ENV_FILE}
ExecStart=${INSTALL_DIR}/${BIN_NAME}
Restart=on-failure
RestartSec=5s
# root para poder leer /var/run/docker.sock salvo que se use --docker=off;
# si el nodo no ejecuta Docker, cambia esto a un usuario sin privilegios.
User=root
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=${STATE_DIR}

[Install]
WantedBy=multi-user.target
EOF
log "Unidad systemd escrita en ${SERVICE_FILE}"

systemctl daemon-reload
systemctl enable --now fleetpulse-agent.service
log "Servicio fleetpulse-agent iniciado. Comprueba el estado con:"
echo "    systemctl status fleetpulse-agent"
echo "    journalctl -u fleetpulse-agent -f"
