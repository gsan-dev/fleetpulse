# Instalador del agente FleetPulse para Windows (servicio nativo via SCM).
#
# Ejecutar en PowerShell como Administrador:
#
#   .\install.ps1 -Token TU_TOKEN_SECRETO -Server fleetpulse.tu-dominio.com:50051
#
# Descarga el binario desde las Releases de GitHub, lo instala en
# C:\Program Files\FleetPulse, registra el servicio "FleetPulseAgent"
# (usando el propio `fleetpulse-agent service install`, ver
# cmd/fleetpulse-agent/service_windows.go) y lo arranca.
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Token,
    [Parameter(Mandatory = $true)][string]$Server,
    [string]$Version = "latest",
    [string]$Interval = "15s",
    [ValidateSet("auto", "on", "off")][string]$Docker = "auto",
    [ValidateSet("auto", "docker", "kubernetes")][string]$Runtime = "auto",
    [ValidateSet("debug", "info", "warn", "error")][string]$LogLevel = "info",
    [string]$TlsCa = "",
    [string]$TlsCert = "",
    [string]$TlsKey = "",
    [string]$InstallDir = "$env:ProgramFiles\FleetPulse",
    [string]$StateDir = "$env:ProgramData\FleetPulse",
    [string]$Repo = "gdev/fleetpulse"
)

$ErrorActionPreference = "Stop"

function Assert-Admin {
    $principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw "Este instalador necesita PowerShell como Administrador."
    }
}

function Get-AgentArch {
    switch ($env:PROCESSOR_ARCHITECTURE) {
        "AMD64" { return "amd64" }
        "ARM64" { return "arm64" }
        default { throw "Arquitectura no soportada: $env:PROCESSOR_ARCHITECTURE (solo amd64 y arm64)" }
    }
}

Assert-Admin

$arch = Get-AgentArch
if ($Version -eq "latest") {
    $assetUrl = "https://github.com/$Repo/releases/latest/download/fleetpulse-agent-windows-$arch.exe"
} else {
    $assetUrl = "https://github.com/$Repo/releases/download/$Version/fleetpulse-agent-windows-$arch.exe"
}

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
New-Item -ItemType Directory -Force -Path $StateDir | Out-Null
$exePath = Join-Path $InstallDir "fleetpulse-agent.exe"

Write-Host "==> Descargando fleetpulse-agent ($arch, $Version) desde GitHub Releases..." -ForegroundColor Cyan
try {
    Invoke-WebRequest -Uri $assetUrl -OutFile $exePath -UseBasicParsing
} catch {
    throw "No se pudo descargar $assetUrl (¿existe esa version/arquitectura?): $_"
}
Write-Host "==> Binario instalado en $exePath" -ForegroundColor Cyan

# Si ya existe un servicio de una instalacion previa, se retira primero para
# que 'service install' (que falla si ya esta registrado) pueda recrearlo
# limpio con la configuracion nueva.
$existing = Get-Service -Name FleetPulseAgent -ErrorAction SilentlyContinue
if ($existing) {
    Write-Host "==> Servicio existente encontrado, deteniendolo para reinstalar..." -ForegroundColor Yellow
    if ($existing.Status -eq "Running") { Stop-Service FleetPulseAgent -Force }
    & $exePath service uninstall | Out-Null
}

$serviceArgs = @(
    "service", "install",
    "--server=$Server",
    "--interval=$Interval",
    "--docker=$Docker",
    "--runtime=$Runtime",
    "--log-level=$LogLevel",
    "--state-dir=$StateDir"
)
if ($TlsCa)   { $serviceArgs += "--tls-ca=$TlsCa" }
if ($TlsCert) { $serviceArgs += "--tls-cert=$TlsCert" }
if ($TlsKey)  { $serviceArgs += "--tls-key=$TlsKey" }

Write-Host "==> Registrando el servicio FleetPulseAgent..." -ForegroundColor Cyan
& $exePath @serviceArgs
if ($LASTEXITCODE -ne 0) { throw "fleetpulse-agent service install fallo con codigo $LASTEXITCODE" }

# El token NUNCA se pasa como argumento del servicio (quedaria visible en
# `sc qc` / el Administrador de tareas): se inyecta via la clave de entorno
# especifica del servicio en el registro, que el SCM aplica solo al arrancarlo.
$serviceKey = "HKLM:\SYSTEM\CurrentControlSet\Services\FleetPulseAgent"
New-ItemProperty -Path $serviceKey -Name "Environment" -PropertyType MultiString -Value @("AGENT_TOKEN=$Token") -Force | Out-Null

Write-Host "==> Iniciando el servicio..." -ForegroundColor Cyan
Start-Service FleetPulseAgent

Write-Host "==> Listo. Comprueba el estado con:" -ForegroundColor Green
Write-Host "    Get-Service FleetPulseAgent"
Write-Host "    Get-EventLog -LogName Application -Source FleetPulseAgent -Newest 20"
