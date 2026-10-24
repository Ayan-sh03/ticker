package catalog

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// seatCache keeps short-lived availability snapshots for published events so
// seat-map reads do not reach the database on every poll. It only ever caches
// read results: ownership is still decided by reserve and confirm under row
// locks, so a stale snapshot can misreport availability but never ownership.
const (
	seatCacheTTL     = 2 * time.Second
	seatFetchTimeout = 3 * time.Second
)

type seatCache struct {
	mu      sync.RWMutex
	entries map[string]seatEntry
	group   singleflight.Group
}

type seatEntry struct {
	seats   []Seat
	expires time.Time
}

func newSeatCache() *seatCache {
	return &seatCache{entries: make(map[string]seatEntry)}
}

func (c *seatCache) get(ctx context.Context, eventID string, fetch func(context.Context) ([]Seat, error)) ([]Seat, error) {
	if seats, ok := c.lookup(eventID); ok {
		return seats, nil
	}
	result := c.group.DoChan(eventID, func() (any, error) {
		if seats, ok := c.lookup(eventID); ok {
			return seats, nil
		}
		// Callers share this fetch, so the first one leaving must not cancel it
		// for the rest. Each caller still stops waiting at its own deadline.
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), seatFetchTimeout)
		defer cancel()
		seats, err := fetch(fetchCtx)
		if err != nil {
			return nil, err
		}
		if len(seats) > 0 {
			c.store(eventID, seats)
		}
		return seats, nil
	})
	select {
	case r := <-result:
		if r.Err != nil {
			return nil, r.Err
		}
		return r.Val.([]Seat), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *seatCache) lookup(eventID string) ([]Seat, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[eventID]
	if !ok || !time.Now().Before(entry.expires) {
		return nil, false
	}
	return entry.seats, true
}

func (c *seatCache) store(eventID string, seats []Seat) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		if now.After(entry.expires) {
			delete(c.entries, key)
		}
	}
	c.entries[eventID] = seatEntry{seats: seats, expires: now.Add(seatCacheTTL)}
}
