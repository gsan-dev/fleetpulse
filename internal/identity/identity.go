// Package identity construye la huella del nodo que se envia en el registro
// (Auto-Discovery) y persiste el agent_id entre reinicios.
package identity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

// agentIDFile es el fichero dentro del state dir con el identificador estable.
const agentIDFile = "agent-id"

// Node es la identidad del nodo tal como la ve el servidor central.
type Node struct {
	AgentID         string
	Hostname        string
	OS              string
	Platform        string
	PlatformVersion string
	KernelVersion   string
	Arch            string
	LocalIP         string
	AgentVersion    string
	CPUCores        uint32
	MemoryTotal     uint64
	BootTime        time.Time
}

// Load reune la huella del nodo y resuelve su agent_id, generandolo y
// persistiendolo la primera vez. Los datos "blandos" (plataforma, IP local,
// nucleos) se rellenan a mejor esfuerzo: que falte uno no debe impedir que el
// agente arranque y reporte.
//
// `hostnameOverride`, cuando no esta vacio, sustituye por completo al
// hostname detectado. Hace falta porque dentro de un contenedor Docker
// `os.Hostname()` (y tambien gopsutil) devuelven el hostname del propio
// contenedor -normalmente su ID corto, ilegible en el panel- y no el de la
// maquina real que lo aloja; viene de --hostname/FLEETPULSE_HOSTNAME
// (ver internal/config), que quien despliegue el contenedor debe fijar al
// nombre real del host.
func Load(ctx context.Context, stateDir, agentVersion, hostnameOverride string) (*Node, error) {
	agentID, err := loadOrCreateAgentID(stateDir)
	if err != nil {
		return nil, err
	}

	node := &Node{
		AgentID:      agentID,
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		AgentVersion: agentVersion,
		LocalIP:      localIP(),
	}

	if hostname, err := os.Hostname(); err == nil {
		node.Hostname = hostname
	}

	if info, err := host.InfoWithContext(ctx); err == nil {
		if node.Hostname == "" {
			node.Hostname = info.Hostname
		}
		node.Platform = info.Platform
		node.PlatformVersion = info.PlatformVersion
		node.KernelVersion = info.KernelVersion
		node.BootTime = time.Unix(int64(info.BootTime), 0)
	}

	if hostnameOverride != "" {
		node.Hostname = hostnameOverride
	}

	if counts, err := cpu.CountsWithContext(ctx, true); err == nil {
		node.CPUCores = uint32(counts)
	}

	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		node.MemoryTotal = vm.Total
	}

	return node, nil
}

// loadOrCreateAgentID lee el identificador persistido o crea uno nuevo. Se
// genera en el cliente para que el agente pueda reintentar el registro sin
// duplicar nodos en el panel si la primera respuesta del servidor se pierde.
func loadOrCreateAgentID(stateDir string) (string, error) {
	path := filepath.Join(stateDir, agentIDFile)

	raw, err := os.ReadFile(path)
	if err == nil {
		if id := strings.TrimSpace(string(raw)); id != "" {
			return id, nil
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("leer %s: %w", path, err)
	}

	id, err := newAgentID()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		return "", fmt.Errorf("crear %s: %w", stateDir, err)
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o640); err != nil {
		return "", fmt.Errorf("escribir %s: %w", path, err)
	}
	return id, nil
}

func newAgentID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generar agent_id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// localIP averigua con que IP saldria el trafico hacia el exterior. El dial
// UDP no envia ningun paquete: solo hace que el kernel resuelva la ruta y
// asigne la interfaz de salida, asi que funciona tambien sin conectividad.
func localIP() string {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return firstNonLoopbackIP()
	}
	defer conn.Close()

	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP.String()
	}
	return firstNonLoopbackIP()
}

func firstNonLoopbackIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP.String()
		}
	}
	return ""
}
