// Package ratelimit rejects excess requests before they reach PostgreSQL.
//
// Writes are counted in Redis so every API instance shares one budget. Reads are
// high-volume polling, so they are counted in process memory to keep them fast
// and off the Redis quota. If Redis fails, writes fall back to memory limits
// instead of failing open or taking the site down.
package ratelimit

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/redis/go-redis/v9"
)

var (
	limitedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ticker_rate_limited_total", Help: "Requests rejected by a rate limit.",
	}, []string{"rule"})
	fallbackTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ticker_rate_limit_fallback_total", Help: "Shared limit checks answered from memory after a Redis failure.",
	})
)

// key is one counter: a rule's budget for one client, account, or network.
type key struct {
	rule   string
	id     string
	limit  int
	window time.Duration
}

func (k key) name(now time.Time) (string, time.Time) {
	slot := now.UnixNano() / int64(k.window)
	return "rl:" + k.rule + ":" + k.id + ":" + strconv.FormatInt(slot, 10), time.Unix(0, (slot+1)*int64(k.window))
}

type Limiter struct {
	redis  *redis.Client
	local  *memory
	logger *slog.Logger
	secret []byte
	ipHead string
	secure bool
	// skipUntil pauses Redis calls after a failure so requests do not each wait
	// for a timeout while Redis is down.
	skipUntil atomic.Int64
}

// New returns a limiter. A nil Redis client keeps every count in memory, which is
// exact for a single API process.
func New(client *redis.Client, secret []byte, clientIPHeader string, secureCookie bool, logger *slog.Logger) *Limiter {
	return &Limiter{redis: client, local: newMemory(), logger: logger, secret: secret, ipHead: clientIPHeader, secure: secureCookie}
}

// allow counts the request against every key and reports how long the caller
// must wait when any key is over its limit.
func (l *Limiter) allow(ctx context.Context, shared bool, keys []key) (time.Duration, bool) {
	now := time.Now()
	if shared && l.redis != nil && now.UnixNano() >= l.skipUntil.Load() {
		// Keys Redis already rejected stay rejected here until their window ends,
		// so a flood from one client costs one Redis call per instance per window.
		if wait, denied := l.local.denied(keys, now); denied {
			limitedTotal.WithLabelValues(keys[0].rule).Inc()
			return wait, false
		}
		counts, err := l.count(ctx, keys, now)
		if err == nil {
			return l.decide(keys, counts, now, true)
		}
		l.skipUntil.Store(now.Add(5 * time.Second).UnixNano())
		l.logger.Warn("redis failed; shared rate limits use memory for 5s", "error", err)
	}
	if shared && l.redis != nil {
		fallbackTotal.Inc()
	}
	return l.decide(keys, l.local.incr(keys, now), now, false)
}

// count increments fixed-window counters in one round trip. SET NX EX creates a
// counter with its expiry, so INCR never leaves a key without a TTL.
func (l *Limiter) count(ctx context.Context, keys []key, now time.Time) ([]int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	pipe := l.redis.Pipeline()
	incrs := make([]*redis.IntCmd, len(keys))
	for i, k := range keys {
		name, end := k.name(now)
		pipe.SetNX(ctx, name, 0, time.Until(end)+time.Second)
		incrs[i] = pipe.Incr(ctx, name)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	counts := make([]int64, len(keys))
	for i, cmd := range incrs {
		counts[i] = cmd.Val()
	}
	return counts, nil
}

func (l *Limiter) decide(keys []key, counts []int64, now time.Time, remember bool) (time.Duration, bool) {
	var wait time.Duration
	for i, k := range keys {
		if counts[i] <= int64(k.limit) {
			continue
		}
		_, end := k.name(now)
		if remember {
			l.local.deny(k, now, end)
		}
		limitedTotal.WithLabelValues(k.rule).Inc()
		wait = max(wait, end.Sub(now))
	}
	return wait, wait == 0
}

// memory holds per-process counters and recently denied shared keys.
type memory struct {
	mu     sync.Mutex
	counts map[string]counter
	until  map[string]time.Time
	swept  time.Time
}

type counter struct {
	n   int64
	end time.Time
}

func newMemory() *memory {
	return &memory{counts: map[string]counter{}, until: map[string]time.Time{}}
}

func (m *memory) incr(keys []key, now time.Time) []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweep(now)
	counts := make([]int64, len(keys))
	for i, k := range keys {
		name, end := k.name(now)
		c := m.counts[name]
		c.n, c.end = c.n+1, end
		m.counts[name] = c
		counts[i] = c.n
	}
	return counts
}

func (m *memory) deny(k key, now, end time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	name, _ := k.name(now)
	m.until[name] = end
}

func (m *memory) denied(keys []key, now time.Time) (time.Duration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var wait time.Duration
	for _, k := range keys {
		name, _ := k.name(now)
		if end, ok := m.until[name]; ok && now.Before(end) {
			wait = max(wait, end.Sub(now))
		}
	}
	return wait, wait > 0
}

// sweep drops counters from finished windows about once a minute.
func (m *memory) sweep(now time.Time) {
	if now.Sub(m.swept) < time.Minute {
		return
	}
	m.swept = now
	for name, c := range m.counts {
		if !now.Before(c.end) {
			delete(m.counts, name)
		}
	}
	for name, end := range m.until {
		if !now.Before(end) {
			delete(m.until, name)
		}
	}
}
