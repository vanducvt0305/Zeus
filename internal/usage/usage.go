// Package usage is the flywheel: it records which MCPs agents actually invoke
// (via call_mcp) and how those calls turn out, then turns that into a 0..1 usage
// prior that search blends into ranking. Real selections and outcomes are the
// one signal competitors can't copy — they require traffic — so over time the
// index learns which servers genuinely work, not just which look good.
//
// The signal is deliberately attributed with care. A call has three outcomes,
// and they are NOT equally the MCP's fault:
//
//   - Success: Zeus reached the MCP, invoked the tool, and got a clean result.
//   - ToolError: Zeus reached the MCP and it ran the tool, but the tool returned
//     an error. This is usually the *caller's* mistake (bad arguments), not a
//     defect of the MCP, so it earns partial credit rather than counting as a
//     failure — penalizing the server for the agent's bad input would poison the
//     prior with noise.
//   - Unreachable: Zeus could not connect to or invoke the MCP at all (transport
//     down, timeout, no remote endpoint). That is a real serviceability defect
//     and is the only outcome that counts fully against the server.
package usage

import (
	"encoding/json"
	"math"
	"os"
	"sync"
)

// Outcome is the result of one forwarded call, attributed to the MCP.
type Outcome int

const (
	// OutcomeSuccess: reached, invoked, clean result.
	OutcomeSuccess Outcome = iota
	// OutcomeToolError: reached and invoked, but the tool returned an error
	// (usually the caller's bad arguments, not the server's fault).
	OutcomeToolError
	// OutcomeUnreachable: could not connect to or invoke the MCP at all.
	OutcomeUnreachable
)

// toolErrorCredit is how much a served-but-errored call counts toward the usage
// score relative to a clean success. It sits between a success (1.0) and an
// unreachable call (0.0): the server demonstrably worked, but the call didn't
// fully succeed, so it earns half credit.
const toolErrorCredit = 0.5

// Stat is the per-MCP tally of invocations. Unreachable calls are the remainder:
// Calls - Successes - ToolErrors.
type Stat struct {
	Calls      int64 `json:"calls"`
	Successes  int64 `json:"successes"`
	ToolErrors int64 `json:"tool_errors"`
}

// Recorder records invocations and exposes a usage score.
type Recorder interface {
	// Record notes one invocation of mcpID and its outcome.
	Record(mcpID string, outcome Outcome)
	// Score returns the 0..1 usage prior for mcpID (0 if never used).
	Score(mcpID string) float64
	// Snapshot returns a copy of all tallies.
	Snapshot() map[string]Stat
	// Flush persists the tallies if a path is configured.
	Flush() error
}

// Noop records nothing and scores everything 0.
type Noop struct{}

func (Noop) Record(string, Outcome)    {}
func (Noop) Score(string) float64      { return 0 }
func (Noop) Snapshot() map[string]Stat { return nil }
func (Noop) Flush() error              { return nil }

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

func (r *Memory) Record(mcpID string, outcome Outcome) {
	if mcpID == "" {
		return
	}
	r.mu.Lock()
	s := r.m[mcpID]
	s.Calls++
	switch outcome {
	case OutcomeSuccess:
		s.Successes++
	case OutcomeToolError:
		s.ToolErrors++
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

// score combines an effective success rate (Laplace-smoothed so a single lucky
// call doesn't score 1.0) with call volume (log-scaled, saturating around 100
// calls). Tool errors earn partial credit, so a reachable server an agent
// mis-called outranks one that couldn't be reached at all.
func score(s Stat) float64 {
	if s.Calls <= 0 {
		return 0
	}
	eff := float64(s.Successes) + toolErrorCredit*float64(s.ToolErrors)
	rate := (eff + 1) / (float64(s.Calls) + 2)
	volume := math.Log10(float64(s.Calls)+1) / 2.0
	if volume > 1 {
		volume = 1
	}
	return rate * volume
}
