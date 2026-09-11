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
