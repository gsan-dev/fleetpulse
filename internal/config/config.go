// Package config resuelve la configuracion del agente a partir de flags y
// variables de entorno, en ese orden de prioridad. El instalador de una sola
// linea escribe las variables en la unidad systemd, y los flags quedan para
// pruebas manuales.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// DockerMode decide si el agente inspecciona el runtime de contenedores.
type DockerMode string

const (
	// DockerAuto intenta conectar y sigue adelante en silencio si no hay
	// daemon: es el modo correcto para una flota heterogenea donde solo
	// algunos nodos ejecutan Docker.
	DockerAuto DockerMode = "auto"
	// DockerOn exige un daemon accesible y aborta el arranque si no lo hay.
	DockerOn DockerMode = "on"
	// DockerOff desactiva la inspeccion de contenedores.
	DockerOff DockerMode = "off"
)

// Defaults del agente.
const (
	DefaultInterval = 15 * time.Second
	// MinInterval evita que un valor mal puesto inunde al recolector.
	MinInterval = time.Second
)

// Config es la configuracion efectiva del agente.
type Config struct {
	// ServerAddr es el host:puerto gRPC del recolector central.
	ServerAddr string
	// Token es el AGENT_TOKEN con el que el nodo se da de alta.
	Token string
	// Interval es la cadencia de envio de rafagas de metricas. El servidor
	// marca el nodo como Unreachable a los 45s sin recibir nada, asi que el
	// valor por defecto deja margen para dos fallos seguidos.
	Interval time.Duration
	// StateDir guarda el agent_id persistido entre reinicios.
	StateDir string
	// Docker controla la inspeccion de contenedores via el socket local.
	Docker DockerMode
	// Runtime elige la fuente de contenedores: "auto" usa Kubelet cuando el
	// proceso detecta que corre dentro de un Pod (variable de entorno
	// KUBERNETES_SERVICE_HOST, que Kubernetes inyecta siempre) y Docker en
	// cualquier otro caso; "docker" y "kubernetes" fuerzan una de las dos.
	Runtime string
	// Once toma una sola muestra, la imprime y termina. Util para depurar un
	// nodo recien instalado sin levantar el servicio.
	Once bool
	// LogLevel: debug, info, warn o error.
	LogLevel string

	// Hostname, cuando no esta vacio, sustituye al hostname autodetectado.
	// Imprescindible cuando el agente corre dentro de un contenedor Docker:
	// el hostname del contenedor (normalmente su ID corto) no es el nombre
	// real de la maquina que lo aloja.
	Hostname string

	// TLSCAFile verifica el certificado del servidor. Vacio => conexion en
	// texto plano (uso recomendado solo en una LAN de confianza o con el
	// servidor detras de un proxy TLS).
	TLSCAFile string
	// TLSCertFile/TLSKeyFile son el certificado de cliente para mTLS. Ambos
	// vacios => TLS de servidor normal sin autenticar el cliente por certificado.
	TLSCertFile string
	TLSKeyFile  string
}

// Load resuelve la configuracion. `args` son los argumentos sin el nombre del
// binario; `output` recibe el texto de ayuda de los flags.
func Load(args []string, output io.Writer) (*Config, error) {
	cfg := &Config{
		ServerAddr: os.Getenv("FLEETPULSE_SERVER"),
		Token:      os.Getenv("AGENT_TOKEN"),
		Interval:   DefaultInterval,
		StateDir:   envOrDefault("FLEETPULSE_STATE_DIR", defaultStateDir()),
		Docker:     DockerMode(strings.ToLower(envOrDefault("FLEETPULSE_DOCKER", string(DockerAuto)))),
		Runtime:    strings.ToLower(envOrDefault("FLEETPULSE_RUNTIME", "auto")),
		LogLevel:   strings.ToLower(envOrDefault("FLEETPULSE_LOG_LEVEL", "info")),

		TLSCAFile:   os.Getenv("FLEETPULSE_TLS_CA"),
		TLSCertFile: os.Getenv("FLEETPULSE_TLS_CERT"),
		TLSKeyFile:  os.Getenv("FLEETPULSE_TLS_KEY"),
		Hostname:    os.Getenv("FLEETPULSE_HOSTNAME"),
	}

	if raw := os.Getenv("FLEETPULSE_INTERVAL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("FLEETPULSE_INTERVAL invalido (%q): %w", raw, err)
		}
		cfg.Interval = parsed
	}

	fs := flag.NewFlagSet("fleetpulse-agent", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.StringVar(&cfg.ServerAddr, "server", cfg.ServerAddr, "host:puerto gRPC del recolector (env FLEETPULSE_SERVER)")
	fs.StringVar(&cfg.Token, "token", cfg.Token, "token de alta del agente (env AGENT_TOKEN)")
	fs.DurationVar(&cfg.Interval, "interval", cfg.Interval, "cadencia de envio de metricas (env FLEETPULSE_INTERVAL)")
	fs.StringVar(&cfg.StateDir, "state-dir", cfg.StateDir, "directorio donde persistir el agent_id (env FLEETPULSE_STATE_DIR)")
	docker := fs.String("docker", string(cfg.Docker), "inspeccion de contenedores: auto, on u off (env FLEETPULSE_DOCKER)")
	fs.StringVar(&cfg.Runtime, "runtime", cfg.Runtime, "fuente de contenedores: auto, docker o kubernetes (env FLEETPULSE_RUNTIME)")
	fs.BoolVar(&cfg.Once, "once", false, "tomar una sola muestra, imprimirla y salir")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "debug, info, warn o error (env FLEETPULSE_LOG_LEVEL)")
	fs.StringVar(&cfg.TLSCAFile, "tls-ca", cfg.TLSCAFile, "CA para verificar el certificado del servidor (env FLEETPULSE_TLS_CA)")
	fs.StringVar(&cfg.TLSCertFile, "tls-cert", cfg.TLSCertFile, "certificado de cliente para mTLS (env FLEETPULSE_TLS_CERT)")
	fs.StringVar(&cfg.TLSKeyFile, "tls-key", cfg.TLSKeyFile, "clave privada de cliente para mTLS (env FLEETPULSE_TLS_KEY)")
	fs.StringVar(&cfg.Hostname, "hostname", cfg.Hostname, "nombre del nodo a reportar, sustituye al autodetectado (env FLEETPULSE_HOSTNAME; imprescindible en Docker)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	cfg.Docker = DockerMode(strings.ToLower(*docker))

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	switch c.Docker {
	case DockerAuto, DockerOn, DockerOff:
	default:
		return fmt.Errorf("modo docker desconocido: %q (usa auto, on u off)", c.Docker)
	}

	switch c.Runtime {
	case "auto", "docker", "kubernetes":
	default:
		return fmt.Errorf("runtime desconocido: %q (usa auto, docker o kubernetes)", c.Runtime)
	}

	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("nivel de log desconocido: %q", c.LogLevel)
	}

	if c.Interval < MinInterval {
		return fmt.Errorf("el intervalo minimo es %s, se recibio %s", MinInterval, c.Interval)
	}

	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		return errors.New("--tls-cert y --tls-key deben darse juntos")
	}

	// En modo --once no se transmite nada, asi que el nodo puede depurarse
	// antes de tener token o servidor.
	if c.Once {
		return nil
	}
	if c.ServerAddr == "" {
		return errors.New("falta la direccion del servidor (--server o FLEETPULSE_SERVER)")
	}
	if c.Token == "" {
		return errors.New("falta el token del agente (--token o AGENT_TOKEN)")
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
