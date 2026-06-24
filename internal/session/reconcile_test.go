package session

// Internal test: reconcile must re-heal a session left "active" by a dropped
// finalize broadcast for an already-bucketed entry. Uses the unexported
// consume/reconcile/flushDirty directly (no goroutines) so the dropped-finalize
// scenario is reproduced deterministically.

import (
	"testing"
	"time"

	"github.com/zerx-lab/ai-fox/internal/llmparse"
	"github.com/zerx-lab/ai-fox/internal/store"
)

func streamingEntry(id string) *store.Entry {
	e := &store.Entry{
		ID:        id,
		StartedAt: time.Now(),
		Method:    "POST",
		URL:       "/v1/messages",
		Streaming: true,
		RequestBody: `{"model":"claude-opus-4-8","stream":true,` +
			`"system":[{"type":"text","text":"sys"}],` +
			`"messages":[{"role":"user","content":[{"type":"text","text":"` + id + `"}]}]}`,
		ResponseBody: "",
	}
	e.Analysis = llmparse.Analyze(llmparse.Input{
		Method: e.Method, URL: e.URL, RequestBody: e.RequestBody, Streaming: true,
	})
	return e
}

func TestReconcileHealsStuckActiveAfterDroppedFinalize(t *testing.T) {
	st := store.New(10)
	agg := New(st, "")

	st.Add(streamingEntry("e-1"))
	got, ok := st.Get("e-1")
	if !ok {
		t.Fatal("entry not in store")
	}
	// Bucket the streaming entry (the only update the aggregator "receives").
	agg.consume(got)
	agg.flushDirty()
	if s := agg.List(); len(s) != 1 || s[0].Status != "active" {
		t.Fatalf("expected 1 active session, got %+v", s)
	}

	// Finalize in the store but DO NOT deliver the finalize to the aggregator —
	// this is the dropped-broadcast case. consume is never called for it.
	st.Update("e-1", func(e *store.Entry) {
		e.EndedAt = time.Now()
		e.DurationMillis = 5
	})

	// Without the heal, reconcile would skip e-1 as `known` and the session
	// would stay active forever. With the heal it re-marks the unfinished
	// session dirty, and flushDirty recomputes it from store truth.
	agg.reconcile()
	agg.flushDirty()

	s := agg.List()
	if len(s) != 1 {
		t.Fatalf("expected 1 session, got %d", len(s))
	}
	if s[0].Status != "completed" || s[0].HasUnfinished {
		t.Fatalf("session not healed: status=%q hasUnfinished=%v", s[0].Status, s[0].HasUnfinished)
	}
}

// TestReconcilePrunesEvictedEntriesFromByEntry guards G4 (session side): once the
// store's ring buffer evicts an entry, reconcile must drop it from byEntry so the
// map stays bounded by the store capacity, and trim the owning session's EntryIDs.
func TestReconcilePrunesEvictedEntriesFromByEntry(t *testing.T) {
	st := store.New(2) // tiny ring buffer to force eviction
	agg := New(st, "")

	// Three distinct entries; the store retains only the last two.
	for _, id := range []string{"e-1", "e-2", "e-3"} {
		e := streamingEntry(id)
		st.Add(e)
		got, _ := st.Get(id)
		if got != nil {
			agg.consume(got)
		}
	}
	agg.flushDirty()

	// e-1 has been evicted from the store but consume recorded it in byEntry.
	if _, ok := st.Get("e-1"); ok {
		t.Fatal("precondition: e-1 should have been evicted from the store")
	}

	agg.reconcile()
	agg.flushDirty()

	agg.mu.RLock()
	defer agg.mu.RUnlock()
	if _, stillThere := agg.byEntry["e-1"]; stillThere {
		t.Fatal("reconcile must prune evicted entry e-1 from byEntry")
	}
	if len(agg.byEntry) != 2 {
		t.Fatalf("byEntry should track only the 2 live entries, got %d", len(agg.byEntry))
	}
	for _, s := range agg.sessions {
		for _, eid := range s.EntryIDs {
			if eid == "e-1" {
				t.Fatalf("session %s still references evicted entry e-1", s.ID)
			}
		}
	}
}
