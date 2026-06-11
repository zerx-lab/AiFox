package session

// Responses-API (Codex CLI) session correlation: previous_response_id chains
// successive turns into one session, more reliably than the fingerprint
// fallback. Breaking the chain (a request with no/unknown previous_response_id)
// forks a fresh session. Internal package so consume/flushDirty run
// deterministically without goroutines.

import (
	"testing"
	"time"

	"github.com/zerx-lab/ai-fox/internal/llmparse"
	"github.com/zerx-lab/ai-fox/internal/store"
)

// mkResponsesEntry builds a /v1/responses entry. respID is the id the response
// echoes; prevID (when set) is the request's previous_response_id. input varies
// per turn so the fingerprint differs between turns — proving the chain, not the
// fingerprint, is what groups them.
func mkResponsesEntry(id, respID, prevID, input string) *store.Entry {
	reqBody := `{"model":"gpt-5-codex","input":"` + input + `"`
	if prevID != "" {
		reqBody += `,"previous_response_id":"` + prevID + `"`
	}
	reqBody += `}`
	respBody := `{"id":"` + respID + `","model":"gpt-5-codex","status":"completed",` +
		`"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],` +
		`"usage":{"input_tokens":10,"output_tokens":3}}`
	e := &store.Entry{
		ID:           id,
		StartedAt:    time.Now(),
		EndedAt:      time.Now(),
		Method:       "POST",
		URL:          "/v1/responses",
		StatusCode:   200,
		RequestBody:  reqBody,
		ResponseBody: respBody,
	}
	e.Analysis = llmparse.Analyze(llmparse.Input{
		Method: e.Method, URL: e.URL, RequestBody: e.RequestBody, ResponseBody: e.ResponseBody,
	})
	return e
}

// TestResponsesChainGroupsThreeTurns simulates a Codex three-turn chained
// conversation: turn 1 has no previous_response_id, turns 2 and 3 chain onto the
// prior response. All three must collapse into ONE session via byResponse.
func TestResponsesChainGroupsThreeTurns(t *testing.T) {
	st := store.New(20)
	agg := New(st, "")
	feed(agg,
		mkResponsesEntry("e1", "resp_1", "", "hello"),
		mkResponsesEntry("e2", "resp_2", "resp_1", "again"),
		mkResponsesEntry("e3", "resp_3", "resp_2", "more"),
	)

	s1 := agg.SessionOf("e1")
	s2 := agg.SessionOf("e2")
	s3 := agg.SessionOf("e3")
	if s1 == "" {
		t.Fatalf("e1 not sessioned")
	}
	if s1 != s2 || s2 != s3 {
		t.Fatalf("chain did not collapse: e1=%s e2=%s e3=%s", s1, s2, s3)
	}
	if got := len(agg.List()); got != 1 {
		t.Fatalf("expected 1 session, got %d", got)
	}
	sess, _ := agg.Get(s1)
	if sess.TurnCount != 3 {
		t.Fatalf("expected 3 turns, got %d", sess.TurnCount)
	}
	if sess.InputTokens != 30 || sess.OutputTokens != 9 {
		t.Fatalf("usage not summed across chain: in=%d out=%d", sess.InputTokens, sess.OutputTokens)
	}
}

// TestResponsesBrokenChainForks confirms a request whose previous_response_id is
// absent (or points at an unknown response) opens its own session rather than
// joining the existing chain.
func TestResponsesBrokenChainForks(t *testing.T) {
	st := store.New(20)
	agg := New(st, "")
	feed(agg,
		mkResponsesEntry("e1", "resp_1", "", "hello"),
		mkResponsesEntry("e2", "resp_2", "resp_1", "again"),
		// New conversation: no previous_response_id.
		mkResponsesEntry("e3", "resp_99", "", "fresh start"),
		// Chains onto resp_99, not the first conversation.
		mkResponsesEntry("e4", "resp_100", "resp_99", "continue fresh"),
	)

	s1 := agg.SessionOf("e1")
	s2 := agg.SessionOf("e2")
	s3 := agg.SessionOf("e3")
	s4 := agg.SessionOf("e4")
	if s1 != s2 {
		t.Fatalf("first chain should group: e1=%s e2=%s", s1, s2)
	}
	if s3 != s4 {
		t.Fatalf("second chain should group: e3=%s e4=%s", s3, s4)
	}
	if s1 == s3 {
		t.Fatalf("broken chain should fork into a separate session, both were %s", s1)
	}
	if got := len(agg.List()); got != 2 {
		t.Fatalf("expected 2 sessions, got %d", got)
	}
}
