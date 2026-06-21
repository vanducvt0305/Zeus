// Package usage is the flywheel: it records which MCPs agents actually invoke
// (via call_mcp) and whether those calls succeed, then turns that into a 0..1
// usage prior that search blends into ranking. Real selections and outcomes are
// the one signal competitors can't copy — they require traffic — so over time
// the index learns which servers genuinely work, not just which look good.
package usage

import (
	"encoding/json"
	"math"
	"os"
	"sync"
)

// Stat is the per-MCP tally of invocations.
type Stat struct {
	Calls     int64 `json:"calls"`
	Successes int64 `json:"successes"`
}

// Recorder records invocations and exposes a usage score.
type Recorder interface {
	// Record notes one invocation of mcpID and whether it succeeded.
	Record(mcpID string, success bool)
	// Score returns the 0..1 usage prior for mcpID (0 if never used).
	Score(mcpID string) float64
	// Snapshot returns a copy of all tallies.
	Snapshot() map[string]Stat
	// Flush persists the tallies if a path is configured.
	Flush() error
}

// Noop records nothing and scores everything 0.
type Noop struct{}

func (Noop) Record(string, bool)        {}
func (Noop) Score(string) float64        { return 0 }
func (Noop) Snapshot() map[string]Stat   { return nil }
func (Noop) Flush() error                { return nil }

// Memory is a thread-safe in-memory recorder with optional JSON persistence.
type Memory struct {
	mu    sync.RWMutex
	m     map[string]Stat
	path  string
	dirty bool
}

// NewMemory creates a recorder. If path is non-empty, prior tallies are loaded
// from it and Flush writes back to it.
func NewMemory(path string) *Memory {
	r := &Memory{m: make(map[string]Stat), path: path}
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, &r.m)
		}
	}
	return r
}

func (r *Memory) Record(mcpID string, success bool) {
	if mcpID == "" {
		return
	}
	r.mu.Lock()
	s := r.m[mcpID]
	s.Calls++
	if success {
		s.Successes++
	}
	r.m[mcpID] = s
	r.dirty = true
	r.mu.Unlock()
}

func (r *Memory) Score(mcpID string) float64 {
	r.mu.RLock()
	s := r.m[mcpID]
	r.mu.RUnlock()
	return score(s)
}

func (r *Memory) Snapshot() map[string]Stat {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Stat, len(r.m))
	for k, v := range r.m {
		out[k] = v
	}
	return out
}

func (r *Memory) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.path == "" || !r.dirty {
		return nil
	}
	data, err := json.Marshal(r.m)
	if err != nil {
		return err
	}
	if err := os.WriteFile(r.path, data, 0o644); err != nil {
		return err
	}
	r.dirty = false
	return nil
}

// score combines success rate (Laplace-smoothed so a single lucky call doesn't
// score 1.0) with call volume (log-scaled, saturating around 100 calls).
func score(s Stat) float64 {
	if s.Calls <= 0 {
		return 0
	}
	rate := float64(s.Successes+1) / float64(s.Calls+2)
	volume := math.Log10(float64(s.Calls)+1) / 2.0
	if volume > 1 {
		volume = 1
	}
	return rate * volume
}
