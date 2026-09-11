// Command fleetpulse-server es el recolector central: recibe la telemetria
// de los agentes por gRPC, la persiste, vigila que nodos han dejado de
// responder y expone una API HTTP/SSE para el dashboard.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	fleetpulsev1 "github.com/gdev/fleetpulse/gen/fleetpulse/v1"
	"github.com/gdev/fleetpulse/internal/alert"
	"github.com/gdev/fleetpulse/internal/api"
	"github.com/gdev/fleetpulse/internal/commandbus"
	"github.com/gdev/fleetpulse/internal/grpcserver"
	"github.com/gdev/fleetpulse/internal/heartbeat"
	"github.com/gdev/fleetpulse/internal/hub"
	"github.com/gdev/fleetpulse/internal/parallel"
	"github.com/gdev/fleetpulse/internal/rpcauth"
	"github.com/gdev/fleetpulse/internal/serverconfig"
	"github.com/gdev/fleetpulse/internal/store"
	"github.com/gdev/fleetpulse/internal/store/memstore"
	"github.com/gdev/fleetpulse/internal/store/pgstore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintf(os.Stderr, "fleetpulse-server: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := serverconfig.Load(args, os.Stderr)
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	heartbeats, closeHeartbeats, err := openHeartbeatBackend(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeHeartbeats()

	// Sin esto, un reinicio del servidor (o de Redis, en despliegues con
	// backend compartido) deja el heartbeat en blanco: un nodo realmente
	// caido no tiene entrada que barrer y se queda marcado "Saludable" para
	// siempre en el panel, con el ultimo last_seen_at que quedo persistido,
	// en vez de pasar a "Unreachable" pasado el timeout.
	seedHeartbeats(ctx, st, heartbeats, time.Now().UTC(), cfg.HeartbeatTimeout, log)

	alerter := buildAlerter(cfg, log)
	watchdog := heartbeat.NewWatchdog(heartbeats, alerter, func(ctx context.Context, agentID string) (string, error) {
		node, err := st.GetNode(ctx, agentID)
		return node.Hostname, err
	}, cfg.HeartbeatTimeout, log)
	go watchdog.Run(ctx)

	go pruneLoop(ctx, st, cfg.MetricRetention, log)

	metricsHub := hub.New[*fleetpulsev1.MetricPayload]()
	commands := commandbus.New()

	grpcServer, err := newGRPCServer(cfg, st, watchdog, metricsHub, commands, log)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("escuchar en %s: %w", cfg.GRPCAddr, err)
	}

	httpServer := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: api.New(st, heartbeats, commands, metricsHub, cfg.DashboardToken, cfg.CORSOrigin, log).Handler(),
	}

	errCh := make(chan error, 2)
	go func() {
		log.Info("servidor gRPC escuchando", "addr", cfg.GRPCAddr, "tls", cfg.TLSCertFile != "", "mtls", cfg.TLSClientCAFile != "")
		if err := grpcServer.Serve(listener); err != nil {
			errCh <- fmt.Errorf("servidor gRPC: %w", err)
		}
	}()
	go func() {
		log.Info("api HTTP escuchando", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("servidor HTTP: %w", err)
		}
	}()

	log.Info("fleetpulse-server iniciado", "version", version, "storage", cfg.Storage)

	select {
	case <-ctx.Done():
		log.Info("parando el servidor")
	case err := <-errCh:
		log.Error("fallo irrecuperable", "error", err)
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	grpcServer.GracefulStop()

	return nil
}

func openStore(ctx context.Context, cfg *serverconfig.Config) (store.Store, error) {
	switch cfg.Storage {
	case serverconfig.StorageMemory:
		return memstore.New(), nil
	default:
		st, err := pgstore.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			return nil, fmt.Errorf("abrir postgres: %w", err)
		}
		return st, nil
	}
}

func openHeartbeatBackend(ctx context.Context, cfg *serverconfig.Config) (heartbeat.Backend, func(), error) {
	if cfg.RedisURL == "" {
		return heartbeat.NewMemoryBackend(), func() {}, nil
	}

	backend, err := heartbeat.NewRedisBackend(ctx, cfg.RedisURL)
	if err != nil {
		return nil, nil, fmt.Errorf("abrir redis: %w", err)
	}
	return backend, func() { _ = backend.Close() }, nil
}

// seedConcurrency acota cuantas llamadas Seed van a la vez contra el backend
// de heartbeat. Con Redis cada una es un viaje de red; en una flota de miles
// de nodos, hacerlo en serie retrasaria el arranque del servidor (y con el,
// cualquier readiness probe) en proporcion al tamano de la flota.
const seedConcurrency = 32

// seedHeartbeats precarga el backend de heartbeat con el last_seen_at
// persistido de cada nodo conocido. El backend arranca siempre vacio (en
// memoria o en un Redis que se acaba de reiniciar), y heartbeat.Sweep solo
// puede marcar Unreachable a los agentes que tienen entrada: sin este
// precargado, un nodo que de verdad lleva horas caido se quedaria
// "Saludable" hasta que (si es que llega a hacerlo) volviera a conectar.
//
// Usa Backend.Seed, no Touch: Seed deriva el estado directamente de
// `lastSeen` frente a `timeout`, asi que un nodo ya caido desde antes del
// reinicio queda Unreachable desde el primer instante en vez de pasar
// brevemente por Online y disparar una alerta duplicada en el siguiente
// Sweep para algo que ya se sabia.
func seedHeartbeats(ctx context.Context, st store.Store, heartbeats heartbeat.Backend, now time.Time, timeout time.Duration, log *slog.Logger) {
	nodes, err := st.ListNodes(ctx)
	if err != nil {
		log.Warn("no se pudo precargar el heartbeat desde el almacen", "error", err)
		return
	}

	parallel.ForEach(ctx, seedConcurrency, nodes, func(n store.Node) {
		// Un nodo recien registrado que todavia no mando su primera rafaga
		// no tiene ningun heartbeat real que precargar. HasHeartbeat es la
		// unica senal que se usa para esto -no una comparacion de
		// timestamps (LastSeenAt lo marca el reloj del AGENTE, no el del
		// servidor: un agente con el reloj desincronizado rompería esa
		// comparacion, quiza para siempre). Si se sembrase aun asi, Seed lo
		// veria "vencido" desde el registro y lo marcaria Unreachable de
		// inmediato, y su primera metrica de verdad dispararia una alerta de
		// "recuperado" para un nodo que nunca estuvo caido.
		if !n.HasHeartbeat {
			return
		}
		if err := heartbeats.Seed(ctx, n.AgentID, n.LastSeenAt, now, timeout); err != nil {
			log.Warn("no se pudo inicializar el heartbeat de un nodo", "agent_id", n.AgentID, "error", err)
		}
	})
}

func buildAlerter(cfg *serverconfig.Config, log *slog.Logger) alert.Alerter {
	var telegram *alert.Telegram
	if cfg.TelegramBotToken != "" && cfg.TelegramChatID != "" {
		telegram = alert.NewTelegram(cfg.TelegramBotToken, cfg.TelegramChatID)
	}
	var discord *alert.Discord
	if cfg.DiscordWebhookURL != "" {
		discord = alert.NewDiscord(cfg.DiscordWebhookURL)
	}

	// alert.New descarta los canales nil, asi que aqui no hace falta
	// condicionar la lista: si ambos son nil, degrada solo a un Noop.
	channels := make([]alert.Channel, 0, 2)
	if telegram != nil {
		channels = append(channels, telegram)
	}
	if discord != nil {
		channels = append(channels, discord)
	}
	return alert.New(log, channels...)
}

func newGRPCServer(
	cfg *serverconfig.Config,
	st store.Store,
	watchdog *heartbeat.Watchdog,
	metricsHub *hub.Hub[*fleetpulsev1.MetricPayload],
	commands *commandbus.Bus,
	log *slog.Logger,
) (*grpc.Server, error) {
	validate := rpcauth.StaticValidator(cfg.Tokens)

	opts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(rpcauth.UnaryServerInterceptor(validate)),
		grpc.ChainStreamInterceptor(rpcauth.StreamServerInterceptor(validate)),
	}

	if cfg.TLSCertFile != "" {
		tlsConfig, err := buildServerTLSConfig(cfg)
		if err != nil {
			return nil, err
		}
		opts = append(opts, grpc.Creds(credentials.NewTLS(tlsConfig)))
	} else {
		log.Warn("gRPC sin TLS: usa --tls-cert/--tls-key en cualquier despliegue fuera de una LAN de confianza")
	}

	server := grpc.NewServer(opts...)
	fleetpulsev1.RegisterCollectorServiceServer(server, grpcserver.New(st, watchdog, metricsHub, commands, log))
	return server, nil
}

func buildServerTLSConfig(cfg *serverconfig.Config) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("cargar certificado TLS del servidor: %w", err)
	}
	tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}

	if cfg.TLSClientCAFile == "" {
		return tlsConfig, nil
	}

	caBytes, err := os.ReadFile(cfg.TLSClientCAFile)
	if err != nil {
		return nil, fmt.Errorf("leer CA de clientes: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, fmt.Errorf("la CA de clientes en %s no contiene certificados PEM validos", cfg.TLSClientCAFile)
	}
	tlsConfig.ClientCAs = pool
	tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	return tlsConfig, nil
}

// pruneLoop purga metricas antiguas periodicamente. En pgstore con
// TimescaleDB esto es un respaldo: la politica de retencion nativa (ver
// README) es la forma recomendada de operarlo en produccion.
func pruneLoop(ctx context.Context, st store.Store, retention time.Duration, log *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().UTC().Add(-retention)
			if err := st.PruneMetrics(ctx, cutoff); err != nil {
				log.Warn("no se pudo purgar el historico de metricas", "error", err)
			}
		}
	}
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}
