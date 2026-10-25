package limit

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBoundedCountersAndReset(t *testing.T) {
	w := New(2, time.Minute, 1)
	now := time.Now()
	if !w.Allow("a", now) || !w.Allow("a", now) || w.Allow("a", now) || w.Allow("b", now) {
		t.Fatal("limit or capacity bypassed")
	}
	if !w.Allow("b", now.Add(time.Minute)) {
		t.Fatal("expired counter blocked new key")
	}
	w.Sweep(now.Add(2 * time.Minute))
	if len(w.entries) != 0 {
		t.Fatal("expired counter retained")
	}
}

func TestConcurrentLimit(t *testing.T) {
	w := New(5, time.Minute, 10)
	var allowed atomic.Int32
	var group sync.WaitGroup
	for range 50 {
		group.Go(func() {
			if w.Allow("a", time.Now()) {
				allowed.Add(1)
			}
		})
	}
	group.Wait()
	if allowed.Load() != 5 {
		t.Fatal("concurrent requests exceeded limit")
	}
}
