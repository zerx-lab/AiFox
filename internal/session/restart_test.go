package session_test

// Restart re-aggregation: entries persisted to the JSONL store decode their
// Analysis as map[string]any on reload, which previously failed the
// `*llmparse.Analysis` type assertion in the aggregator — so restored traffic
// never re-aggregated into sessions (the user saw zero sessions after a
// restart). main wires store.MapAnalysis(llmparse.ReifyAnalysis) on boot to fix
// it; this test locks that behavior in.

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zerx-lab/ai-fox/internal/llmparse"
	"github.com/zerx-lab/ai-fox/internal/session"
	"github.com/zerx-lab/ai-fox/internal/store"
)

func anthropicReq(user string) string {
	return `{"model":"claude-opus-4-8","max_tokens":256,"stream":false,` +
		`"system":[{"type":"text","text":"You are OpenCode."}],` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"` + user + `"}]}]}`
}

func anthropicResp() string {
	return `{"id":"msg_1","model":"claude-opus-4-8","role":"assistant","stop_reason":"end_turn",` +
		`"content":[{"type":"text","text":"hi"}],` +
		`"usage":{"input_tokens":12,"output_tokens":3,"cache_read_input_tokens":4}}`
}

// makeEntry builds a finalized entry with a real *llmparse.Analysis attached,
// mirroring what the proxy captures.
func makeEntry(id, user string) *store.Entry {
	e := &store.Entry{
		ID:           id,
		StartedAt:    time.Now().Add(-time.Second),
		EndedAt:      time.Now(),
		Method:       "POST",
		URL:          "/v1/messages",
		StatusCode:   200,
		RequestBody:  anthropicReq(user),
		ResponseBody: anthropicResp(),
	}
	e.Analysis = llmparse.Analyze(llmparse.Input{
		Method:       e.Method,
		URL:          e.URL,
		RequestBody:  e.RequestBody,
		ResponseBody: e.ResponseBody,
	})
	return e
}

func TestRestartReaggregatesPersistedSessions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "traffic.jsonl")

	// First boot: persist two entries with live *llmparse.Analysis.
	{
		st, err := store.NewPersistent(50, path)
		if err != nil {
			t.Fatalf("store: %v", err)
		}
		st.Add(makeEntry("e-1", "alpha task"))
		st.Add(makeEntry("e-2", "beta task"))
	}

	// Second boot: reload from disk (Analysis is now map[string]any) and apply
	// the reify step main does, then aggregate.
	st, err := store.NewPersistent(50, path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	// Sanity: before reify, the restored Analysis is NOT *llmparse.Analysis.
	for _, e := range st.List() {
		if _, ok := e.Analysis.(*llmparse.Analysis); ok {
			t.Fatalf("expected restored Analysis to decode as map, got *Analysis for %s", e.ID)
		}
	}
	st.MapAnalysis(func(a any) any {
		if r := llmparse.ReifyAnalysis(a); r != nil {
			return r
		}
		return a
	})
	for _, e := range st.List() {
		a, ok := e.Analysis.(*llmparse.Analysis)
		if !ok {
			t.Fatalf("reify failed: %s still not *Analysis", e.ID)
		}
		// Normalized must be populated after reify — it is the field the session
		// aggregator keys on. A json-tag regression that zeroed Normalized would
		// produce empty sessions without triggering the type-assertion check above.
		if a.Normalized == nil {
			t.Fatalf("reify produced *Analysis with nil Normalized for %s; "+
				"check the 'normalized' json tag on Analysis.Normalized", e.ID)
		}
	}

	agg := session.New(st, "")
	stop := agg.Start()
	defer stop()

	// Two distinct conversations -> two sessions, with usage rolled up.
	deadline := time.Now().Add(2 * time.Second)
	var sessions []*session.Summary
	for time.Now().Before(deadline) {
		sessions = agg.List()
		if len(sessions) == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 re-aggregated sessions after restart, got %d", len(sessions))
	}
	for _, s := range sessions {
		if s.Model != "claude-opus-4-8" {
			t.Errorf("session %s model = %q, want claude-opus-4-8", s.ID, s.Model)
		}
		if s.InputTokens != 12 || s.OutputTokens != 3 || s.CacheRead != 4 {
			t.Errorf("session %s usage = in%d/out%d/cr%d, want 12/3/4",
				s.ID, s.InputTokens, s.OutputTokens, s.CacheRead)
		}
		if s.Status != "completed" {
			t.Errorf("session %s status = %q, want completed", s.ID, s.Status)
		}
	}
}
