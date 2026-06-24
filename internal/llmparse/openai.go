package llmparse

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// OpenAI Chat Completions analyzer. Fills both the provider-specific
// OpenAIAnalysis (request + response, streamed or not) and the shared
// Normalized / Usage projections. The renderer's raw view still works for
// anything the structured parser misses.

func isOpenAIChat(in Input) bool {
	if !strings.EqualFold(in.Method, "POST") {
		return false
	}
	path := trimQuery(in.URL)
	return strings.HasSuffix(path, "/v1/chat/completions") || strings.HasSuffix(path, "/chat/completions")
}

// knownOpenAIRequestKeys is the set of top-level chat-request fields we map into
// structured slots; anything else lands in UnknownFields with a warning.
var knownOpenAIRequestKeys = map[string]bool{
	"model": true, "messages": true, "tools": true, "functions": true,
	"temperature": true, "top_p": true, "max_tokens": true,
	"max_completion_tokens": true, "stream": true, "stream_options": true,
	"tool_choice": true, "function_call": true, "n": true,
	"stop": true, "presence_penalty": true, "frequency_penalty": true,
	"logit_bias": true, "user": true, "seed": true, "response_format": true,
}

func analyzeOpenAIChat(in Input) *Analysis {
	out := &Analysis{
		Kind:     KindOpenAIChat,
		Endpoint: "POST " + trimQuery(in.URL),
		OpenAI:   &OpenAIAnalysis{},
	}
	if in.RequestBody != "" {
		var raw map[string]any
		if err := json.Unmarshal([]byte(in.RequestBody), &raw); err != nil {
			out.Warnings = append(out.Warnings, "could not parse OpenAI request as JSON: "+err.Error())
		} else {
			req, warnings := parseOpenAIRequest(raw)
			out.OpenAI.Request = req
			out.Warnings = append(out.Warnings, warnings...)
			out.Normalized = openaiToNormalized(raw)
		}
	}
	if in.ResponseBody != "" {
		ct := strings.ToLower(in.ResponseHeaders["content-type"])
		if in.Streaming || strings.HasPrefix(ct, "text/event-stream") {
			resp, warnings := parseOpenAISSE(in.ResponseBody)
			out.OpenAI.Response = resp
			out.Warnings = append(out.Warnings, warnings...)
		} else {
			resp, warnings := parseOpenAIResponse(in.ResponseBody)
			out.OpenAI.Response = resp
			out.Warnings = append(out.Warnings, warnings...)
		}
	}
	fillOpenAINormalized(out)
	return out
}

// parseOpenAIRequest maps a chat-completions request body into OpenAIRequest.
func parseOpenAIRequest(raw map[string]any) (*OpenAIRequest, []string) {
	var warnings []string
	req := &OpenAIRequest{}
	if v, ok := raw["model"].(string); ok {
		req.Model = v
	}
	if n, ok := raw["temperature"].(float64); ok {
		req.Temperature = &n
	}
	if n, ok := raw["top_p"].(float64); ok {
		req.TopP = &n
	}
	if n, ok := raw["max_tokens"].(float64); ok {
		req.MaxTokens = int(n)
	}
	if n, ok := raw["max_completion_tokens"].(float64); ok && req.MaxTokens == 0 {
		req.MaxTokens = int(n)
	}
	if b, ok := raw["stream"].(bool); ok {
		req.Stream = b
	}
	if so, ok := raw["stream_options"].(map[string]any); ok {
		if b, ok := so["include_usage"].(bool); ok {
			req.IncludeUsage = b
		}
	}
	if arr, ok := raw["messages"].([]any); ok {
		for _, m := range arr {
			if msg, ok := m.(map[string]any); ok {
				req.Messages = append(req.Messages, parseOpenAIMessage(msg))
			} else {
				warnings = append(warnings, "skipped a non-object message")
			}
		}
	}
	req.Tools = parseOpenAITools(raw)
	for k, v := range raw {
		if !knownOpenAIRequestKeys[k] {
			if req.UnknownFields == nil {
				req.UnknownFields = map[string]any{}
			}
			req.UnknownFields[k] = v
		}
	}
	if len(req.UnknownFields) > 0 {
		warnings = append(warnings, fmt.Sprintf("request has %d unknown top-level field(s)", len(req.UnknownFields)))
	}
	return req, warnings
}

// parseOpenAITools reads both the modern `tools` array (each {type,function})
// and the legacy top-level `functions` array.
func parseOpenAITools(raw map[string]any) []OpenAITool {
	var out []OpenAITool
	if arr, ok := raw["tools"].([]any); ok {
		for _, t := range arr {
			m, ok := t.(map[string]any)
			if !ok {
				continue
			}
			tool := OpenAITool{}
			if s, ok := m["type"].(string); ok {
				tool.Type = s
			}
			if fn, ok := m["function"].(map[string]any); ok {
				tool.Name, _ = fn["name"].(string)
				tool.Description, _ = fn["description"].(string)
				tool.Parameters = fn["parameters"]
			}
			out = append(out, tool)
		}
	}
	if arr, ok := raw["functions"].([]any); ok {
		for _, f := range arr {
			m, ok := f.(map[string]any)
			if !ok {
				continue
			}
			tool := OpenAITool{Type: "function"}
			tool.Name, _ = m["name"].(string)
			tool.Description, _ = m["description"].(string)
			tool.Parameters = m["parameters"]
			out = append(out, tool)
		}
	}
	return out
}

// parseOpenAIMessage maps one message object, handling string/array content,
// modern tool_calls, and legacy function_call.
func parseOpenAIMessage(m map[string]any) OpenAIMessage {
	msg := OpenAIMessage{}
	msg.Role, _ = m["role"].(string)
	msg.Content = openaiMessageText(m["content"])
	msg.Name, _ = m["name"].(string)
	msg.ToolCallID, _ = m["tool_call_id"].(string)
	if calls, ok := m["tool_calls"].([]any); ok {
		for _, c := range calls {
			if tc, ok := parseOpenAIToolCall(c); ok {
				msg.ToolCalls = append(msg.ToolCalls, tc)
			}
		}
	}
	if fc, ok := m["function_call"].(map[string]any); ok {
		call := &OpenAIFunctionCall{}
		call.Name, _ = fc["name"].(string)
		call.Arguments, _ = fc["arguments"].(string)
		msg.FunctionCall = call
	}
	return msg
}

// parseOpenAIToolCall maps a single tool_calls entry ({id,type,function}).
func parseOpenAIToolCall(c any) (OpenAIToolCall, bool) {
	m, ok := c.(map[string]any)
	if !ok {
		return OpenAIToolCall{}, false
	}
	tc := OpenAIToolCall{}
	tc.ID, _ = m["id"].(string)
	tc.Type, _ = m["type"].(string)
	if fn, ok := m["function"].(map[string]any); ok {
		tc.Name, _ = fn["name"].(string)
		tc.Arguments, _ = fn["arguments"].(string)
	}
	return tc, true
}

// parseOpenAIResponse maps a non-streamed chat-completions response.
func parseOpenAIResponse(body string) (*OpenAIResponse, []string) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, []string{"response is not valid JSON: " + err.Error()}
	}
	resp := &OpenAIResponse{}
	resp.ID, _ = raw["id"].(string)
	resp.Model, _ = raw["model"].(string)
	if errObj, ok := raw["error"].(map[string]any); ok {
		resp.Error = parseOpenAIError(errObj)
	}
	if arr, ok := raw["choices"].([]any); ok {
		for _, c := range arr {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			ch := OpenAIChoice{}
			if n, ok := cm["index"].(float64); ok {
				ch.Index = int(n)
			}
			ch.FinishReason, _ = cm["finish_reason"].(string)
			if msg, ok := cm["message"].(map[string]any); ok {
				m := parseOpenAIMessage(msg)
				ch.Message = &m
			}
			if t, ok := cm["text"].(string); ok {
				ch.Text = t
			}
			resp.Choices = append(resp.Choices, ch)
		}
	}
	if u, ok := raw["usage"].(map[string]any); ok {
		resp.Usage = parseOpenAIUsage(u)
	}
	return resp, nil
}

// parseOpenAIUsage maps the usage object, including
// prompt_tokens_details.cached_tokens.
func parseOpenAIUsage(m map[string]any) *OpenAIUsage {
	u := &OpenAIUsage{}
	if n, ok := m["prompt_tokens"].(float64); ok {
		u.PromptTokens = int(n)
	}
	if n, ok := m["completion_tokens"].(float64); ok {
		u.CompletionTokens = int(n)
	}
	if n, ok := m["total_tokens"].(float64); ok {
		u.TotalTokens = int(n)
	}
	if d, ok := m["prompt_tokens_details"].(map[string]any); ok {
		if n, ok := d["cached_tokens"].(float64); ok {
			u.CachedTokens = int(n)
		}
	}
	return u
}

func parseOpenAIError(m map[string]any) *OpenAIError {
	e := &OpenAIError{}
	e.Type, _ = m["type"].(string)
	e.Message, _ = m["message"].(string)
	// code may be a string or a number; coerce to string.
	switch c := m["code"].(type) {
	case string:
		e.Code = c
	case float64:
		e.Code = fmt.Sprintf("%g", c)
	}
	return e
}

// fillOpenAINormalized projects the parsed OpenAI response onto the shared
// Usage / StopReason / Error fields. PromptTokens includes cached tokens on the
// wire, so InputTokens is the uncached remainder to mirror Anthropic's split.
func fillOpenAINormalized(out *Analysis) {
	if out.OpenAI == nil || out.OpenAI.Response == nil {
		return
	}
	resp := out.OpenAI.Response
	for _, ch := range resp.Choices {
		if ch.FinishReason != "" {
			out.StopReason = normalizeStopReason(ch.FinishReason)
			break
		}
	}
	if e := resp.Error; e != nil {
		out.Error = &NormalizedError{Type: e.Type, Code: e.Code, Message: e.Message}
	}
	if u := resp.Usage; u != nil {
		input := u.PromptTokens - u.CachedTokens
		if input < 0 {
			input = 0
		}
		out.Usage = &NormalizedUsage{
			InputTokens:     input,
			OutputTokens:    u.CompletionTokens,
			CacheReadTokens: u.CachedTokens,
		}
	}
}

// isOpenAICompletions matches POST /v1/completions (legacy text completion).
func isOpenAICompletions(in Input) bool {
	if !strings.EqualFold(in.Method, "POST") {
		return false
	}
	path := trimQuery(in.URL)
	// Guard against /chat/completions, which isOpenAIChat owns.
	if strings.HasSuffix(path, "/chat/completions") {
		return false
	}
	return strings.HasSuffix(path, "/v1/completions") || strings.HasSuffix(path, "/completions")
}

// analyzeOpenAICompletions does a minimal parse of the legacy text-completion
// endpoint: prompt, choices[].text, usage. No session aggregation tuning.
func analyzeOpenAICompletions(in Input) *Analysis {
	out := &Analysis{
		Kind:     KindOpenAICompletions,
		Endpoint: "POST " + trimQuery(in.URL),
		OpenAI:   &OpenAIAnalysis{},
	}
	if in.RequestBody != "" {
		var raw map[string]any
		if err := json.Unmarshal([]byte(in.RequestBody), &raw); err != nil {
			out.Warnings = append(out.Warnings, "could not parse request as JSON: "+err.Error())
		} else {
			req := &OpenAIRequest{}
			req.Model, _ = raw["model"].(string)
			if b, ok := raw["stream"].(bool); ok {
				req.Stream = b
			}
			req.Prompt = flattenPrompt(raw["prompt"])
			out.OpenAI.Request = req
			out.Normalized = &NormalizedRequest{Provider: ProviderOpenAI, Model: req.Model, Stream: req.Stream}
		}
	}
	if in.ResponseBody != "" {
		resp, warnings := parseOpenAIResponse(in.ResponseBody)
		out.OpenAI.Response = resp
		out.Warnings = append(out.Warnings, warnings...)
		fillOpenAINormalized(out)
	}
	return out
}

// flattenPrompt collapses the legacy prompt (string or array of strings) into a
// single newline-joined string.
func flattenPrompt(v any) string {
	switch p := v.(type) {
	case string:
		return p
	case []any:
		var parts []string
		for _, item := range p {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func openaiToNormalized(raw map[string]any) *NormalizedRequest {
	out := &NormalizedRequest{Provider: ProviderOpenAI}
	if v, ok := raw["model"].(string); ok {
		out.Model = v
	}
	if b, ok := raw["stream"].(bool); ok {
		out.Stream = b
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
	// Legacy single function_call object: fold name into the fingerprint so two
	// otherwise-identical messages that differ only by the function called don't
	// collide.
	if fc, ok := msg["function_call"].(map[string]any); ok {
		if name, ok := fc["name"].(string); ok {
			h.Write([]byte{0xfd})
			h.Write([]byte(name))
		}
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}
