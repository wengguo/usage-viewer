package concurrency

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/config"
)

// Redis key prefixes, mirrored from the Sub2API gateway concurrency tracking.
const (
	apiKeySlotKeyPrefix      = "concurrency:api_key:"
	liveAPIKeySlotKeyPrefix  = "concurrency:live:api_key:"
	slotTTLSeconds     int64 = 900 // 15-minute request slot window
	liveLeaseTTLSeconds int64 = 60 // live lease window
)

// Resolver reads current in-flight concurrency counts for API keys from Redis.
type Resolver struct {
	client *redis.Client
}

// NewResolver builds a resolver from configuration. It returns nil when no
// Redis host is configured, meaning concurrency cannot be resolved.
func NewResolver(cfg config.Config) *Resolver {
	if cfg.RedisHost == "" {
		return nil
	}
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.RedisHost, cfg.RedisPort),
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	return &Resolver{client: client}
}

// Resolve returns the in-flight concurrency count for each API key. Counts are
// best-effort: keys with no Redis data (or on Redis failure) map to 0.
func (r *Resolver) Resolve(ctx context.Context, apiKeyIDs []int64) map[int64]int64 {
	result := make(map[int64]int64, len(apiKeyIDs))
	if r == nil || r.client == nil || len(apiKeyIDs) == 0 {
		return result
	}
	for _, id := range apiKeyIDs {
		result[id] = 0
	}

	now, err := r.client.Time(ctx).Result()
	if err != nil {
		return result
	}
	slotCutoff := now.Unix() - slotTTLSeconds
	liveCutoff := now.Unix() - liveLeaseTTLSeconds

	pipe := r.client.Pipeline()
	type zcountCmd struct {
		id  int64
		cmd *redis.IntCmd
	}
	var cmds []zcountCmd
	for _, id := range apiKeyIDs {
		slotKey := apiKeySlotKeyPrefix + strconv.FormatInt(id, 10)
		liveKey := liveAPIKeySlotKeyPrefix + strconv.FormatInt(id, 10)
		cmds = append(cmds,
			zcountCmd{id: id, cmd: pipe.ZCount(ctx, slotKey, strconv.FormatInt(slotCutoff, 10), "+inf")},
			zcountCmd{id: id, cmd: pipe.ZCount(ctx, liveKey, strconv.FormatInt(liveCutoff, 10), "+inf")},
		)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return result
	}
	for _, cmd := range cmds {
		result[cmd.id] += cmd.cmd.Val()
	}
	return result
}

// Close closes the underlying Redis client, if any.
func (r *Resolver) Close() {
	if r != nil && r.client != nil {
		_ = r.client.Close()
	}
}
