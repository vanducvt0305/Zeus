package usage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecordAndScore(t *testing.T) {
	r := NewMemory("")
	if r.Score("x") != 0 {
		t.Fatal("unused MCP should score 0")
	}

	// A well-used, mostly-successful MCP should score higher than a rarely-used
	// or failing one.
	for i := 0; i < 50; i++ {
		r.Record("good", OutcomeSuccess)
	}
	r.Record("rare", OutcomeSuccess)
	for i := 0; i < 50; i++ {
		if i%2 == 0 {
			r.Record("flaky", OutcomeSuccess)
		} else {
			r.Record("flaky", OutcomeUnreachable)
		}
	}

	if !(r.Score("good") > r.Score("rare")) {
		t.Errorf("volume should help: good %.3f vs rare %.3f", r.Score("good"), r.Score("rare"))
	}
	if !(r.Score("good") > r.Score("flaky")) {
		t.Errorf("success rate should help: good %.3f vs flaky %.3f", r.Score("good"), r.Score("flaky"))
	}
	for _, id := range []string{"good", "rare", "flaky"} {
		if s := r.Score(id); s < 0 || s > 1 {
			t.Errorf("score(%s)=%.3f out of range", id, s)
		}
	}
}

// TestToolErrorBeatsUnreachable is the #5 fix: a reachable server that the agent
// mis-called (tool error) must score higher than one that couldn't be reached at
// all, and lower than one that cleanly succeeded — the prior tracks the server's
// serviceability, not the caller's bad arguments.
func TestToolErrorBeatsUnreachable(t *testing.T) {
	r := NewMemory("")
	for i := 0; i < 20; i++ {
		r.Record("clean", OutcomeSuccess)
		r.Record("misused", OutcomeToolError)
		r.Record("broken", OutcomeUnreachable)
	}
	clean, misused, broken := r.Score("clean"), r.Score("misused"), r.Score("broken")
	if !(clean > misused && misused > broken) {
		t.Fatalf("want clean > misused > broken, got %.3f, %.3f, %.3f", clean, misused, broken)
	}
}

func TestSnapshotCounts(t *testing.T) {
	r := NewMemory("")
	r.Record("a", OutcomeSuccess)
	r.Record("a", OutcomeToolError)
	r.Record("a", OutcomeUnreachable)
	snap := r.Snapshot()
	if snap["a"].Calls != 3 || snap["a"].Successes != 1 || snap["a"].ToolErrors != 1 {
		t.Fatalf("snapshot = %+v, want calls=3 successes=1 tool_errors=1", snap["a"])
	}
}

func TestPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	r := NewMemory(path)
	r.Record("a", OutcomeSuccess)
	r.Record("a", OutcomeSuccess)
	if err := r.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file written: %v", err)
	}

	// A fresh recorder loads the prior tallies.
	r2 := NewMemory(path)
	if r2.Snapshot()["a"].Calls != 2 {
		t.Fatalf("reloaded calls = %d, want 2", r2.Snapshot()["a"].Calls)
	}
}
