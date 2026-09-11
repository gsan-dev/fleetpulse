package pgstore

// statements es el esquema inicial, aplicado en orden al arrancar el
// servidor. Se ejecuta cada vez (todo con IF NOT EXISTS / ON CONFLICT) en
// lugar de depender de una herramienta externa de migraciones: para un
// proyecto self-hosted de un solo binario, que el propio servidor deje la
// base de datos lista es mas simple de operar que anadir `golang-migrate` a
// la lista de dependencias del despliegue.
//
// El esquema funciona tanto contra un PostgreSQL normal como contra
// TimescaleDB: la conversion de `metrics` en hypertable solo se dispara si la
// extension esta instalada (bloque DO al final), asi que degradar a Postgres
// puro para pruebas locales no rompe el arranque.
var statements = []string{
	`CREATE TABLE IF NOT EXISTS nodes (
		agent_id            TEXT PRIMARY KEY,
		hostname            TEXT NOT NULL,
		os                  TEXT NOT NULL,
		platform            TEXT NOT NULL,
		platform_version    TEXT NOT NULL,
		kernel_version      TEXT NOT NULL,
		arch                TEXT NOT NULL,
		local_ip            TEXT NOT NULL,
		public_ip           TEXT NOT NULL,
		agent_version       TEXT NOT NULL,
		cpu_cores           INTEGER NOT NULL,
		memory_total_bytes  BIGINT NOT NULL,
		boot_time           TIMESTAMPTZ NOT NULL,
		registered_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
		last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,

	`CREATE TABLE IF NOT EXISTS metrics (
		agent_id             TEXT NOT NULL REFERENCES nodes(agent_id) ON DELETE CASCADE,
		time                 TIMESTAMPTZ NOT NULL,
		cpu_usage_percent    REAL NOT NULL,
		memory_used_bytes    BIGINT NOT NULL,
		memory_total_bytes   BIGINT NOT NULL,
		disk_usage_percent   REAL NOT NULL,
		rx_bytes_per_second  BIGINT NOT NULL,
		tx_bytes_per_second  BIGINT NOT NULL,
		load1                REAL NOT NULL,
		PRIMARY KEY (agent_id, time)
	)`,

	`CREATE INDEX IF NOT EXISTS idx_metrics_agent_time ON metrics (agent_id, time DESC)`,

	// create_hypertable exige que la tabla este vacia de indices propios de
	// hypertable previos; if_not_exists la hace idonea para reintentarse en
	// cada arranque sin fallar si ya se aplico.
	`DO $$
	BEGIN
		IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb') THEN
			PERFORM create_hypertable('metrics', 'time', if_not_exists => TRUE, migrate_data => TRUE);
		END IF;
	END $$`,

	`CREATE TABLE IF NOT EXISTS containers (
		agent_id            TEXT NOT NULL REFERENCES nodes(agent_id) ON DELETE CASCADE,
		container_id        TEXT NOT NULL,
		name                TEXT NOT NULL,
		image               TEXT NOT NULL,
		status              TEXT NOT NULL,
		state               TEXT NOT NULL,
		cpu_percent         REAL NOT NULL,
		memory_bytes        BIGINT NOT NULL,
		memory_limit_bytes  BIGINT NOT NULL,
		started_at          TIMESTAMPTZ NOT NULL,
		restart_count       INTEGER NOT NULL DEFAULT 0,
		rx_bytes            BIGINT NOT NULL,
		tx_bytes            BIGINT NOT NULL,
		updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
		PRIMARY KEY (agent_id, container_id)
	)`,
}
