package pricing

import (
	"math"
	"testing"

	"github.com/zerx-lab/ai-fox/internal/llmparse"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCost_anthropicOpus(t *testing.T) {
	// 1M input + 1M output on opus-4-8 = $5 + $25 = $30.
	u := llmparse.NormalizedUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	cost, ok := Cost("claude-opus-4-8", u)
	if !ok || !approx(cost, 30) {
		t.Fatalf("opus cost: %v ok=%v", cost, ok)
	}
}

func TestCost_cacheTokens(t *testing.T) {
	// haiku: input $1, cache read $0.1, cache write $1.25 per MTok.
	u := llmparse.NormalizedUsage{
		InputTokens:      1_000_000,
		CacheReadTokens:  1_000_000,
		CacheWriteTokens: 1_000_000,
	}
	cost, ok := Cost("claude-haiku-4-5", u)
	if !ok || !approx(cost, 1+0.1+1.25) {
		t.Fatalf("haiku cache cost: %v ok=%v", cost, ok)
	}
}

func TestCost_prefixMatchDatedSnapshot(t *testing.T) {
	u := llmparse.NormalizedUsage{InputTokens: 1_000_000}
	cost, ok := Cost("claude-sonnet-4-6-20250930", u)
	if !ok || !approx(cost, 3) {
		t.Fatalf("dated snapshot should prefix-match sonnet: %v ok=%v", cost, ok)
	}
}

func TestCost_openai(t *testing.T) {
	u := llmparse.NormalizedUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	cost, ok := Cost("gpt-4o", u)
	if !ok || !approx(cost, 2.5+10) {
		t.Fatalf("gpt-4o cost: %v ok=%v", cost, ok)
	}
}

func TestCost_unknownModel(t *testing.T) {
	if _, ok := Cost("some-unknown-model", llmparse.NormalizedUsage{InputTokens: 100}); ok {
		t.Fatalf("unknown model should return ok=false")
	}
	if _, ok := Cost("", llmparse.NormalizedUsage{}); ok {
		t.Fatalf("empty model should return ok=false")
	}
}

func TestLookup_longestPrefixWins(t *testing.T) {
	// gpt-4o-mini must not be shadowed by gpt-4o (longest prefix wins).
	r, ok := Lookup("gpt-4o-mini-2024-07-18")
	if !ok || r.InputPerMTok != 0.15 {
		t.Fatalf("gpt-4o-mini prefix match wrong: %+v ok=%v", r, ok)
	}
}
