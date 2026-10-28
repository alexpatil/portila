package client

import (
	"context"
	"testing"
	"time"
)

func TestHostLimiter(t *testing.T) {
	limiter := newHostLimiter(2)

	// Acquire slot 1
	rel1, err := limiter.acquire(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("acquire 1 failed: %v", err)
	}

	// Acquire slot 2
	rel2, err := limiter.acquire(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("acquire 2 failed: %v", err)
	}

	// Slot 3 should block and time out
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = limiter.acquire(ctx, "example.com")
	if err == nil {
		t.Fatalf("expected timeout error on 3rd slot, got nil")
	}

	// Release slot 1
	rel1()

	// Now acquiring should succeed
	rel3, err := limiter.acquire(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("acquire 3 after release failed: %v", err)
	}

	rel2()
	rel3()

	// Verify host map cleanup when all references are dropped
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if len(limiter.hosts) != 0 {
		t.Errorf("expected empty host map after all releases, got %d entries", len(limiter.hosts))
	}
}

func TestDNSResolverDefault(t *testing.T) {
	res := newDNSResolver(nil)
	if res == nil {
		t.Fatalf("expected non-nil resolver")
	}
}

func TestDNSResolverCustom(t *testing.T) {
	res := newDNSResolver([]string{"1.1.1.1:53"})
	if res == nil {
		t.Fatalf("expected non-nil resolver")
	}
	if !res.PreferGo {
		t.Errorf("expected PreferGo to be true for custom upstream servers")
	}
}
