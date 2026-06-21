package search

import (
	"container/list"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vanducvt0305/zeus/internal/store"
)

// resultCache is a small, thread-safe LRU with per-entry TTL over finished search
// results. Agents frequently repeat the same discovery query (retries, multi-step
// plans), and the whole pipeline — embed → hybrid retrieve → rerank → blend — is
// deterministic for a fixed index, so a short-lived cache cuts repeated latency
// and embedding cost. The TTL bounds staleness so fresh index/usage state still
// surfaces within seconds. A nil *resultCache is a no-op (cache disabled), so the
// Service can call get/put unconditionally.
type resultCache struct {
	ttl time.Duration
	max int
	mu  sync.Mutex
	ll  *list.List
	m   map[string]*list.Element
}

type cacheEntry struct {
	key     string
	hits    []store.Hit
	expires time.Time
}

// newResultCache returns a cache, or nil (disabled) if ttl or max is non-positive.
func newResultCache(ttl time.Duration, max int) *resultCache {
	if ttl <= 0 || max <= 0 {
		return nil
	}
	return &resultCache{ttl: ttl, max: max, ll: list.New(), m: make(map[string]*list.Element, max)}
}

func (c *resultCache) get(key string) ([]store.Hit, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.m[key]
	if !ok {
		return nil, false
	}
	e := el.Value.(*cacheEntry)
	if time.Now().After(e.expires) {
		c.ll.Remove(el)
		delete(c.m, key)
		return nil, false
	}
	c.ll.MoveToFront(el)
	return cloneHits(e.hits), true
}

func (c *resultCache) put(key string, hits []store.Hit) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	exp := time.Now().Add(c.ttl)
	if el, ok := c.m[key]; ok {
		e := el.Value.(*cacheEntry)
		e.hits = cloneHits(hits)
		e.expires = exp
		c.ll.MoveToFront(el)
		return
	}
	c.m[key] = c.ll.PushFront(&cacheEntry{key: key, hits: cloneHits(hits), expires: exp})
	for c.ll.Len() > c.max {
		back := c.ll.Back()
		if back == nil {
			break
		}
		c.ll.Remove(back)
		delete(c.m, back.Value.(*cacheEntry).key)
	}
}

// cloneHits returns a shallow copy of the slice so a cached result can't be
// reordered/truncated by a caller (the MCP payloads are treated read-only).
func cloneHits(h []store.Hit) []store.Hit {
	out := make([]store.Hit, len(h))
	copy(out, h)
	return out
}

// cacheKey captures everything that changes a result: the query, the (defaulted)
// top-k, the confidence cutoff, and the filter. Categories are sorted so the key
// is order-independent. NUL separators avoid collisions between adjacent fields.
func cacheKey(query string, topK int, minConfidence float64, f store.Filter) string {
	cats := append([]string(nil), f.Categories...)
	sort.Strings(cats)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\x00%d\x00%g\x00%s\x00%s", query, topK, minConfidence, f.Source, strings.Join(cats, ","))
	return b.String()
}
