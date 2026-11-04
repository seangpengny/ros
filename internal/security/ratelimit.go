package security

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

type Limiter interface {
	Allow(string, time.Time) bool
}

type RateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	entries map[string][]time.Time
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{limit: limit, window: window, entries: make(map[string][]time.Time)}
}

func (r *RateLimiter) Allow(key string, now time.Time) bool {
	if r == nil || key == "" || r.limit <= 0 || r.window <= 0 {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := now.Add(-r.window)
	current := r.entries[key][:0]
	for _, timestamp := range r.entries[key] {
		if timestamp.After(cutoff) {
			current = append(current, timestamp)
		}
	}
	if len(current) >= r.limit {
		r.entries[key] = current
		return false
	}
	r.entries[key] = append(current, now)
	return true
}

type RedisRateLimiter struct {
	client   *redis.Client
	prefix   string
	limit    int
	window   time.Duration
	sequence atomic.Uint64
}

func NewRedisRateLimiter(client *redis.Client, prefix string, limit int, window time.Duration) *RedisRateLimiter {
	return &RedisRateLimiter{client: client, prefix: prefix, limit: limit, window: window}
}

func (r *RedisRateLimiter) Allow(key string, now time.Time) bool {
	if r == nil || r.client == nil || key == "" || r.limit <= 0 || r.window <= 0 {
		return false
	}
	const script = `
local key = KEYS[1]
redis.call('ZREMRANGEBYSCORE', key, '-inf', ARGV[2])
local count = redis.call('ZCARD', key)
if count >= tonumber(ARGV[3]) then
  return 0
end
redis.call('ZADD', key, ARGV[1], ARGV[5])
redis.call('PEXPIRE', key, ARGV[4])
return 1
`
	nowMillis := now.UnixMilli()
	member := fmt.Sprintf("%d-%d", now.UnixNano(), r.sequence.Add(1))
	result, err := r.client.Eval(context.Background(), script, []string{r.prefix + ":" + key}, nowMillis, nowMillis-r.window.Milliseconds(), r.limit, r.window.Milliseconds(), member).Int()
	return err == nil && result == 1
}
