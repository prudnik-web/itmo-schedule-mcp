// Package limit implements a bounded fixed-window limiter. Full maps reject new
// keys instead of evicting live counters and allowing rate limits to be bypassed.
package limit

import (
	"sync"
	"time"
)

type counter struct {
	start time.Time
	count int
}

type Window struct {
	mu                sync.Mutex
	entries           map[string]counter
	maximum, capacity int
	period            time.Duration
}

func New(maximum int, period time.Duration, capacity int) *Window {
	if maximum <= 0 || period <= 0 || capacity <= 0 {
		panic("invalid rate limit")
	}
	return &Window{entries: make(map[string]counter), maximum: maximum, period: period, capacity: capacity}
}

func (w *Window) Allow(key string, now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	c, exists := w.entries[key]
	if !exists && len(w.entries) >= w.capacity {
		for k, old := range w.entries {
			if !now.Before(old.start.Add(w.period)) {
				delete(w.entries, k)
			}
		}
		if len(w.entries) >= w.capacity {
			return false
		}
	}
	if !exists || !now.Before(c.start.Add(w.period)) {
		c = counter{start: now}
	}
	if c.count >= w.maximum {
		return false
	}
	c.count++
	w.entries[key] = c
	return true
}

func (w *Window) Sweep(now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for key, c := range w.entries {
		if !now.Before(c.start.Add(w.period)) {
			delete(w.entries, key)
		}
	}
}
