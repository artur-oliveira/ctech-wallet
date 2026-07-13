package ws

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

const channelPrefix = "ws:"

// RedisRegistry fans out WebSocket messages across all API instances via Redis
// (Valkey) Pub/Sub. Each instance holds local connections; Redis is the
// fan-out bus.
type RedisRegistry struct {
	client   *redis.Client
	local    *MemoryRegistry
	pubsub   *redis.PubSub
	cancelFn context.CancelFunc
}

func NewRedisRegistry(client *redis.Client) *RedisRegistry {
	return &RedisRegistry{
		client: client,
		local:  NewMemoryRegistry(),
	}
}

func (r *RedisRegistry) Start(ctx context.Context) error {
	listenCtx, cancel := context.WithCancel(ctx)
	r.cancelFn = cancel
	r.pubsub = r.client.PSubscribe(listenCtx, channelPrefix+"*")
	go r.listen(listenCtx)
	slog.Info("RedisRegistry started")
	return nil
}

func (r *RedisRegistry) Stop(_ context.Context) error {
	if r.cancelFn != nil {
		r.cancelFn()
	}
	if r.pubsub != nil {
		_ = r.pubsub.Close()
	}
	slog.Info("RedisRegistry stopped")
	return nil
}

func (r *RedisRegistry) Register(userID, connID string, conn Conn) {
	r.local.Register(userID, connID, conn)
}

func (r *RedisRegistry) Unregister(userID, connID string) {
	r.local.Unregister(userID, connID)
}

// Broadcast publishes to Redis; the listener task delivers to local connections.
func (r *RedisRegistry) Broadcast(ctx context.Context, userID string, payload []byte) {
	ch := channelPrefix + userID
	if err := r.client.Publish(ctx, ch, payload).Err(); err != nil {
		slog.Error("redis publish failed, falling back to local", "user", userID, "err", err)
		r.local.Broadcast(ctx, userID, payload)
	}
}

func (r *RedisRegistry) listen(ctx context.Context) {
	retryDelay := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		ch := r.pubsub.Channel()
		for msg := range ch {
			retryDelay = time.Second
			userID := msg.Channel[len(channelPrefix):]
			r.local.Broadcast(ctx, userID, []byte(msg.Payload))
		}

		select {
		case <-ctx.Done():
			return
		default:
			slog.Warn("redis pubsub channel closed, reconnecting", "delay", retryDelay)
			time.Sleep(retryDelay)
			retryDelay = min(retryDelay*2, 60*time.Second)
			r.pubsub = r.client.PSubscribe(ctx, fmt.Sprintf("%s*", channelPrefix))
		}
	}
}
