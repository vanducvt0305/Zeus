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
		r.Record("good", true)
	}
	r.Record("rare", true)
	for i := 0; i < 50; i++ {
		r.Record("flaky", i%2 == 0) // ~50% success
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

func TestSnapshotCounts(t *testing.T) {
	r := NewMemory("")
	r.Record("a", true)
	r.Record("a", false)
	snap := r.Snapshot()
	if snap["a"].Calls != 2 || snap["a"].Successes != 1 {
		t.Fatalf("snapshot = %+v, want calls=2 successes=1", snap["a"])
	}
}

func TestPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	r := NewMemory(path)
	r.Record("a", true)
	r.Record("a", true)
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
