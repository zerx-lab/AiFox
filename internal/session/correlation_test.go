package session

// L3 correlation tests: header-key primary, replay fork, utility exclusion,
// and the fallback longest-prefix fork. Internal package so the deterministic
// consume/flushDirty path can be driven directly (no goroutines/timing).

import (
	"strings"
	"testing"
	"time"

	"github.com/zerx-lab/ai-fox/internal/llmparse"
	"github.com/zerx-lab/ai-fox/internal/store"
)

// reqBody builds an Anthropic /v1/messages request. msgs alternate
// user/assistant starting with user. withTools controls the tool catalog
// (its presence is what IsUtilityRequest keys on).
func reqBody(model, system string, msgs []string, withTools bool) string {
	var b strings.Builder
	b.WriteString(`{"model":"` + model + `","stream":true,`)
	b.WriteString(`"system":[{"type":"text","text":"` + system + `"}],`)
	if withTools {
		b.WriteString(`"tools":[{"name":"bash","description":"run","input_schema":{"type":"object"}}],`)
	}
	b.WriteString(`"messages":[`)
	for i, m := range msgs {
		if i > 0 {
			b.WriteString(",")
		}
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		b.WriteString(`{"role":"` + role + `","content":[{"type":"text","text":"` + m + `"}]}`)
	}
	b.WriteString(`]}`)
	return b.String()
}

type entryOpts struct {
	affinity     string
	replayedFrom string
	model        string
	system       string
	msgs         []string
	withTools    bool
}

func mkEntry(id string, o entryOpts) *store.Entry {
	e := &store.Entry{
		ID:             id,
		StartedAt:      time.Now(),
		EndedAt:        time.Now(),
		Method:         "POST",
		URL:            "/v1/messages",
		StatusCode:     200,
		RequestBody:    reqBody(o.model, o.system, o.msgs, o.withTools),
		ResponseBody:   `{"id":"r","model":"` + o.model + `","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":5,"output_tokens":2}}`,
		ReplayedFromID: o.replayedFrom,
	}
	if o.affinity != "" {
		e.RequestHeaders = map[string]string{"X-Session-Affinity": o.affinity}
	}
	e.Analysis = llmparse.Analyze(llmparse.Input{
		Method: e.Method, URL: e.URL, RequestBody: e.RequestBody, ResponseBody: e.ResponseBody,
	})
	return e
}

func feed(a *Aggregator, entries ...*store.Entry) {
	for _, e := range entries {
		a.st.Add(e)
		got, _ := a.st.Get(e.ID)
		a.consume(got)
	}
	a.flushDirty()
}

// TestHeaderKeyGroupsAcrossModelsAndSystems mirrors real opencode traffic: a
// main opus conversation, its haiku title-gen sub-task, and a follow-up opus
// turn all share one X-Session-Affinity and must collapse into ONE session
// despite differing model/system/first-message; a different affinity is its own
// session.
func TestHeaderKeyGroupsAcrossModelsAndSystems(t *testing.T) {
	st := store.New(20)
	agg := New(st, "")
	feed(agg,
		mkEntry("e1", entryOpts{affinity: "ses_A", model: "claude-opus-4-8", system: "You are OpenCode.", msgs: []string{"run ls"}, withTools: true}),
		mkEntry("e2", entryOpts{affinity: "ses_A", model: "claude-haiku-4-5", system: "You are a title generator.", msgs: []string{"Generate a title"}}),
		mkEntry("e3", entryOpts{affinity: "ses_A", model: "claude-opus-4-8", system: "You are OpenCode.", msgs: []string{"run ls", "ok", "now cat"}, withTools: true}),
		mkEntry("e4", entryOpts{affinity: "ses_B", model: "claude-haiku-4-5", system: "You are a title generator.", msgs: []string{"Generate a title"}}),
	)

	sessions := agg.List()
	if len(sessions) != 2 {
		t.Fatalf("want 2 sessions (by affinity), got %d", len(sessions))
	}
	var big *Summary
	for _, s := range sessions {
		if len(s.EntryIDs) == 3 {
			big = s
		}
	}
	if big == nil {
		t.Fatalf("expected a 3-entry session; got %+v", sessions)
	}
	// turns exclude the haiku title-gen (utility); 2 opus turns remain.
	if big.TurnCount != 2 {
		t.Errorf("TurnCount = %d, want 2 (haiku title-gen excluded)", big.TurnCount)
	}
	if big.Model != "claude-opus-4-8" {
		t.Errorf("primary Model = %q, want opus (non-utility)", big.Model)
	}
	if len(big.Models) != 2 || big.Models[0] != "claude-opus-4-8" {
		t.Errorf("Models = %v, want [opus, haiku]", big.Models)
	}
}

// TestHeaderKeyCaseInsensitive: the same affinity value under a non-canonical
// header casing must map to the same session.
func TestHeaderKeyCaseInsensitive(t *testing.T) {
	st := store.New(10)
	agg := New(st, "")
	e1 := mkEntry("e1", entryOpts{affinity: "ses_X", model: "m", system: "s", msgs: []string{"a"}, withTools: true})
	e2 := mkEntry("e2", entryOpts{model: "m", system: "s", msgs: []string{"b"}, withTools: true})
	e2.RequestHeaders = map[string]string{"x-session-affinity": "ses_X"} // lowercase
	feed(agg, e1, e2)
	if got := len(agg.List()); got != 1 {
		t.Fatalf("case-insensitive header lookup failed: want 1 session, got %d", got)
	}
}

// TestReplayForksSession: a replayed entry (ReplayedFromID set) must NOT fold
// into the original session even though it carries the original's affinity.
func TestReplayForksSession(t *testing.T) {
	st := store.New(10)
	agg := New(st, "")
	orig := mkEntry("e1", entryOpts{affinity: "ses_R", model: "m", system: "s", msgs: []string{"a"}, withTools: true})
	replay := mkEntry("e2", entryOpts{affinity: "ses_R", replayedFrom: "e1", model: "m", system: "s", msgs: []string{"a"}, withTools: true})
	feed(agg, orig, replay)
	sessions := agg.List()
	if len(sessions) != 2 {
		t.Fatalf("replay should fork: want 2 sessions, got %d", len(sessions))
	}
}

// TestFallbackForksOnDivergence: with no session header, two conversations that
// share the fingerprint anchor but diverge in their message history must not
// cross-attribute — the longest-prefix match forks when neither contains the
// other.
func TestFallbackForksOnDivergence(t *testing.T) {
	st := store.New(10)
	agg := New(st, "")
	// Session A grows to [user X, asst A, user Y].
	feed(agg,
		mkEntry("a1", entryOpts{model: "m", system: "s", msgs: []string{"X"}}),
		mkEntry("a2", entryOpts{model: "m", system: "s", msgs: []string{"X", "A", "Y"}}),
	)
	if got := len(agg.List()); got != 1 {
		t.Fatalf("same conversation should stay 1 session, got %d", got)
	}
	// A divergent continuation [user X, asst B, user Z] shares the anchor (first
	// user "X") but is not a prefix-extension of A's history → must fork.
	feed(agg, mkEntry("b1", entryOpts{model: "m", system: "s", msgs: []string{"X", "B", "Z"}}))
	if got := len(agg.List()); got != 2 {
		t.Fatalf("divergent conversation should fork: want 2 sessions, got %d", got)
	}
}

// TestProviderBackfilledForHeaderKeyedNilAnalysisFirst reproduces the live
// proxy ordering: a header-keyed entry is first consumed with NO analysis (the
// request-time Add broadcasts before runAnalysis), so newSession mints the
// session with empty Provider/Model; a later update carries the analysis. The
// rollup must backfill Provider/Model from the store on flush.
func TestProviderBackfilledForHeaderKeyedNilAnalysisFirst(t *testing.T) {
	st := store.New(10)
	agg := New(st, "")

	// Add with headers but NO analysis yet, and consume — mimics store.Add at
	// request time before any runAnalysis.
	e := &store.Entry{
		ID:             "e1",
		StartedAt:      time.Now(),
		Method:         "POST",
		URL:            "/v1/messages",
		RequestHeaders: map[string]string{"X-Session-Affinity": "ses_P"},
		RequestBody:    reqBody("claude-opus-4-8", "sys", []string{"hi"}, true),
	}
	st.Add(e)
	got, _ := st.Get("e1")
	agg.consume(got) // norm == nil here; session minted with empty Provider
	agg.flushDirty()
	if s := agg.List(); len(s) != 1 || s[0].Provider != "" {
		t.Fatalf("pre-analysis: expected 1 session with empty provider, got %+v", s)
	}

	// Now the analysis lands (finalize) and a fresh consume + flush backfills.
	st.Update("e1", func(en *store.Entry) {
		en.EndedAt = time.Now()
		en.ResponseBody = `{"id":"r","model":"claude-opus-4-8","usage":{"input_tokens":3,"output_tokens":1}}`
		en.Analysis = llmparse.Analyze(llmparse.Input{
			Method: en.Method, URL: en.URL, RequestBody: en.RequestBody, ResponseBody: en.ResponseBody,
		})
	})
	got2, _ := st.Get("e1")
	agg.consume(got2)
	agg.flushDirty()

	s := agg.List()
	if len(s) != 1 {
		t.Fatalf("want 1 session, got %d", len(s))
	}
	if s[0].Provider != "anthropic" {
		t.Errorf("Provider = %q, want anthropic (backfilled from rollup)", s[0].Provider)
	}
	if s[0].Model != "claude-opus-4-8" {
		t.Errorf("Model = %q, want opus", s[0].Model)
	}
}

// TestNameSurvivesRestartViaHeaderAnchor: a custom name set on a header-keyed
// session must re-apply after the aggregator is recreated, because the names
// file is keyed by the (stable) header anchor.
func TestNameSurvivesRestartViaHeaderAnchor(t *testing.T) {
	dir := t.TempDir()
	namesPath := dir + "/session-names.json"
	st := store.New(10)

	agg := New(st, namesPath)
	feed(agg, mkEntry("e1", entryOpts{affinity: "ses_N", model: "m", system: "s", msgs: []string{"a"}, withTools: true}))
	sessions := agg.List()
	if len(sessions) != 1 {
		t.Fatalf("setup: want 1 session, got %d", len(sessions))
	}
	if err := agg.SetName(sessions[0].ID, "My Session"); err != nil {
		t.Fatalf("SetName: %v", err)
	}

	// Recreate the aggregator (new IDs) over the same store + names file.
	agg2 := New(st, namesPath)
	feed(agg2, mkEntry("e1", entryOpts{affinity: "ses_N", model: "m", system: "s", msgs: []string{"a"}, withTools: true}))
	s2 := agg2.List()
	if len(s2) != 1 {
		t.Fatalf("after restart: want 1 session, got %d", len(s2))
	}
	if s2[0].Name != "My Session" {
		t.Errorf("name not re-applied after restart: got %q", s2[0].Name)
	}
}

// TestFallbackKeepsModelInFingerprint: two headerless conversations identical
// except for model must NOT merge (Model stays in the fallback fingerprint).
func TestFallbackKeepsModelInFingerprint(t *testing.T) {
	st := store.New(10)
	agg := New(st, "")
	feed(agg,
		mkEntry("e1", entryOpts{model: "claude-opus-4-8", system: "s", msgs: []string{"same"}}),
		mkEntry("e2", entryOpts{model: "claude-haiku-4-5", system: "s", msgs: []string{"same"}}),
	)
	if got := len(agg.List()); got != 2 {
		t.Fatalf("different models must not merge in fallback: want 2 sessions, got %d", got)
	}
}
