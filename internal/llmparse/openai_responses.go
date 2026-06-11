package llmparse

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// OpenAI Responses API analyzer (Codex CLI). Fills the provider-specific
// ResponsesAnalysis (request + response, streamed or not) and the shared
// Normalized / Usage / StopReason / Error projections, the same way the
// Anthropic and Chat-Completions analyzers do.

// isOpenAIResponses matches POST /v1/responses. The proxy strips the upstream
// host, so a suffix check on the path is enough. GET /v1/responses/{id} and
// other sub-paths are intentionally NOT matched: they pass through unparsed.
func isOpenAIResponses(in Input) bool {
	if !strings.EqualFold(in.Method, "POST") {
		return false
	}
	path := trimQuery(in.URL)
	return strings.HasSuffix(path, "/v1/responses") || strings.HasSuffix(path, "/responses") || path == "v1/responses"
}

// knownResponsesRequestKeys is the set of top-level request fields we map into
// structured slots; anything else lands in UnknownFields with a warning.
var knownResponsesRequestKeys = map[string]bool{
	"model": true, "input": true, "instructions": true, "tools": true,
	"previous_response_id": true, "store": true, "stream": true,
	"max_output_tokens": true, "temperature": true, "reasoning": true,
	"text": true, "tool_choice": true, "top_p": true, "metadata": true,
	"parallel_tool_calls": true, "include": true, "truncation": true,
}

func analyzeOpenAIResponses(in Input) *Analysis {
	out := &Analysis{
		Kind:      KindOpenAIResponses,
		Endpoint:  "POST " + trimQuery(in.URL),
		Responses: &ResponsesAnalysis{},
	}
	if in.RequestBody != "" {
		var raw map[string]any
		if err := json.Unmarshal([]byte(in.RequestBody), &raw); err != nil {
			out.Warnings = append(out.Warnings, "could not parse Responses request as JSON: "+err.Error())
		} else {
			req, warnings := parseResponsesRequest(raw)
			out.Responses.Request = req
			out.Warnings = append(out.Warnings, warnings...)
			out.Normalized = responsesToNormalized(req)
		}
	}
	if in.ResponseBody != "" {
		ct := strings.ToLower(in.ResponseHeaders["content-type"])
		if in.Streaming || strings.HasPrefix(ct, "text/event-stream") {
			resp, warnings := parseResponsesSSE(in.ResponseBody)
			out.Responses.Response = resp
			out.Warnings = append(out.Warnings, warnings...)
		} else {
			resp, warnings := parseResponsesResponse(in.ResponseBody)
			out.Responses.Response = resp
			out.Warnings = append(out.Warnings, warnings...)
		}
	}
	fillResponsesNormalized(out)
	return out
}

// parseResponsesRequest maps a Responses request body into ResponsesRequest.
func parseResponsesRequest(raw map[string]any) (*ResponsesRequest, []string) {
	var warnings []string
	req := &ResponsesRequest{}
	req.Model, _ = raw["model"].(string)
	req.Instructions, _ = raw["instructions"].(string)
	req.PreviousResponseID, _ = raw["previous_response_id"].(string)
	if b, ok := raw["store"].(bool); ok {
		req.Store = &b
	}
	if b, ok := raw["stream"].(bool); ok {
		req.Stream = b
	}
	if n, ok := raw["max_output_tokens"].(float64); ok {
		req.MaxOutputTokens = int(n)
	}
	if n, ok := raw["temperature"].(float64); ok {
		req.Temperature = &n
	}
	if r, ok := raw["reasoning"].(map[string]any); ok {
		rc := &ResponsesReasoningConfig{}
		rc.Effort, _ = r["effort"].(string)
		rc.Summary, _ = r["summary"].(string)
		req.Reasoning = rc
	}
	if txt, ok := raw["text"]; ok {
		req.Text = txt
	}
	req.Input = parseResponsesInput(raw["input"], &warnings)
	req.Tools = parseResponsesTools(raw["tools"])
	for k, v := range raw {
		if !knownResponsesRequestKeys[k] {
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

// parseResponsesInput normalizes the `input` field, which may be a plain string
// (treated as a single user message) or an array of items.
func parseResponsesInput(v any, warnings *[]string) []ResponsesItem {
	switch in := v.(type) {
	case nil:
		return nil
	case string:
		if in == "" {
			return nil
		}
		return []ResponsesItem{{
			Type:    "message",
			Role:    "user",
			Content: in,
			Parts:   []ResponsesPart{{Type: "input_text", Text: in}},
		}}
	case []any:
		var out []ResponsesItem
		for _, raw := range in {
			m, ok := raw.(map[string]any)
			if !ok {
				if warnings != nil {
					*warnings = append(*warnings, "skipped a non-object input item")
				}
				continue
			}
			out = append(out, parseResponsesItem(m))
		}
		return out
	default:
		if warnings != nil {
			*warnings = append(*warnings, "input field has an unexpected type")
		}
		return nil
	}
}

// parseResponsesItem maps a single input/output item. Message items collapse
// their content parts; function_call / function_call_output / reasoning items
// pull their salient fields; anything else is preserved in Raw.
func parseResponsesItem(m map[string]any) ResponsesItem {
	item := ResponsesItem{}
	item.Type, _ = m["type"].(string)
	// Items without an explicit type but with a role are messages (the common
	// chat-style input shape).
	if item.Type == "" {
		if _, ok := m["role"]; ok {
			item.Type = "message"
		}
	}
	switch item.Type {
	case "message":
		item.Role, _ = m["role"].(string)
		item.Status, _ = m["status"].(string)
		item.Parts = parseResponsesParts(m["content"])
		item.Content = flattenResponsesParts(item.Parts)
		if item.Content == "" {
			// Plain string content shorthand.
			if s, ok := m["content"].(string); ok {
				item.Content = s
				if len(item.Parts) == 0 {
					item.Parts = []ResponsesPart{{Type: "input_text", Text: s}}
				}
			}
		}
	case "function_call":
		item.ID, _ = m["id"].(string)
		item.CallID, _ = m["call_id"].(string)
		item.Name, _ = m["name"].(string)
		item.Arguments, _ = m["arguments"].(string)
		item.Status, _ = m["status"].(string)
	case "function_call_output":
		item.CallID, _ = m["call_id"].(string)
		item.Output = stringifyResponsesOutput(m["output"])
	case "reasoning":
		item.ID, _ = m["id"].(string)
		item.Status, _ = m["status"].(string)
		item.Summary = stringifyReasoningSummary(m["summary"])
	default:
		item.Raw = m
	}
	return item
}

// parseResponsesParts maps a message item's content, which may be a string or
// an array of typed parts (input_text / output_text / refusal / input_image).
func parseResponsesParts(v any) []ResponsesPart {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []ResponsesPart
	for _, raw := range arr {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		part := ResponsesPart{}
		part.Type, _ = m["type"].(string)
		switch part.Type {
		case "input_text", "output_text", "text":
			part.Text, _ = m["text"].(string)
		case "refusal":
			part.Text, _ = m["refusal"].(string)
		case "input_image":
			img := &ResponsesImage{}
			img.URL, _ = m["image_url"].(string)
			img.Detail, _ = m["detail"].(string)
			part.Image = img
		default:
			part.Raw = m
		}
		out = append(out, part)
	}
	return out
}

// flattenResponsesParts joins the text of every text-bearing part with newlines.
func flattenResponsesParts(parts []ResponsesPart) string {
	var sb strings.Builder
	for _, p := range parts {
		if p.Text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(p.Text)
	}
	return sb.String()
}

// stringifyResponsesOutput collapses a function_call_output's `output` field
// (string, or an array of {type:output_text,text} parts) into a string.
func stringifyResponsesOutput(v any) string {
	switch o := v.(type) {
	case string:
		return o
	case []any:
		var sb strings.Builder
		for _, raw := range o {
			if m, ok := raw.(map[string]any); ok {
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

// stringifyReasoningSummary collapses a reasoning item's `summary` (array of
// {type:summary_text,text}) into a string.
func stringifyReasoningSummary(v any) string {
	arr, ok := v.([]any)
	if !ok {
		if s, ok := v.(string); ok {
			return s
		}
		return ""
	}
	var sb strings.Builder
	for _, raw := range arr {
		if m, ok := raw.(map[string]any); ok {
			if s, ok := m["text"].(string); ok {
				if sb.Len() > 0 {
					sb.WriteByte('\n')
				}
				sb.WriteString(s)
			}
		}
	}
	return sb.String()
}

// parseResponsesTools reads the request's tools array. Responses function tools
// are flat ({type:"function",name,description,parameters}); built-in tools
// (web_search, file_search, …) are preserved in Raw with their Type recorded.
func parseResponsesTools(v any) []ResponsesTool {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []ResponsesTool
	for _, raw := range arr {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		tool := ResponsesTool{}
		tool.Type, _ = m["type"].(string)
		if tool.Type == "function" {
			tool.Name, _ = m["name"].(string)
			tool.Description, _ = m["description"].(string)
			tool.Parameters = m["parameters"]
		} else {
			// Built-in tool type: keep the raw definition for the renderer.
			tool.Raw = m
		}
		out = append(out, tool)
	}
	return out
}

// parseResponsesResponse maps a non-streamed JSON response.
func parseResponsesResponse(body string) (*ResponsesResponse, []string) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, []string{"response is not valid JSON: " + err.Error()}
	}
	// A bare top-level error envelope (HTTP 4xx/5xx body).
	if errObj, ok := raw["error"].(map[string]any); ok && raw["object"] == nil && raw["output"] == nil {
		return &ResponsesResponse{Error: parseOpenAIError(errObj)}, nil
	}
	return responseFromMap(raw), nil
}

// responseFromMap builds a ResponsesResponse from a decoded `response` object.
// Shared by the non-streamed parser and the SSE parser (which receives the same
// shape inside response.completed / .failed / .incomplete events).
func responseFromMap(raw map[string]any) *ResponsesResponse {
	resp := &ResponsesResponse{}
	resp.ID, _ = raw["id"].(string)
	resp.Model, _ = raw["model"].(string)
	resp.Status, _ = raw["status"].(string)
	if errObj, ok := raw["error"].(map[string]any); ok {
		resp.Error = parseOpenAIError(errObj)
	}
	if inc, ok := raw["incomplete_details"].(map[string]any); ok {
		reason, _ := inc["reason"].(string)
		resp.Incomplete = &ResponsesIncomplete{Reason: reason}
	}
	if arr, ok := raw["output"].([]any); ok {
		for _, o := range arr {
			if m, ok := o.(map[string]any); ok {
				resp.Output = append(resp.Output, parseResponsesItem(m))
			}
		}
	}
	if u, ok := raw["usage"].(map[string]any); ok {
		resp.Usage = parseResponsesUsage(u)
	}
	return resp
}

// parseResponsesUsage maps the usage object. Note the Responses-specific field
// names (input_tokens / output_tokens) and the nested cached/reasoning details.
func parseResponsesUsage(m map[string]any) *ResponsesUsage {
	u := &ResponsesUsage{}
	if n, ok := m["input_tokens"].(float64); ok {
		u.InputTokens = int(n)
	}
	if n, ok := m["output_tokens"].(float64); ok {
		u.OutputTokens = int(n)
	}
	if n, ok := m["total_tokens"].(float64); ok {
		u.TotalTokens = int(n)
	}
	if d, ok := m["input_tokens_details"].(map[string]any); ok {
		if n, ok := d["cached_tokens"].(float64); ok {
			u.CachedTokens = int(n)
		}
	}
	if d, ok := m["output_tokens_details"].(map[string]any); ok {
		if n, ok := d["reasoning_tokens"].(float64); ok {
			u.ReasoningTokens = int(n)
		}
	}
	return u
}

// fillResponsesNormalized projects the parsed Responses response onto the shared
// Usage / StopReason / Error fields. cached_tokens maps to CacheReadTokens; the
// Responses API has no cache-write concept so CacheWriteTokens stays 0.
// InputTokens is the uncached remainder, mirroring the Anthropic/Chat split.
func fillResponsesNormalized(out *Analysis) {
	if out.Responses == nil || out.Responses.Response == nil {
		return
	}
	resp := out.Responses.Response
	out.StopReason = responsesStopReason(resp)
	if e := resp.Error; e != nil {
		out.Error = &NormalizedError{Type: e.Type, Code: e.Code, Message: e.Message}
	}
	if u := resp.Usage; u != nil {
		input := u.InputTokens - u.CachedTokens
		if input < 0 {
			input = 0
		}
		out.Usage = &NormalizedUsage{
			InputTokens:     input,
			OutputTokens:    u.OutputTokens,
			CacheReadTokens: u.CachedTokens,
		}
	}
}

// responsesStopReason maps a Responses status + output shape onto the shared
// stop enum: a function_call output means tool_use; an incomplete status capped
// by max_output_tokens means max_tokens; completed means end_turn; failed/error
// falls back to other.
func responsesStopReason(resp *ResponsesResponse) string {
	for _, item := range resp.Output {
		if item.Type == "function_call" {
			return StopToolUse
		}
	}
	switch strings.ToLower(resp.Status) {
	case "completed":
		return StopEndTurn
	case "incomplete":
		// max_output_tokens is the length-cap case; any other incomplete reason
		// (e.g. content_filter) isn't a token cap, so it falls back to "other".
		if resp.Incomplete != nil && strings.Contains(resp.Incomplete.Reason, "max_output_tokens") {
			return StopMaxTokens
		}
		return StopOther
	case "failed":
		return StopOther
	default:
		return ""
	}
}

// responsesToNormalized projects a ResponsesRequest into the common shape used
// by session aggregation. instructions maps to System; a string input becomes a
// single user message (already normalized in parseResponsesInput).
func responsesToNormalized(req *ResponsesRequest) *NormalizedRequest {
	if req == nil {
		return nil
	}
	out := &NormalizedRequest{
		Provider: ProviderOpenAI,
		Model:    req.Model,
		System:   req.Instructions,
		Stream:   req.Stream,
	}
	for _, item := range req.Input {
		role := item.Role
		if role == "" {
			// Non-message items (function_call etc.) carry no role; bucket them
			// under a synthetic role so their fingerprint still contributes.
			role = item.Type
		}
		out.Messages = append(out.Messages, NormalizedMessage{
			Role:        role,
			Fingerprint: responsesFingerprintItem(item),
		})
	}
	return out
}

// PreviousResponseID returns the request's previous_response_id when this is a
// Responses analysis, else "". Used by session correlation to chain stateful
// Codex turns together regardless of fingerprint.
func (a *Analysis) PreviousResponseID() string {
	if a == nil || a.Responses == nil || a.Responses.Request == nil {
		return ""
	}
	return a.Responses.Request.PreviousResponseID
}

// ResponseID returns the response's id when this is a Responses analysis with a
// parsed response, else "". The aggregator registers this id so a later
// request whose previous_response_id matches joins the same session.
func (a *Analysis) ResponseID() string {
	if a == nil || a.Responses == nil || a.Responses.Response == nil {
		return ""
	}
	return a.Responses.Response.ID
}

func responsesFingerprintItem(item ResponsesItem) string {
	h := sha256.New()
	h.Write([]byte(item.Type))
	h.Write([]byte{0})
	h.Write([]byte(item.Role))
	h.Write([]byte{0})
	h.Write([]byte(item.Content))
	switch item.Type {
	case "function_call":
		h.Write([]byte{0xfe})
		h.Write([]byte(item.CallID))
		h.Write([]byte{0})
		h.Write([]byte(item.Name))
	case "function_call_output":
		h.Write([]byte{0xfd})
		h.Write([]byte(item.CallID))
		h.Write([]byte{0})
		h.Write([]byte(item.Output))
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}
