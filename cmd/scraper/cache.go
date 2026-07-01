// Tiny in-process LRU for fetch responses. Same-session repeats (LLM
// re-asking for the same page) are cheap; new URLs evict the oldest entry
// once we hit the cap. Entries also have a TTL so a long-lived process does
// not return week-old HTML.
package main

import (
	"container/list"
	"sync"
	"time"
)

const (
	cacheDefaultMax = 64
	cacheDefaultTTL = 5 * time.Minute
)

// cachedResponse is exactly what a handler needs to skip the network round
// trip on the next call: the bytes, the status code, the response headers,
// and the final URL after redirects.
type cachedResponse struct {
	URL       string
	Status    int
	Header    map[string][]string
	Body      []byte
	FetchedAt time.Time
	FromCache bool
}

type lruEntry struct {
	key       string
	val       *cachedResponse
	expiresAt time.Time
}

type lruCache struct {
	mu    sync.Mutex
	max   int
	ttl   time.Duration
	ll    *list.List
	items map[string]*list.Element
	clock func() time.Time
}

func newLRUCache(max int, ttl time.Duration) *lruCache {
	if max <= 0 {
		max = cacheDefaultMax
	}
	if ttl <= 0 {
		ttl = cacheDefaultTTL
	}
	return &lruCache{
		max:   max,
		ttl:   ttl,
		ll:    list.New(),
		items: make(map[string]*list.Element),
		clock: time.Now,
	}
}

func (c *lruCache) Get(key string) (*cachedResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	e := el.Value.(*lruEntry)
	if c.clock().After(e.expiresAt) {
		c.ll.Remove(el)
		delete(c.items, key)
		return nil, false
	}
	c.ll.MoveToFront(el)
	return e.val, true
}

func (c *lruCache) Set(key string, val *cachedResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		ent := el.Value.(*lruEntry)
		ent.val = val
		ent.expiresAt = c.clock().Add(c.ttl)
		c.ll.MoveToFront(el)
		return
	}
	e := &lruEntry{key: key, val: val, expiresAt: c.clock().Add(c.ttl)}
	el := c.ll.PushFront(e)
	c.items[key] = el
	for c.ll.Len() > c.max {
		oldest := c.ll.Back()
		if oldest == nil {
			break
		}
		c.ll.Remove(oldest)
		delete(c.items, oldest.Value.(*lruEntry).key)
	}
}

func (c *lruCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
