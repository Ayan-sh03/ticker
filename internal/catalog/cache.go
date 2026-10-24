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
	// A stale snapshot is kept this long so an event that has not changed can
	// reuse its encoded bytes instead of compressing them again.
	seatKeepStale = time.Minute
)

type seatCache struct {
	mu      sync.RWMutex
	entries map[string]seatEntry
	group   singleflight.Group
}

type seatEntry struct {
	seatMap *SeatMap
	expires time.Time
}

func newSeatCache() *seatCache {
	return &seatCache{entries: make(map[string]seatEntry)}
}

// get returns the event's fresh snapshot, or fetches a new one. fetch receives
// the previous snapshot, if any, so it can reuse it when nothing changed.
func (c *seatCache) get(ctx context.Context, eventID string, fetch func(context.Context, *SeatMap) (*SeatMap, error)) (*SeatMap, error) {
	if seatMap, fresh := c.lookup(eventID); fresh {
		return seatMap, nil
	}
	result := c.group.DoChan(eventID, func() (any, error) {
		previous, fresh := c.lookup(eventID)
		if fresh {
			return previous, nil
		}
		// Callers share this fetch, so the first one leaving must not cancel it
		// for the rest. Each caller still stops waiting at its own deadline.
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), seatFetchTimeout)
		defer cancel()
		seatMap, err := fetch(fetchCtx, previous)
		if err != nil {
			return nil, err
		}
		if seatMap != nil {
			c.store(eventID, seatMap)
		}
		return seatMap, nil
	})
	select {
	case r := <-result:
		if r.Err != nil {
			return nil, r.Err
		}
		return r.Val.(*SeatMap), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// lookup returns the event's last snapshot, if any, and whether it is fresh.
func (c *seatCache) lookup(eventID string) (*SeatMap, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[eventID]
	if !ok {
		return nil, false
	}
	return entry.seatMap, time.Now().Before(entry.expires)
}

func (c *seatCache) store(eventID string, seatMap *SeatMap) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		if now.Sub(entry.expires) > seatKeepStale {
			delete(c.entries, key)
		}
	}
	c.entries[eventID] = seatEntry{seatMap: seatMap, expires: now.Add(seatCacheTTL)}
}
