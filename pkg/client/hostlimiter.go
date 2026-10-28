package client

import (
	"context"
	"sync"
)

// hostLimiter caps how many connections may be active to the SAME target host
// at once. Extra connections block in acquire until a slot frees or the context
// is cancelled, turning a burst to one site into a polite, paced stream so the
// site does not rate-limit/block the shared exit IP.
//
// Per-host semaphores are refcounted and removed when idle, so browsing many
// distinct hosts does not leak entries.
type hostLimiter struct {
	limit int

	mu    sync.Mutex
	hosts map[string]*hostSem
}

type hostSem struct {
	ch   chan struct{}
	refs int // waiters + holders; the entry is dropped when this hits zero
}

func newHostLimiter(limit int) *hostLimiter {
	return &hostLimiter{limit: limit, hosts: make(map[string]*hostSem)}
}

// acquire blocks until a slot for host is available or ctx is done. The returned
// release function must be called exactly once to free the slot.
func (h *hostLimiter) acquire(ctx context.Context, host string) (func(), error) {
	if h.limit <= 0 {
		return func() {}, nil // limiting disabled
	}

	h.mu.Lock()
	e := h.hosts[host]
	if e == nil {
		e = &hostSem{ch: make(chan struct{}, h.limit)}
		h.hosts[host] = e
	}
	e.refs++
	h.mu.Unlock()

	release := func() {
		<-e.ch
		h.drop(host, e)
	}

	select {
	case e.ch <- struct{}{}:
		return release, nil
	case <-ctx.Done():
		h.drop(host, e) // gave up waiting; never took a slot
		return nil, ctx.Err()
	}
}

func (h *hostLimiter) drop(host string, e *hostSem) {
	h.mu.Lock()
	if e.refs--; e.refs == 0 {
		delete(h.hosts, host)
	}
	h.mu.Unlock()
}
