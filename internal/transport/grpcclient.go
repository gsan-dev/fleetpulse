// Package transport es el lado cliente del contrato gRPC: registra el nodo y
// mantiene abierto el stream de metricas hacia el recolector central,
// reabriendolo cuando la conexion se cae.
package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"

	fleetpulsev1 "github.com/gdev/fleetpulse/gen/fleetpulse/v1"
	"github.com/gdev/fleetpulse/internal/rpcauth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// TLSFiles agrupa las rutas de certificados que activan TLS/mTLS en el
// cliente. Todos vacios => conexion en texto plano.
type TLSFiles struct {
	CAFile   string
	CertFile string
	KeyFile  string
}

// BuildTLSConfig construye la configuracion TLS del cliente a partir de las
// rutas de fichero, o nil si no se configuro ninguna (conexion en texto plano).
func BuildTLSConfig(files TLSFiles) (*tls.Config, error) {
	if files.CAFile == "" && files.CertFile == "" {
		return nil, nil
	}

	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}

	if files.CAFile != "" {
		caBytes, err := os.ReadFile(files.CAFile)
		if err != nil {
			return nil, fmt.Errorf("leer CA del servidor: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caBytes) {
			return nil, fmt.Errorf("%s no contiene certificados PEM validos", files.CAFile)
		}
		tlsConfig.RootCAs = pool
	}

	if files.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(files.CertFile, files.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("cargar certificado de cliente (mTLS): %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return tlsConfig, nil
}

// Client mantiene la conexion gRPC y el stream de metricas activo.
type Client struct {
	conn    *grpc.ClientConn
	rpc     fleetpulsev1.CollectorServiceClient
	token   string
	agentID string
	log     *slog.Logger

	stream grpc.ClientStreamingClient[fleetpulsev1.MetricPayload, fleetpulsev1.PushMetricsAck]
}

// Dial abre la conexion gRPC. No bloquea a la espera de que el servidor
// responda (grpc.NewClient es perezoso): los fallos de red se ven en la
// primera llamada real (Register o Send).
func Dial(serverAddr, token, agentID string, tlsConfig *tls.Config, log *slog.Logger) (*Client, error) {
	creds := insecure.NewCredentials()
	if tlsConfig != nil {
		creds = credentials.NewTLS(tlsConfig)
	}

	conn, err := grpc.NewClient(serverAddr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("conectar con %s: %w", serverAddr, err)
	}

	return &Client{
		conn:    conn,
		rpc:     fleetpulsev1.NewCollectorServiceClient(conn),
		token:   token,
		agentID: agentID,
		log:     log,
	}, nil
}

// RPC expone el cliente gRPC generado, para que internal/commands pueda usar
// StreamCommands y ReportCommandResult sin que este paquete tenga que
// reexportar cada metodo una a una.
func (c *Client) RPC() fleetpulsev1.CollectorServiceClient { return c.rpc }

// AgentID y Token se necesitan tal cual en internal/commands para autenticar
// y suscribirse al canal de comandos con las mismas credenciales.
func (c *Client) AgentID() string { return c.agentID }
func (c *Client) Token() string   { return c.token }

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) authContext(ctx context.Context) context.Context {
	return rpcauth.OutgoingContext(ctx, c.token)
}

// Register da de alta (o refresca) el nodo. Se llama al arrancar y cada vez
// que reabrir el stream de metricas falla tras agotar reintentos: si el
// servidor perdio el estado (reinicio en modo memoria), un nuevo Register
// lo recupera sin intervencion manual.
func (c *Client) Register(ctx context.Context, node *fleetpulsev1.NodeInfo) (*fleetpulsev1.RegisterResponse, error) {
	resp, err := c.rpc.Register(c.authContext(ctx), &fleetpulsev1.RegisterRequest{
		Token:   c.token,
		AgentId: c.agentID,
		Node:    node,
	})
	if err != nil {
		return nil, fmt.Errorf("registrar nodo: %w", err)
	}
	return resp, nil
}

// Send transmite una muestra. Abre el stream de PushMetrics la primera vez
// que se llama (o tras un fallo previo) y lo reutiliza en las siguientes
// llamadas: mientras el agente siga vivo, es un unico stream de larga
// duracion, tal como documenta el .proto.
func (c *Client) Send(ctx context.Context, payload *fleetpulsev1.MetricPayload) error {
	if c.stream == nil {
		stream, err := c.rpc.PushMetrics(c.authContext(ctx))
		if err != nil {
			return fmt.Errorf("abrir stream de metricas: %w", err)
		}
		c.stream = stream
	}

	if err := c.stream.Send(payload); err != nil {
		c.stream = nil // se reabrira en el siguiente intento
		return fmt.Errorf("enviar metricas: %w", err)
	}
	return nil
}

// CloseStream cierra el stream de metricas actual, avisando al servidor de
// que no llegaran mas rafagas (se usa al parar el agente ordenadamente).
func (c *Client) CloseStream() {
	if c.stream == nil {
		return
	}
	if ack, err := c.stream.CloseAndRecv(); err == nil {
		c.log.Debug("stream de metricas cerrado", "aceptadas", ack.GetAcceptedPayloads())
	}
	c.stream = nil
}
