// Package pgstore implementa store.Store sobre PostgreSQL/TimescaleDB usando
// pgx. Es el backend de produccion: persiste entre reinicios del servidor y,
// con la extension timescaledb instalada, comprime y expira el historico de
// metricas automaticamente.
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gdev/fleetpulse/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store implementa store.Store contra un pool de conexiones pgx.
type Store struct {
	pool *pgxpool.Pool
}

// Open conecta con la base de datos, la comprueba con un ping y aplica el
// esquema (ver schema.go). Fallar aqui debe abortar el arranque del
// servidor: sin base de datos no hay donde persistir nada.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("crear pool de conexiones: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping a la base de datos: %w", err)
	}

	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	for i, stmt := range statements {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("migracion #%d: %w", i, err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	s.pool.Close()
	return nil
}

func (s *Store) UpsertNode(ctx context.Context, node store.Node) error {
	const q = `
		INSERT INTO nodes (
			agent_id, hostname, os, platform, platform_version, kernel_version,
			arch, local_ip, public_ip, agent_version, cpu_cores, memory_total_bytes, boot_time
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (agent_id) DO UPDATE SET
			hostname = EXCLUDED.hostname,
			os = EXCLUDED.os,
			platform = EXCLUDED.platform,
			platform_version = EXCLUDED.platform_version,
			kernel_version = EXCLUDED.kernel_version,
			arch = EXCLUDED.arch,
			local_ip = EXCLUDED.local_ip,
			public_ip = EXCLUDED.public_ip,
			agent_version = EXCLUDED.agent_version,
			cpu_cores = EXCLUDED.cpu_cores,
			memory_total_bytes = EXCLUDED.memory_total_bytes,
			boot_time = EXCLUDED.boot_time
		-- registered_at, last_seen_at y has_heartbeat no se tocan: los
		-- gobiernan el primer INSERT (con su DEFAULT) y TouchNode
		-- respectivamente. has_heartbeat es la unica fuente de verdad de "hay
		-- un heartbeat real que precargar" que usa
		-- cmd/fleetpulse-server.seedHeartbeats -no una comparacion de
		-- timestamps, que se rompe si el reloj del agente esta desincronizado
		-- del servidor.
	`
	_, err := s.pool.Exec(ctx, q,
		node.AgentID, node.Hostname, node.OS, node.Platform, node.PlatformVersion, node.KernelVersion,
		node.Arch, node.LocalIP, node.PublicIP, node.AgentVersion, node.CPUCores, node.MemoryTotal, node.BootTime,
	)
	if err != nil {
		return fmt.Errorf("upsert nodo %s: %w", node.AgentID, err)
	}
	return nil
}

const nodeColumns = `agent_id, hostname, os, platform, platform_version, kernel_version,
	arch, local_ip, public_ip, agent_version, cpu_cores, memory_total_bytes, boot_time,
	registered_at, last_seen_at, has_heartbeat`

func (s *Store) GetNode(ctx context.Context, agentID string) (store.Node, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+nodeColumns+` FROM nodes WHERE agent_id = $1`, agentID)
	if err != nil {
		return store.Node{}, fmt.Errorf("consultar nodo %s: %w", agentID, err)
	}
	defer rows.Close()

	node, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[store.Node])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.Node{}, store.ErrNotFound
		}
		return store.Node{}, fmt.Errorf("leer nodo %s: %w", agentID, err)
	}
	return node, nil
}

func (s *Store) ListNodes(ctx context.Context) ([]store.Node, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+nodeColumns+` FROM nodes ORDER BY hostname`)
	if err != nil {
		return nil, fmt.Errorf("listar nodos: %w", err)
	}
	defer rows.Close()

	nodes, err := pgx.CollectRows(rows, pgx.RowToStructByPos[store.Node])
	if err != nil {
		return nil, fmt.Errorf("leer nodos: %w", err)
	}
	return nodes, nil
}

func (s *Store) TouchNode(ctx context.Context, agentID string, at time.Time) error {
	tag, err := s.pool.Exec(ctx, `UPDATE nodes SET last_seen_at = $2, has_heartbeat = true WHERE agent_id = $1`, agentID, at)
	if err != nil {
		return fmt.Errorf("actualizar heartbeat de %s: %w", agentID, err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) InsertMetric(ctx context.Context, point store.MetricPoint) error {
	const q = `
		INSERT INTO metrics (
			agent_id, time, cpu_usage_percent, memory_used_bytes, memory_total_bytes,
			disk_usage_percent, rx_bytes_per_second, tx_bytes_per_second, load1
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (agent_id, time) DO NOTHING
	`
	_, err := s.pool.Exec(ctx, q,
		point.AgentID, point.Timestamp, point.CPUUsagePercent, point.MemoryUsedBytes, point.MemoryTotalBytes,
		point.DiskUsagePercent, point.RxBytesPerSecond, point.TxBytesPerSecond, point.Load1,
	)
	if err != nil {
		return fmt.Errorf("insertar metrica de %s: %w", point.AgentID, err)
	}
	return nil
}

const metricColumns = `agent_id, time, cpu_usage_percent, memory_used_bytes, memory_total_bytes,
	disk_usage_percent, rx_bytes_per_second, tx_bytes_per_second, load1`

func (s *Store) LatestMetric(ctx context.Context, agentID string) (store.MetricPoint, error) {
	const q = `SELECT ` + metricColumns + ` FROM metrics WHERE agent_id = $1 ORDER BY time DESC LIMIT 1`
	rows, err := s.pool.Query(ctx, q, agentID)
	if err != nil {
		return store.MetricPoint{}, fmt.Errorf("consultar ultima metrica de %s: %w", agentID, err)
	}
	defer rows.Close()

	point, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[store.MetricPoint])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.MetricPoint{}, store.ErrNotFound
		}
		return store.MetricPoint{}, fmt.Errorf("leer ultima metrica de %s: %w", agentID, err)
	}
	return point, nil
}

func (s *Store) QueryRange(ctx context.Context, agentID string, from, to time.Time) ([]store.MetricPoint, error) {
	const q = `SELECT ` + metricColumns + ` FROM metrics
		WHERE agent_id = $1 AND time BETWEEN $2 AND $3
		ORDER BY time ASC`
	rows, err := s.pool.Query(ctx, q, agentID, from, to)
	if err != nil {
		return nil, fmt.Errorf("consultar rango de %s: %w", agentID, err)
	}
	defer rows.Close()

	points, err := pgx.CollectRows(rows, pgx.RowToStructByPos[store.MetricPoint])
	if err != nil {
		return nil, fmt.Errorf("leer rango de %s: %w", agentID, err)
	}
	return points, nil
}

func (s *Store) PruneMetrics(ctx context.Context, before time.Time) error {
	// Con TimescaleDB la retencion real la fija una compression/retention
	// policy nativa (ver README, seccion de operacion); esta consulta cubre
	// el caso de PostgreSQL puro, donde no hay tal politica automatica.
	if _, err := s.pool.Exec(ctx, `DELETE FROM metrics WHERE time < $1`, before); err != nil {
		return fmt.Errorf("purgar metricas anteriores a %s: %w", before, err)
	}
	return nil
}

const containerColumns = `agent_id, container_id, name, image, status, state,
	cpu_percent, memory_bytes, memory_limit_bytes, started_at, restart_count,
	rx_bytes, tx_bytes, updated_at`

func (s *Store) SetContainers(ctx context.Context, agentID string, containers []store.Container) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("abrir transaccion de contenedores de %s: %w", agentID, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op si ya hubo Commit

	ids := make([]string, 0, len(containers))
	for _, c := range containers {
		ids = append(ids, c.ID)
	}

	// Los contenedores que ya no aparecen en la rafaga (se borraron o el
	// engine los perdio de vista) se retiran del snapshot.
	if _, err := tx.Exec(ctx,
		`DELETE FROM containers WHERE agent_id = $1 AND NOT (container_id = ANY($2))`,
		agentID, ids,
	); err != nil {
		return fmt.Errorf("limpiar contenedores obsoletos de %s: %w", agentID, err)
	}

	const upsert = `
		INSERT INTO containers (` + containerColumns + `)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (agent_id, container_id) DO UPDATE SET
			name = EXCLUDED.name, image = EXCLUDED.image, status = EXCLUDED.status,
			state = EXCLUDED.state, cpu_percent = EXCLUDED.cpu_percent,
			memory_bytes = EXCLUDED.memory_bytes, memory_limit_bytes = EXCLUDED.memory_limit_bytes,
			started_at = EXCLUDED.started_at, restart_count = EXCLUDED.restart_count,
			rx_bytes = EXCLUDED.rx_bytes, tx_bytes = EXCLUDED.tx_bytes, updated_at = EXCLUDED.updated_at
	`
	now := time.Now().UTC()
	for _, c := range containers {
		if _, err := tx.Exec(ctx, upsert,
			agentID, c.ID, c.Name, c.Image, c.Status, c.State,
			c.CPUPercent, c.MemoryBytes, c.MemoryLimit, c.StartedAt, c.RestartCount,
			c.RxBytes, c.TxBytes, now,
		); err != nil {
			return fmt.Errorf("upsert contenedor %s de %s: %w", c.ID, agentID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("confirmar contenedores de %s: %w", agentID, err)
	}
	return nil
}

func (s *Store) ListContainers(ctx context.Context, agentID string) ([]store.Container, error) {
	const q = `SELECT ` + containerColumns + ` FROM containers WHERE agent_id = $1 ORDER BY name`
	rows, err := s.pool.Query(ctx, q, agentID)
	if err != nil {
		return nil, fmt.Errorf("listar contenedores de %s: %w", agentID, err)
	}
	defer rows.Close()

	containers, err := pgx.CollectRows(rows, pgx.RowToStructByPos[store.Container])
	if err != nil {
		return nil, fmt.Errorf("leer contenedores de %s: %w", agentID, err)
	}
	return containers, nil
}
