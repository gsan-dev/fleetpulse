// Package serverconfig resuelve la configuracion del recolector central:
// flags y variables de entorno, en ese orden de prioridad, igual que
// internal/config hace para el agente.
package serverconfig

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// StorageMode decide donde se persiste la telemetria.
type StorageMode string

const (
	// StoragePostgres usa TimescaleDB/PostgreSQL. Exige DATABASE_URL.
	StoragePostgres StorageMode = "postgres"
	// StorageMemory guarda todo en el proceso: pensado para probar el panel
	// completo sin levantar infraestructura, no para produccion (se pierde
	// todo al reiniciar el servidor).
	StorageMemory StorageMode = "memory"
)

// Defaults del servidor.
const (
	DefaultGRPCAddr          = ":50051"
	DefaultHTTPAddr          = ":8080"
	DefaultHeartbeatTimeout  = 45 * time.Second
	DefaultHeartbeatInterval = 15 * time.Second
	// DefaultMetricRetention es cuanto se conservan las muestras crudas antes
	// de que el servidor las purgue. TimescaleDB las compacta con su propia
	// politica; en modo memoria evita un crecimiento sin limite.
	DefaultMetricRetention = 7 * 24 * time.Hour
)

// Config es la configuracion efectiva del servidor.
type Config struct {
	GRPCAddr string
	HTTPAddr string

	// Tokens son los AGENT_TOKEN validos para Register/PushMetrics/comandos.
	Tokens []string

	Storage     StorageMode
	DatabaseURL string

	// RedisURL habilita el backend de heartbeat en Redis, compartido entre
	// varias replicas del servidor. Vacio => backend en memoria del proceso,
	// suficiente para una unica instancia (caso self-hosted mas comun).
	RedisURL string

	HeartbeatTimeout time.Duration
	MetricRetention  time.Duration

	// TLS del lado servidor para el listener gRPC. Ambos vacios => texto
	// plano (uso recomendado solo detras de un proxy TLS o en LAN de confianza).
	TLSCertFile string
	TLSKeyFile  string
	// TLSClientCAFile activa mTLS: solo agentes con certificado firmado por
	// esta CA pueden conectar. Vacio => TLS de servidor normal sin mTLS.
	TLSClientCAFile string

	// DashboardToken protege la API HTTP que consume el dashboard. Vacio =>
	// sin autenticacion, adecuado para una LAN de confianza.
	DashboardToken string
	// CORSOrigin es el origen permitido para la API HTTP (dashboard en otro
	// puerto/host durante desarrollo).
	CORSOrigin string

	TelegramBotToken  string
	TelegramChatID    string
	DiscordWebhookURL string

	LogLevel string
}

// Load resuelve la configuracion del servidor.
func Load(args []string, output io.Writer) (*Config, error) {
	cfg := &Config{
		GRPCAddr:          envOrDefault("FLEETPULSE_GRPC_ADDR", DefaultGRPCAddr),
		HTTPAddr:          envOrDefault("FLEETPULSE_HTTP_ADDR", DefaultHTTPAddr),
		Storage:           StorageMode(strings.ToLower(envOrDefault("FLEETPULSE_STORAGE", string(StoragePostgres)))),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		RedisURL:          os.Getenv("REDIS_URL"),
		HeartbeatTimeout:  DefaultHeartbeatTimeout,
		MetricRetention:   DefaultMetricRetention,
		TLSCertFile:       os.Getenv("FLEETPULSE_TLS_CERT"),
		TLSKeyFile:        os.Getenv("FLEETPULSE_TLS_KEY"),
		TLSClientCAFile:   os.Getenv("FLEETPULSE_TLS_CLIENT_CA"),
		DashboardToken:    os.Getenv("FLEETPULSE_DASHBOARD_TOKEN"),
		CORSOrigin:        envOrDefault("FLEETPULSE_CORS_ORIGIN", "*"),
		TelegramBotToken:  os.Getenv("FLEETPULSE_TELEGRAM_BOT_TOKEN"),
		TelegramChatID:    os.Getenv("FLEETPULSE_TELEGRAM_CHAT_ID"),
		DiscordWebhookURL: os.Getenv("FLEETPULSE_DISCORD_WEBHOOK_URL"),
		LogLevel:          strings.ToLower(envOrDefault("FLEETPULSE_LOG_LEVEL", "info")),
	}

	if raw := os.Getenv("AGENT_TOKENS"); raw != "" {
		cfg.Tokens = splitTokens(raw)
	}
	if raw := os.Getenv("FLEETPULSE_HEARTBEAT_TIMEOUT"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("FLEETPULSE_HEARTBEAT_TIMEOUT invalido (%q): %w", raw, err)
		}
		cfg.HeartbeatTimeout = parsed
	}
	if raw := os.Getenv("FLEETPULSE_METRIC_RETENTION"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("FLEETPULSE_METRIC_RETENTION invalido (%q): %w", raw, err)
		}
		cfg.MetricRetention = parsed
	}

	fs := flag.NewFlagSet("fleetpulse-server", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.StringVar(&cfg.GRPCAddr, "grpc-addr", cfg.GRPCAddr, "direccion de escucha gRPC (env FLEETPULSE_GRPC_ADDR)")
	fs.StringVar(&cfg.HTTPAddr, "http-addr", cfg.HTTPAddr, "direccion de escucha HTTP/API (env FLEETPULSE_HTTP_ADDR)")
	storage := fs.String("storage", string(cfg.Storage), "postgres o memory (env FLEETPULSE_STORAGE)")
	fs.StringVar(&cfg.DatabaseURL, "database-url", cfg.DatabaseURL, "DSN de PostgreSQL/TimescaleDB (env DATABASE_URL)")
	fs.StringVar(&cfg.RedisURL, "redis-url", cfg.RedisURL, "URL de Redis para heartbeat compartido (env REDIS_URL)")
	tokens := fs.String("tokens", strings.Join(cfg.Tokens, ","), "tokens de agente validos, separados por comas (env AGENT_TOKENS)")
	fs.DurationVar(&cfg.HeartbeatTimeout, "heartbeat-timeout", cfg.HeartbeatTimeout, "tiempo sin metricas antes de marcar Unreachable")
	fs.StringVar(&cfg.TLSCertFile, "tls-cert", cfg.TLSCertFile, "certificado TLS del servidor gRPC (env FLEETPULSE_TLS_CERT)")
	fs.StringVar(&cfg.TLSKeyFile, "tls-key", cfg.TLSKeyFile, "clave privada TLS del servidor gRPC (env FLEETPULSE_TLS_KEY)")
	fs.StringVar(&cfg.TLSClientCAFile, "tls-client-ca", cfg.TLSClientCAFile, "CA para exigir mTLS a los agentes (env FLEETPULSE_TLS_CLIENT_CA)")
	fs.StringVar(&cfg.DashboardToken, "dashboard-token", cfg.DashboardToken, "token que debe enviar el dashboard a la API (env FLEETPULSE_DASHBOARD_TOKEN)")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "debug, info, warn o error (env FLEETPULSE_LOG_LEVEL)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	cfg.Storage = StorageMode(strings.ToLower(*storage))
	if *tokens != "" {
		cfg.Tokens = splitTokens(*tokens)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	switch c.Storage {
	case StoragePostgres, StorageMemory:
	default:
		return fmt.Errorf("storage desconocido: %q (usa postgres o memory)", c.Storage)
	}
	if c.Storage == StoragePostgres && c.DatabaseURL == "" {
		return errors.New("falta DATABASE_URL (o usa --storage=memory para probar sin base de datos)")
	}

	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("nivel de log desconocido: %q", c.LogLevel)
	}

	if len(c.Tokens) == 0 {
		return errors.New("no se ha configurado ningun AGENT_TOKEN valido (--tokens o AGENT_TOKENS)")
	}

	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		return errors.New("--tls-cert y --tls-key deben darse juntos")
	}
	if c.TLSClientCAFile != "" && c.TLSCertFile == "" {
		return errors.New("--tls-client-ca (mTLS) requiere tambien --tls-cert y --tls-key")
	}

	if c.HeartbeatTimeout < time.Second {
		return fmt.Errorf("heartbeat-timeout minimo 1s, se recibio %s", c.HeartbeatTimeout)
	}

	return nil
}

func splitTokens(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
