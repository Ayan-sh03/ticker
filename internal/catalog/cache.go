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
const seatCacheTTL = 2 * time.Second

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
	value, err, _ := c.group.Do(eventID, func() (any, error) {
		if seats, ok := c.lookup(eventID); ok {
			return seats, nil
		}
		seats, err := fetch(ctx)
		if err != nil {
			return nil, err
		}
		if len(seats) > 0 {
			c.store(eventID, seats)
		}
		return seats, nil
	})
	if err != nil {
		return nil, err
	}
	return value.([]Seat), nil
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
