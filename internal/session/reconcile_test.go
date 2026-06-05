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
