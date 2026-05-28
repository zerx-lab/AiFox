package llmparse

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// Skeleton OpenAI analyzer. Today it only fills NormalizedRequest so the
// session aggregator can group consecutive /v1/chat/completions calls under
// one session; the rich provider-specific Analysis (akin to Anthropic) is
// left for a follow-up. Returning a half-populated Analysis is preferable
// to nil because the renderer still gets endpoint + normalized + warnings,
// and the OpenAI raw view falls back to the JSON / HTTP tabs unchanged.

func isOpenAIChat(in Input) bool {
	if !strings.EqualFold(in.Method, "POST") {
		return false
	}
	path := trimQuery(in.URL)
	return strings.HasSuffix(path, "/v1/chat/completions") || strings.HasSuffix(path, "/chat/completions")
}

func analyzeOpenAIChat(in Input) *Analysis {
	out := &Analysis{
		Kind:     "openai.chat",
		Endpoint: "POST " + trimQuery(in.URL),
	}
	if in.RequestBody == "" {
		return out
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(in.RequestBody), &raw); err != nil {
		out.Warnings = append(out.Warnings, "could not parse OpenAI request as JSON: "+err.Error())
		return out
	}
	out.Normalized = openaiToNormalized(raw)
	return out
}

func openaiToNormalized(raw map[string]any) *NormalizedRequest {
	out := &NormalizedRequest{Provider: ProviderOpenAI}
	if v, ok := raw["model"].(string); ok {
		out.Model = v
	}
	// In OpenAI's chat schema the "system" message is just the first message
	// with role=="system". Lift it into out.System so the fingerprint shape
	// matches Anthropic's.
	if arr, ok := raw["messages"].([]any); ok {
		for _, m := range arr {
			msg, ok := m.(map[string]any)
			if !ok {
				continue
			}
			role, _ := msg["role"].(string)
			content := openaiMessageText(msg["content"])
			if role == "system" && out.System == "" {
				out.System = content
				// Don't push the system message into normalized.Messages —
				// matches the Anthropic shape, which keeps system out of the
				// messages array.
				continue
			}
			out.Messages = append(out.Messages, NormalizedMessage{
				Role:        role,
				Fingerprint: openaiFingerprintMessage(role, content, msg),
			})
		}
	}
	return out
}

// openaiMessageText collapses OpenAI's "content" — which may be a string,
// an array of parts ([{type:"text",text:"…"}, …]), or null — into a single
// string for fingerprinting purposes.
func openaiMessageText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []any:
		var sb strings.Builder
		for _, item := range x {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if s, ok := m["text"].(string); ok {
				sb.WriteString(s)
			}
		}
		return sb.String()
	default:
		return ""
	}
}

func openaiFingerprintMessage(role, content string, msg map[string]any) string {
	h := sha256.New()
	h.Write([]byte(role))
	h.Write([]byte{0})
	h.Write([]byte(content))
	if calls, ok := msg["tool_calls"].([]any); ok {
		for _, c := range calls {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			if id, ok := cm["id"].(string); ok {
				h.Write([]byte{0xfe})
				h.Write([]byte(id))
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}
