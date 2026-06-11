// Package pricing maps model identifiers to per-token costs and computes the
// dollar cost of a normalized usage record. The table is built-in and covers
// the mainstream Anthropic and OpenAI models the proxy is likely to see;
// unknown models return ok=false so callers can omit a cost rather than show a
// wrong one.
//
// Prices are expressed per million tokens (per MTok) for readability and
// converted to per-token at compute time. Model lookup tolerates date/variant
// suffixes (e.g. "claude-sonnet-4-6-20250930") via longest-prefix matching, so
// dated snapshots resolve to their base model's price.
package pricing

import (
	"sort"
	"strings"

	"github.com/zerx-lab/ai-fox/internal/llmparse"
)

// Rates holds the per-MTok prices for one model. CacheRead and CacheWrite are
// optional (zero means "same as input" is NOT assumed — a zero cache rate
// contributes nothing, which matches providers that bill cache reads at the
// input rate only when the usage record carries cache tokens).
type Rates struct {
	// InputPerMTok is the price per million uncached input tokens.
	InputPerMTok float64
	// OutputPerMTok is the price per million output tokens.
	OutputPerMTok float64
	// CacheReadPerMTok is the price per million cache-read input tokens.
	CacheReadPerMTok float64
	// CacheWritePerMTok is the price per million cache-write (creation) tokens.
	CacheWritePerMTok float64
}

// table maps a model-name PREFIX to its rates. Keys are the canonical base
// model names; lookup picks the longest key that prefixes the queried model so
// dated snapshots and speed variants resolve correctly. Anthropic cache pricing
// follows the documented multipliers: cache read ≈ 0.1× input, ephemeral cache
// write ≈ 1.25× input.
var table = map[string]Rates{
	// --- Anthropic ---
	// Fable 5: $10 / $50 per MTok.
	"claude-fable-5": {InputPerMTok: 10, OutputPerMTok: 50, CacheReadPerMTok: 1, CacheWritePerMTok: 12.5},
	// Opus 4.x: $5 / $25 per MTok.
	"claude-opus-4-8": {InputPerMTok: 5, OutputPerMTok: 25, CacheReadPerMTok: 0.5, CacheWritePerMTok: 6.25},
	"claude-opus-4-6": {InputPerMTok: 5, OutputPerMTok: 25, CacheReadPerMTok: 0.5, CacheWritePerMTok: 6.25},
	// Sonnet 4.6: $3 / $15 per MTok.
	"claude-sonnet-4-6": {InputPerMTok: 3, OutputPerMTok: 15, CacheReadPerMTok: 0.3, CacheWritePerMTok: 3.75},
	// Haiku 4.5: $1 / $5 per MTok.
	"claude-haiku-4-5": {InputPerMTok: 1, OutputPerMTok: 5, CacheReadPerMTok: 0.1, CacheWritePerMTok: 1.25},

	// --- OpenAI ---
	// gpt-4o: $2.50 / $10 per MTok; cached input $1.25.
	"gpt-4o": {InputPerMTok: 2.5, OutputPerMTok: 10, CacheReadPerMTok: 1.25},
	// gpt-4o-mini: $0.15 / $0.60 per MTok; cached input $0.075.
	"gpt-4o-mini": {InputPerMTok: 0.15, OutputPerMTok: 0.6, CacheReadPerMTok: 0.075},
	// o4-mini: $1.10 / $4.40 per MTok; cached input $0.275.
	"o4-mini": {InputPerMTok: 1.1, OutputPerMTok: 4.4, CacheReadPerMTok: 0.275},
}

// sortedKeys is the table keys ordered longest-first, so prefix matching prefers
// the most specific model name (e.g. "claude-opus-4-8" before a shorter key).
var sortedKeys = func() []string {
	keys := make([]string, 0, len(table))
	for k := range table {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	return keys
}()

// Lookup returns the rates for a model name, tolerating date/variant suffixes by
// longest-prefix match. ok is false for models not in the table.
func Lookup(model string) (Rates, bool) {
	if model == "" {
		return Rates{}, false
	}
	m := strings.ToLower(strings.TrimSpace(model))
	if r, ok := table[m]; ok {
		return r, true
	}
	for _, k := range sortedKeys {
		if strings.HasPrefix(m, k) {
			return table[k], true
		}
	}
	return Rates{}, false
}

// Cost returns the dollar cost of a usage record for a model, and ok=false when
// the model is unknown (so the UI can omit cost rather than show $0).
func Cost(model string, u llmparse.NormalizedUsage) (float64, bool) {
	r, ok := Lookup(model)
	if !ok {
		return 0, false
	}
	const perMTok = 1_000_000.0
	cost := float64(u.InputTokens)*r.InputPerMTok +
		float64(u.OutputTokens)*r.OutputPerMTok +
		float64(u.CacheReadTokens)*r.CacheReadPerMTok +
		float64(u.CacheWriteTokens)*r.CacheWritePerMTok
	return cost / perMTok, true
}
