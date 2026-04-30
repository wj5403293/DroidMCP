package main

import (
	"testing"
	"time"
)

func TestLRUCacheGetSet(t *testing.T) {
	c := newLRUCache(2, time.Minute)
	c.Set("a", &cachedResponse{Body: []byte("A")})
	c.Set("b", &cachedResponse{Body: []byte("B")})
	if v, ok := c.Get("a"); !ok || string(v.Body) != "A" {
		t.Fatalf("expected hit on a, got %v %v", ok, v)
	}
	// Adding c with cap=2 evicts the LRU. After Get("a") above, b is LRU.
	c.Set("c", &cachedResponse{Body: []byte("C")})
	if _, ok := c.Get("b"); ok {
		t.Fatal("expected b to be evicted as LRU")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("expected a to survive (most recently used)")
	}
}

func TestLRUCacheTTL(t *testing.T) {
	c := newLRUCache(4, 10*time.Millisecond)
	now := time.Now()
	c.clock = func() time.Time { return now }
	c.Set("a", &cachedResponse{Body: []byte("A")})

	now = now.Add(20 * time.Millisecond)
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected a to be expired")
	}
}

func TestLRUCacheUpdate(t *testing.T) {
	c := newLRUCache(2, time.Minute)
	c.Set("a", &cachedResponse{Body: []byte("A")})
	c.Set("a", &cachedResponse{Body: []byte("A2")})
	if v, _ := c.Get("a"); string(v.Body) != "A2" {
		t.Fatalf("expected updated value, got %s", v.Body)
	}
	if c.Len() != 1 {
		t.Fatalf("expected len 1 after update, got %d", c.Len())
	}
}
