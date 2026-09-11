package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Claves usadas en Redis. Un ZSET guarda el ultimo instante de cada agente
// (para poder pedir "quien lleva mas de X sin dar senales" con un rango de
// puntuacion) y un HASH guarda el estado actual, que es lo que de verdad
// gobierna si se dispara una alerta o no.
const (
	zsetKey = "fleetpulse:heartbeat:lastseen"
	hashKey = "fleetpulse:heartbeat:state"
)

// touchScript registra el heartbeat y devuelve el estado previo en una sola
// operacion atomica, para que dos rafagas casi simultaneas no dupliquen la
// alerta de "nodo recuperado".
const touchScript = `
local previous = redis.call('HGET', KEYS[2], ARGV[1])
redis.call('ZADD', KEYS[1], ARGV[2], ARGV[1])
redis.call('HSET', KEYS[2], ARGV[1], 'online')
if previous == false then
	return 'unknown'
end
return previous
`

// seedScript precarga el estado de un agente a partir de un last_seen_at ya
// conocido. Nunca hace retroceder la puntuacion existente en el ZSET (si ya
// hay una mas reciente -otra replica sigue viendo heartbeats de verdad- se
// conserva esa), y el estado resultante se deriva siempre de la puntuacion
// final frente al timeout, nunca se fuerza a 'online' a ciegas: asi un nodo
// que ya estaba caido antes del reinicio del servidor queda Unreachable
// desde el primer instante, sin pasar por una transicion que dispare una
// alerta duplicada en el siguiente Sweep.
const seedScript = `
local agent = ARGV[1]
local candidate = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local timeout = tonumber(ARGV[4])

local current = redis.call('ZSCORE', KEYS[1], agent)
local score = candidate
if current and tonumber(current) > candidate then
	score = tonumber(current)
end
redis.call('ZADD', KEYS[1], score, agent)

-- >= para que el limite coincida exactamente con el de sweepScript, que usa
-- ZRANGEBYSCORE('-inf', now-timeout) -- un rango inclusivo, no "> timeout".
local state = 'online'
if (now - score) >= timeout then
	state = 'unreachable'
end
redis.call('HSET', KEYS[2], agent, state)
return state
`

// sweepScript marca Unreachable a los agentes cuyo ultimo heartbeat supera el
// umbral y que seguian marcados online, devolviendo solo esos (no los que ya
// estaban Unreachable de un barrido anterior), para que el llamante dispare
// la alerta una unica vez por transicion.
const sweepScript = `
local stale = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
local result = {}
for _, agent in ipairs(stale) do
	local state = redis.call('HGET', KEYS[2], agent)
	if state == 'online' then
		redis.call('HSET', KEYS[2], agent, 'unreachable')
		table.insert(result, agent)
	end
end
return result
`

// RedisBackend implementa Backend sobre Redis, para compartir el estado de
// heartbeat entre varias replicas del servidor central.
type RedisBackend struct {
	client *redis.Client
}

// NewRedisBackend conecta con Redis a partir de una URL (redis://host:puerto/db).
func NewRedisBackend(ctx context.Context, redisURL string) (*RedisBackend, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parsear REDIS_URL: %w", err)
	}
	client := redis.NewClient(opts)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("ping a Redis: %w", err)
	}

	return &RedisBackend{client: client}, nil
}

func (b *RedisBackend) Close() error {
	return b.client.Close()
}

func (b *RedisBackend) Touch(ctx context.Context, agentID string, at time.Time) (State, error) {
	raw, err := b.client.Eval(ctx, touchScript, []string{zsetKey, hashKey}, agentID, at.UnixMilli()).Text()
	if err != nil {
		return StateUnknown, fmt.Errorf("touch heartbeat de %s: %w", agentID, err)
	}
	return parseState(raw), nil
}

func (b *RedisBackend) Sweep(ctx context.Context, timeout time.Duration, now time.Time) ([]string, error) {
	threshold := now.Add(-timeout).UnixMilli()
	agents, err := b.client.Eval(ctx, sweepScript, []string{zsetKey, hashKey}, threshold).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("barrido de heartbeat: %w", err)
	}
	return agents, nil
}

func (b *RedisBackend) State(ctx context.Context, agentID string) (State, error) {
	raw, err := b.client.HGet(ctx, hashKey, agentID).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return StateUnknown, nil
		}
		return StateUnknown, fmt.Errorf("leer estado de %s: %w", agentID, err)
	}
	return parseState(raw), nil
}

func (b *RedisBackend) Seed(ctx context.Context, agentID string, lastSeen, now time.Time, timeout time.Duration) error {
	_, err := b.client.Eval(ctx, seedScript, []string{zsetKey, hashKey},
		agentID, lastSeen.UnixMilli(), now.UnixMilli(), timeout.Milliseconds(),
	).Text()
	if err != nil {
		return fmt.Errorf("precargar heartbeat de %s: %w", agentID, err)
	}
	return nil
}

func parseState(raw string) State {
	switch raw {
	case "online":
		return StateOnline
	case "unreachable":
		return StateUnreachable
	default:
		return StateUnknown
	}
}
