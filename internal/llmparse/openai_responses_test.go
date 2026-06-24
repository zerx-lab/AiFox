package llmparse

import (
	"strings"
	"testing"
)

func TestResponses_routing(t *testing.T) {
	// POST /v1/responses is parsed; GET and sub-paths are not.
	if !isOpenAIResponses(Input{Method: "POST", URL: "/v1/responses"}) {
		t.Fatalf("POST /v1/responses should match")
	}
	if isOpenAIResponses(Input{Method: "GET", URL: "/v1/responses/resp_1"}) {
		t.Fatalf("GET sub-path should not match")
	}
	if isOpenAIResponses(Input{Method: "POST", URL: "/v1/responses/resp_1/cancel"}) {
		t.Fatalf("POST sub-path should not match")
	}
}

func TestResponses_nonStreaming(t *testing.T) {
	req := `{
		"model":"gpt-5-codex",
		"instructions":"You are a coding agent.",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"list files"}]}
		],
		"tools":[
			{"type":"function","name":"bash","description":"run a command","parameters":{"type":"object"}},
			{"type":"web_search"}
		],
		"previous_response_id":"resp_prev",
		"store":true,
		"max_output_tokens":2048,
		"temperature":0.2,
		"reasoning":{"effort":"medium"},
		"future_field":true
	}`
	resp := `{
		"id":"resp_1","model":"gpt-5-codex","status":"completed",
		"output":[
			{"type":"reasoning","summary":[{"type":"summary_text","text":"thinking"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"here you go"}]},
			{"type":"function_call","call_id":"call_1","name":"bash","arguments":"{\"cmd\":\"ls\"}"}
		],
		"usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120,
			"input_tokens_details":{"cached_tokens":40},
			"output_tokens_details":{"reasoning_tokens":8}}
	}`
	a := Analyze(Input{
		Method: "POST", URL: "/v1/responses",
		RequestBody: req, ResponseBody: resp,
		ResponseHeaders: map[string]string{"content-type": "application/json"},
	})
	if a == nil || a.Kind != KindOpenAIResponses || a.Responses == nil {
		t.Fatalf("expected openai.responses analysis, got %+v", a)
	}
	r := a.Responses.Request
	if r == nil || r.Model != "gpt-5-codex" || r.Instructions != "You are a coding agent." {
		t.Fatalf("request core not parsed: %+v", r)
	}
	if r.PreviousResponseID != "resp_prev" || r.MaxOutputTokens != 2048 {
		t.Fatalf("request chaining/limits: %+v", r)
	}
	if r.Reasoning == nil || r.Reasoning.Effort != "medium" {
		t.Fatalf("reasoning config: %+v", r.Reasoning)
	}
	if r.Store == nil || !*r.Store {
		t.Fatalf("store flag: %+v", r.Store)
	}
	if len(r.Input) != 1 || r.Input[0].Content != "list files" {
		t.Fatalf("input item not parsed: %+v", r.Input)
	}
	// Flat function tool + built-in tool kept in Raw.
	if len(r.Tools) != 2 || r.Tools[0].Name != "bash" || r.Tools[0].Type != "function" {
		t.Fatalf("function tool not parsed flat: %+v", r.Tools)
	}
	if r.Tools[1].Type != "web_search" || r.Tools[1].Raw == nil {
		t.Fatalf("built-in tool should land in Raw: %+v", r.Tools[1])
	}
	if _, ok := r.UnknownFields["future_field"]; !ok {
		t.Fatalf("unknown field not captured: %+v", r.UnknownFields)
	}

	rsp := a.Responses.Response
	if rsp == nil || rsp.Status != "completed" || len(rsp.Output) != 3 {
		t.Fatalf("response output not parsed: %+v", rsp)
	}
	if rsp.Output[0].Type != "reasoning" || rsp.Output[0].Summary != "thinking" {
		t.Fatalf("reasoning output: %+v", rsp.Output[0])
	}
	if rsp.Output[1].Content != "here you go" {
		t.Fatalf("message output: %+v", rsp.Output[1])
	}
	if rsp.Output[2].Type != "function_call" || rsp.Output[2].Name != "bash" || rsp.Output[2].CallID != "call_1" {
		t.Fatalf("function_call output: %+v", rsp.Output[2])
	}

	// Normalized usage: cached -> cache read, input remainder uncached.
	if a.Usage == nil || a.Usage.InputTokens != 60 || a.Usage.OutputTokens != 20 || a.Usage.CacheReadTokens != 40 {
		t.Fatalf("normalized usage: %+v", a.Usage)
	}
	if a.Usage.CacheWriteTokens != 0 {
		t.Fatalf("Responses has no cache-write; want 0, got %d", a.Usage.CacheWriteTokens)
	}
	// A function_call output present -> tool_use stop.
	if a.StopReason != StopToolUse {
		t.Fatalf("stop reason: %q", a.StopReason)
	}
	// Normalized request: instructions -> system.
	if a.Normalized == nil || a.Normalized.System != "You are a coding agent." || a.Normalized.Model != "gpt-5-codex" {
		t.Fatalf("normalized request: %+v", a.Normalized)
	}
}

func TestResponses_inputStringForm(t *testing.T) {
	a := Analyze(Input{
		Method: "POST", URL: "/v1/responses",
		RequestBody: `{"model":"gpt-5","input":"just a string"}`,
	})
	r := a.Responses.Request
	if len(r.Input) != 1 || r.Input[0].Role != "user" || r.Input[0].Content != "just a string" {
		t.Fatalf("string input not normalized to a user message: %+v", r.Input)
	}
	if len(a.Normalized.Messages) != 1 || a.Normalized.Messages[0].Role != "user" {
		t.Fatalf("normalized messages: %+v", a.Normalized.Messages)
	}
}

func TestResponses_completedStopAndStatus(t *testing.T) {
	resp := `{"id":"r","model":"m","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":3,"output_tokens":1}}`
	a := Analyze(Input{Method: "POST", URL: "/v1/responses", ResponseBody: resp})
	if a.StopReason != StopEndTurn {
		t.Fatalf("completed should map to end_turn, got %q", a.StopReason)
	}
}

func TestResponses_incompleteMaxTokens(t *testing.T) {
	resp := `{"id":"r","model":"m","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`
	a := Analyze(Input{Method: "POST", URL: "/v1/responses", ResponseBody: resp})
	if a.Responses.Response.Incomplete == nil || a.Responses.Response.Incomplete.Reason != "max_output_tokens" {
		t.Fatalf("incomplete details not parsed: %+v", a.Responses.Response.Incomplete)
	}
	if a.StopReason != StopMaxTokens {
		t.Fatalf("incomplete(max_output_tokens) should map to max_tokens, got %q", a.StopReason)
	}
}

func TestResponses_errorBody(t *testing.T) {
	resp := `{"error":{"type":"invalid_request_error","code":"bad","message":"nope"}}`
	a := Analyze(Input{Method: "POST", URL: "/v1/responses", ResponseBody: resp})
	if a.Error == nil || a.Error.Type != "invalid_request_error" || a.Error.Code != "bad" {
		t.Fatalf("error not normalized: %+v", a.Error)
	}
}

func TestResponses_streamingTextAndUsage(t *testing.T) {
	stream := strings.Join([]string{
		`event: response.created`,
		`data: {"response":{"id":"resp_1","model":"gpt-5-codex","status":"in_progress"}}`,
		"",
		`event: response.output_item.added`,
		`data: {"output_index":0,"item":{"type":"message","role":"assistant"}}`,
		"",
		`event: response.output_text.delta`,
		`data: {"output_index":0,"delta":"Hel"}`,
		"",
		`event: response.output_text.delta`,
		`data: {"output_index":0,"delta":"lo"}`,
		"",
		`event: response.output_text.done`,
		`data: {"output_index":0,"text":"Hello"}`,
		"",
		`event: response.completed`,
		`data: {"response":{"id":"resp_1","model":"gpt-5-codex","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}],"usage":{"input_tokens":5,"output_tokens":2}}}`,
		"",
	}, "\n")
	a := Analyze(Input{
		Method: "POST", URL: "/v1/responses",
		ResponseBody:    stream,
		ResponseHeaders: map[string]string{"content-type": "text/event-stream"},
		Streaming:       true,
	})
	rsp := a.Responses.Response
	if rsp == nil || !rsp.Streamed {
		t.Fatalf("should be a streamed response: %+v", rsp)
	}
	if rsp.Status != "completed" {
		t.Fatalf("final status from completed event: %q", rsp.Status)
	}
	if len(rsp.Output) != 1 || rsp.Output[0].Content != "Hello" {
		t.Fatalf("final output from completed event: %+v", rsp.Output)
	}
	// Usage comes from the terminal event.
	if a.Usage == nil || a.Usage.InputTokens != 5 || a.Usage.OutputTokens != 2 {
		t.Fatalf("usage from completed event: %+v", a.Usage)
	}
	if a.StopReason != StopEndTurn {
		t.Fatalf("stop reason: %q", a.StopReason)
	}
}

func TestResponses_streamingFunctionCallFragments(t *testing.T) {
	stream := strings.Join([]string{
		`event: response.output_item.added`,
		`data: {"output_index":0,"item":{"type":"function_call","call_id":"call_1","name":"bash"}}`,
		"",
		`event: response.function_call_arguments.delta`,
		`data: {"output_index":0,"delta":"{\"cmd\":"}`,
		"",
		`event: response.function_call_arguments.delta`,
		`data: {"output_index":0,"delta":"\"ls\"}"}`,
		"",
		`event: response.function_call_arguments.done`,
		`data: {"output_index":0,"arguments":"{\"cmd\":\"ls\"}"}`,
		"",
		`event: response.completed`,
		`data: {"response":{"id":"r","status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"bash","arguments":"{\"cmd\":\"ls\"}"}]}}`,
		"",
	}, "\n")
	a := Analyze(Input{Method: "POST", URL: "/v1/responses", ResponseBody: stream, Streaming: true})
	out := a.Responses.Response.Output
	if len(out) != 1 || out[0].Type != "function_call" || out[0].Arguments != `{"cmd":"ls"}` {
		t.Fatalf("function_call not reconstructed: %+v", out)
	}
	if a.StopReason != StopToolUse {
		t.Fatalf("stop reason: %q", a.StopReason)
	}
}

// TestResponses_streamingTruncatedInStream verifies the in-stream aggregation
// path: with NO terminal event, we still surface the partial text from deltas.
func TestResponses_streamingTruncatedInStream(t *testing.T) {
	stream := strings.Join([]string{
		`event: response.created`,
		`data: {"response":{"id":"resp_1","model":"gpt-5-codex","status":"in_progress"}}`,
		"",
		`event: response.output_item.added`,
		`data: {"output_index":0,"item":{"type":"message","role":"assistant"}}`,
		"",
		`event: response.output_text.delta`,
		`data: {"output_index":0,"delta":"partial"}`,
		"",
	}, "\n")
	a := Analyze(Input{Method: "POST", URL: "/v1/responses", ResponseBody: stream, Streaming: true})
	rsp := a.Responses.Response
	if rsp == nil || !rsp.Streamed {
		t.Fatalf("expected a partial streamed response")
	}
	if rsp.ID != "resp_1" || rsp.Status != "in_progress" {
		t.Fatalf("snapshot fields lost: %+v", rsp)
	}
	if len(rsp.Output) != 1 || rsp.Output[0].Content != "partial" {
		t.Fatalf("in-stream partial text not aggregated: %+v", rsp.Output)
	}
}

// TestResponses_streamingUnknownEventTolerated checks that unfamiliar event
// types don't flood warnings (at most one summary) and don't break parsing.
func TestResponses_streamingUnknownEventTolerated(t *testing.T) {
	stream := strings.Join([]string{
		`event: response.some_future_event`,
		`data: {"foo":1}`,
		"",
		`event: response.another_future_event`,
		`data: {"bar":2}`,
		"",
		`event: response.completed`,
		`data: {"response":{"id":"r","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1}}}`,
		"",
	}, "\n")
	a := Analyze(Input{Method: "POST", URL: "/v1/responses", ResponseBody: stream, Streaming: true})
	if a.Responses.Response.Output[0].Content != "ok" {
		t.Fatalf("completed event should still parse despite unknown events")
	}
	unknownWarnings := 0
	for _, w := range a.Warnings {
		if strings.Contains(w, "unrecognized Responses event") {
			unknownWarnings++
		}
	}
	if unknownWarnings != 1 {
		t.Fatalf("expected exactly one summary warning for unknown events, got %d (%v)", unknownWarnings, a.Warnings)
	}
}
