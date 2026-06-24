package llmparse

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Provider names the upstream API family the request targets. New analyzers
// add a new constant. The set is open — the session aggregator only uses it
// to disambiguate fingerprints between providers.
const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
	ProviderGemini    = "gemini"
)

// NormalizedRequest is the provider-neutral view used by session aggregation
// and by anything that needs to compare two requests structurally. Every
// provider-specific analyzer should emit one in addition to its
// provider-specific tree.
//
// The point of this shape is *not* to replace the provider-specific tree.
// It's a small, common-denominator projection: enough to identify "same
// conversation, next turn", not enough to reproduce the request faithfully.
type NormalizedRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
	// System is the concatenated text of every system block, joined with a
	// newline. Used in the session fingerprint.
	System string `json:"system,omitempty"`
	// Stream reports whether the request asked for a streamed response.
	Stream   bool                `json:"stream,omitempty"`
	Messages []NormalizedMessage `json:"messages,omitempty"`
}

// NormalizedUsage is the provider-neutral token accounting used by session
// rollups and pricing. Each analyzer fills it from its provider-specific usage
// so downstream consumers never branch on provider. CacheRead/CacheWrite map to
// Anthropic's cache_read/cache_creation and OpenAI's prompt_tokens_details
// cached tokens (OpenAI exposes no cache-write figure, so CacheWrite stays 0).
type NormalizedUsage struct {
	InputTokens      int `json:"inputTokens,omitempty"`
	OutputTokens     int `json:"outputTokens,omitempty"`
	CacheReadTokens  int `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int `json:"cacheWriteTokens,omitempty"`
}

// Normalized stop_reason values. Provider-specific reasons collapse onto this
// small enum so the UI and rollups don't branch per provider.
const (
	StopEndTurn   = "end_turn"
	StopToolUse   = "tool_use"
	StopMaxTokens = "max_tokens"
	StopStop      = "stop_sequence"
	StopOther     = "other"
)

// normalizeStopReason maps a provider stop_reason onto the shared enum.
// Anthropic: end_turn|tool_use|max_tokens|stop_sequence. OpenAI:
// stop|tool_calls|length|function_call|content_filter.
func normalizeStopReason(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "end_turn", "stop":
		return StopEndTurn
	case "tool_use", "tool_calls", "function_call":
		return StopToolUse
	case "max_tokens", "length":
		return StopMaxTokens
	case "stop_sequence":
		return StopStop
	case "":
		return ""
	default:
		return StopOther
	}
}

// NormalizedError is the provider-neutral error projection. Type/Code/Message
// map onto whatever envelope the provider used (Anthropic error.type, OpenAI
// error.type + error.code).
type NormalizedError struct {
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// NormalizedToolCall is a provider-neutral projection of a single tool/function
// call: a stable id, the tool name, and the arguments serialized as a JSON
// string (the wire form both providers ultimately carry).
type NormalizedToolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// NormalizedMessage strips a single role-tagged message down to what session
// aggregation needs: role + a short fingerprint of the textual content. The
// fingerprint is a sha256 of the concatenated text-bearing blocks, so two
// requests that share a prefix of messages share their first N fingerprints
// exactly.
type NormalizedMessage struct {
	Role        string `json:"role"`
	Fingerprint string `json:"fingerprint"`
}

// Fingerprint computes a stable hash of the conversation anchor (provider +
// system + first user message). Two requests with the same Fingerprint
// belong to the same session candidate; aggregator still needs to verify
// message-prefix containment before merging them.
func (r *NormalizedRequest) Fingerprint() string {
	if r == nil {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(r.Provider))
	h.Write([]byte{0})
	h.Write([]byte(r.Model))
	h.Write([]byte{0})
	h.Write([]byte(r.System))
	h.Write([]byte{0})
	for _, m := range r.Messages {
		if m.Role == "user" {
			h.Write([]byte(m.Fingerprint))
			break
		}
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// anthropicToNormalized projects an AnthropicRequest into the common shape.
// Sibling analyzers (openai.go, gemini.go) should follow the same pattern.
func anthropicToNormalized(req *AnthropicRequest) *NormalizedRequest {
	if req == nil {
		return nil
	}
	system := ""
	for i, blk := range req.System {
		if i > 0 {
			system += "\n"
		}
		system += blk.Text
	}
	out := &NormalizedRequest{
		Provider: ProviderAnthropic,
		Model:    req.Model,
		System:   system,
		Stream:   req.Stream,
	}
	for _, m := range req.Messages {
		out.Messages = append(out.Messages, NormalizedMessage{
			Role:        m.Role,
			Fingerprint: fingerprintBlocks(m.Content),
		})
	}
	return out
}

// fingerprintBlocks reduces a content slice to a sha256 hex of its
// "user-visible payload" — text + tool_use ids + tool_result content. We
// deliberately ignore cache_control so two requests differing only by their
// cache annotations still collide.
func fingerprintBlocks(blocks []AnthropicBlock) string {
	h := sha256.New()
	for _, blk := range blocks {
		h.Write([]byte(blk.Type))
		h.Write([]byte{0})
		switch blk.Type {
		case "text", "thinking", "redacted_thinking":
			h.Write([]byte(blk.Text))
		case "tool_use":
			h.Write([]byte(blk.ID))
			h.Write([]byte{0})
			h.Write([]byte(blk.Name))
		case "tool_result":
			h.Write([]byte(blk.ToolUseID))
			h.Write([]byte{0})
			h.Write([]byte(stringifyContent(blk.Content)))
		}
		h.Write([]byte{0xff})
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

func stringifyContent(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []any:
		var sb strings.Builder
		for _, item := range x {
			if m, ok := item.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					sb.WriteString(s)
				}
			}
		}
		return sb.String()
	default:
		return ""
	}
}
