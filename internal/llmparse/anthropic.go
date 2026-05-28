package llmparse

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Input is the captured pair we run analyzers against. Headers are
// canonicalized lower-case keys.
type Input struct {
	Method          string
	URL             string
	RequestBody     string
	ResponseBody    string
	ResponseHeaders map[string]string
	Streaming       bool
}

// Analyze inspects the captured pair and returns a structured view, or nil
// if no analyzer recognized the endpoint. Returning nil is normal; the
// renderer falls back to its raw JSON view for unknown endpoints.
func Analyze(in Input) *Analysis {
	if isAnthropicMessages(in) {
		return analyzeAnthropicMessages(in)
	}
	return nil
}

// isAnthropicMessages matches /v1/messages POSTs. The proxy strips the
// upstream host (we only see the path + query), so a single suffix check is
// enough; we don't need to know the configured baseURL.
func isAnthropicMessages(in Input) bool {
	if !strings.EqualFold(in.Method, "POST") {
		return false
	}
	path := in.URL
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	// Accept any path ending in /v1/messages (including beta variants like
	// /v1/messages?beta=...) but not /v1/messages/count_tokens etc.
	return strings.HasSuffix(path, "/v1/messages") || path == "v1/messages"
}

func analyzeAnthropicMessages(in Input) *Analysis {
	out := &Analysis{
		Kind:      KindAnthropicMessages,
		Endpoint:  fmt.Sprintf("%s %s", strings.ToUpper(in.Method), trimQuery(in.URL)),
		Anthropic: &AnthropicAnalysis{},
	}

	if req, warnings := parseAnthropicRequest(in.RequestBody); req != nil {
		out.Anthropic.Request = req
		out.Warnings = append(out.Warnings, warnings...)
	} else if in.RequestBody != "" {
		out.Warnings = append(out.Warnings, "request body is not valid JSON")
	}

	if in.ResponseBody != "" {
		ct := strings.ToLower(in.ResponseHeaders["content-type"])
		if in.Streaming || strings.HasPrefix(ct, "text/event-stream") {
			resp, warnings := parseAnthropicSSE(in.ResponseBody)
			out.Anthropic.Response = resp
			out.Warnings = append(out.Warnings, warnings...)
		} else {
			resp, warnings := parseAnthropicResponse(in.ResponseBody)
			out.Anthropic.Response = resp
			out.Warnings = append(out.Warnings, warnings...)
		}
	}

	return out
}

func trimQuery(s string) string {
	if i := strings.IndexByte(s, '?'); i >= 0 {
		return s[:i]
	}
	return s
}

// known is the set of top-level fields we map into structured slots. Any key
// outside this set goes into UnknownFields so the UI can show it.
var knownAnthropicRequestKeys = map[string]bool{
	"model":          true,
	"max_tokens":     true,
	"temperature":    true,
	"top_p":          true,
	"top_k":          true,
	"stream":         true,
	"stop_sequences": true,
	"system":         true,
	"tools":          true,
	"tool_choice":    true,
	"messages":       true,
	"metadata":       true,
}

func parseAnthropicRequest(body string) (*AnthropicRequest, []string) {
	if body == "" {
		return nil, nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, []string{"could not parse request as JSON: " + err.Error()}
	}
	var warnings []string

	req := &AnthropicRequest{}
	if v, ok := raw["model"].(string); ok {
		req.Model = v
	}
	if n, ok := raw["max_tokens"].(float64); ok {
		req.MaxTokens = int(n)
	}
	if n, ok := raw["temperature"].(float64); ok {
		req.Temperature = &n
	}
	if n, ok := raw["top_p"].(float64); ok {
		req.TopP = &n
	}
	if n, ok := raw["top_k"].(float64); ok {
		v := int(n)
		req.TopK = &v
	}
	if b, ok := raw["stream"].(bool); ok {
		req.Stream = b
	}
	if arr, ok := raw["stop_sequences"].([]any); ok {
		req.StopSequences = stringArray(arr)
	}
	if v, ok := raw["system"]; ok {
		req.System = parseSystem(v)
	}
	if arr, ok := raw["tools"].([]any); ok {
		for _, t := range arr {
			req.Tools = append(req.Tools, parseTool(t))
		}
	}
	if v, ok := raw["tool_choice"]; ok {
		req.ToolChoice = v
	}
	if arr, ok := raw["messages"].([]any); ok {
		for _, m := range arr {
			if msg, ok := parseMessage(m); ok {
				req.Messages = append(req.Messages, msg)
			} else {
				warnings = append(warnings, "skipped a non-object message")
			}
		}
	}
	if v, ok := raw["metadata"]; ok {
		req.Metadata = v
	}

	for k, v := range raw {
		if !knownAnthropicRequestKeys[k] {
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

// parseSystem accepts either a plain string ("the system prompt") or the
// newer array-of-blocks form. Returns a slice so the renderer has one path
// to handle.
func parseSystem(v any) []AnthropicBlock {
	switch sys := v.(type) {
	case string:
		return []AnthropicBlock{{Type: "text", Text: sys}}
	case []any:
		out := make([]AnthropicBlock, 0, len(sys))
		for _, b := range sys {
			out = append(out, parseBlock(b))
		}
		return out
	default:
		return nil
	}
}

func parseTool(v any) AnthropicTool {
	m, ok := v.(map[string]any)
	if !ok {
		return AnthropicTool{Raw: v}
	}
	tool := AnthropicTool{}
	if name, ok := m["name"].(string); ok {
		tool.Name = name
	}
	if desc, ok := m["description"].(string); ok {
		tool.Description = desc
	}
	if schema, ok := m["input_schema"]; ok {
		tool.InputSchema = schema
	}
	// Provider-specific server tools (e.g. computer_use, web_search) don't
	// follow the name/description/input_schema shape — keep the raw object so
	// the UI can show whatever fields they have.
	if tool.Name == "" && tool.InputSchema == nil {
		tool.Raw = v
	}
	return tool
}

func parseMessage(v any) (AnthropicMessage, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return AnthropicMessage{}, false
	}
	msg := AnthropicMessage{}
	if role, ok := m["role"].(string); ok {
		msg.Role = role
	}
	switch c := m["content"].(type) {
	case string:
		msg.Content = []AnthropicBlock{{Type: "text", Text: c}}
	case []any:
		for _, b := range c {
			msg.Content = append(msg.Content, parseBlock(b))
		}
	}
	return msg, true
}

func parseBlock(v any) AnthropicBlock {
	m, ok := v.(map[string]any)
	if !ok {
		return AnthropicBlock{Type: "unknown", Raw: v}
	}
	blk := AnthropicBlock{}
	if t, ok := m["type"].(string); ok {
		blk.Type = t
	}
	switch blk.Type {
	case "text":
		if s, ok := m["text"].(string); ok {
			blk.Text = s
		}
	case "tool_use":
		if s, ok := m["id"].(string); ok {
			blk.ID = s
		}
		if s, ok := m["name"].(string); ok {
			blk.Name = s
		}
		if x, ok := m["input"]; ok {
			blk.Input = x
		}
	case "tool_result":
		if s, ok := m["tool_use_id"].(string); ok {
			blk.ToolUseID = s
		}
		if b, ok := m["is_error"].(bool); ok {
			blk.IsError = b
		}
		if x, ok := m["content"]; ok {
			blk.Content = x
		}
	case "image":
		// Just pass through source.* in Raw; we don't want to inline base64.
		blk.Raw = m["source"]
	case "thinking", "redacted_thinking":
		if s, ok := m["thinking"].(string); ok {
			blk.Text = s
		}
		if s, ok := m["data"].(string); ok && blk.Text == "" {
			blk.Text = s
		}
	default:
		blk.Raw = v
	}
	if cc, ok := m["cache_control"]; ok {
		blk.CacheControl = cc
	}
	return blk
}

func parseAnthropicResponse(body string) (*AnthropicResponse, []string) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, []string{"response is not valid JSON: " + err.Error()}
	}
	resp := &AnthropicResponse{}
	if v, ok := raw["id"].(string); ok {
		resp.ID = v
	}
	if v, ok := raw["model"].(string); ok {
		resp.Model = v
	}
	if v, ok := raw["role"].(string); ok {
		resp.Role = v
	}
	if v, ok := raw["stop_reason"].(string); ok {
		resp.StopReason = v
	}
	if arr, ok := raw["content"].([]any); ok {
		for _, b := range arr {
			resp.Content = append(resp.Content, parseBlock(b))
		}
	}
	if u, ok := raw["usage"].(map[string]any); ok {
		resp.Usage = parseUsage(u)
	}
	if errObj, ok := raw["error"].(map[string]any); ok {
		resp.Error = &AnthropicError{}
		if s, ok := errObj["type"].(string); ok {
			resp.Error.Type = s
		}
		if s, ok := errObj["message"].(string); ok {
			resp.Error.Message = s
		}
	}
	return resp, nil
}

func parseUsage(m map[string]any) *AnthropicUsage {
	u := &AnthropicUsage{}
	if n, ok := m["input_tokens"].(float64); ok {
		u.InputTokens = int(n)
	}
	if n, ok := m["output_tokens"].(float64); ok {
		u.OutputTokens = int(n)
	}
	if n, ok := m["cache_read_input_tokens"].(float64); ok {
		u.CacheReadInputTokens = int(n)
	}
	if n, ok := m["cache_creation_input_tokens"].(float64); ok {
		u.CacheCreationInputTokens = int(n)
	}
	return u
}

func stringArray(v []any) []string {
	out := make([]string, 0, len(v))
	for _, x := range v {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
