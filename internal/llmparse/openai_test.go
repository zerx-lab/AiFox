package llmparse

import (
	"strings"
	"testing"
)

func TestOpenAI_nonStreamingResponse(t *testing.T) {
	req := `{
		"model":"gpt-4o",
		"messages":[
			{"role":"system","content":"be terse"},
			{"role":"user","content":"hi"}
		],
		"temperature":0.5,
		"max_tokens":256,
		"future_field":1
	}`
	resp := `{
		"id":"chatcmpl-1","model":"gpt-4o-2024-08-06",
		"choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13,"prompt_tokens_details":{"cached_tokens":4}}
	}`
	a := Analyze(Input{
		Method:          "POST",
		URL:             "/v1/chat/completions",
		RequestBody:     req,
		ResponseBody:    resp,
		ResponseHeaders: map[string]string{"content-type": "application/json"},
	})
	if a == nil || a.Kind != KindOpenAIChat || a.OpenAI == nil {
		t.Fatalf("expected openai.chat analysis, got %+v", a)
	}
	r := a.OpenAI.Request
	if r == nil || r.Model != "gpt-4o" || r.MaxTokens != 256 {
		t.Fatalf("request not parsed: %+v", r)
	}
	if r.Temperature == nil || *r.Temperature != 0.5 {
		t.Fatalf("temperature: %+v", r.Temperature)
	}
	if _, ok := r.UnknownFields["future_field"]; !ok {
		t.Fatalf("unknown field not captured: %+v", r.UnknownFields)
	}
	rsp := a.OpenAI.Response
	if rsp == nil || rsp.Model != "gpt-4o-2024-08-06" {
		t.Fatalf("response not parsed: %+v", rsp)
	}
	if len(rsp.Choices) != 1 || rsp.Choices[0].Message.Content != "hello" {
		t.Fatalf("choice not parsed: %+v", rsp.Choices)
	}
	if rsp.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason: %q", rsp.Choices[0].FinishReason)
	}
	// Normalized usage: prompt_tokens minus cached = input remainder.
	if a.Usage == nil || a.Usage.InputTokens != 6 || a.Usage.OutputTokens != 3 || a.Usage.CacheReadTokens != 4 {
		t.Fatalf("normalized usage: %+v", a.Usage)
	}
	if a.StopReason != StopEndTurn {
		t.Fatalf("stop reason normalize: %q", a.StopReason)
	}
}

func TestOpenAI_toolCallsNonStreaming(t *testing.T) {
	resp := `{
		"id":"c1","model":"gpt-4o",
		"choices":[{"index":0,"finish_reason":"tool_calls","message":{
			"role":"assistant","content":null,
			"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]
		}}]
	}`
	a := Analyze(Input{Method: "POST", URL: "/v1/chat/completions", ResponseBody: resp})
	if a == nil || a.OpenAI == nil || a.OpenAI.Response == nil {
		t.Fatalf("no response")
	}
	tc := a.OpenAI.Response.Choices[0].Message.ToolCalls
	if len(tc) != 1 || tc[0].Name != "get_weather" || tc[0].ID != "call_1" {
		t.Fatalf("tool call not parsed: %+v", tc)
	}
	if a.StopReason != StopToolUse {
		t.Fatalf("stop reason: %q", a.StopReason)
	}
}

func TestOpenAI_legacyFunctionCall(t *testing.T) {
	resp := `{
		"id":"c1","model":"gpt-4o",
		"choices":[{"index":0,"finish_reason":"function_call","message":{
			"role":"assistant","content":null,
			"function_call":{"name":"lookup","arguments":"{\"q\":1}"}
		}}]
	}`
	a := Analyze(Input{Method: "POST", URL: "/v1/chat/completions", ResponseBody: resp})
	fc := a.OpenAI.Response.Choices[0].Message.FunctionCall
	if fc == nil || fc.Name != "lookup" || fc.Arguments != `{"q":1}` {
		t.Fatalf("legacy function_call not parsed: %+v", fc)
	}
	if a.StopReason != StopToolUse {
		t.Fatalf("stop reason: %q", a.StopReason)
	}
}

func TestOpenAI_streamingTextAndUsage(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		"",
		`data: {"choices":[{"index":0,"delta":{"content":"Hel"}}]}`,
		"",
		`data: {"choices":[{"index":0,"delta":{"content":"lo"}}]}`,
		"",
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		`data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	a := Analyze(Input{
		Method:          "POST",
		URL:             "/v1/chat/completions",
		ResponseBody:    stream,
		ResponseHeaders: map[string]string{"content-type": "text/event-stream"},
		Streaming:       true,
	})
	if a == nil || a.OpenAI == nil || a.OpenAI.Response == nil {
		t.Fatalf("no streamed response")
	}
	r := a.OpenAI.Response
	if !r.Streamed {
		t.Fatalf("should be marked streamed")
	}
	if len(r.Choices) != 1 || r.Choices[0].Message.Content != "Hello" {
		t.Fatalf("content not merged: %+v", r.Choices)
	}
	if r.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish reason: %q", r.Choices[0].FinishReason)
	}
	if a.Usage == nil || a.Usage.InputTokens != 5 || a.Usage.OutputTokens != 2 {
		t.Fatalf("usage from include_usage chunk: %+v", a.Usage)
	}
}

func TestOpenAI_streamingToolCallFragments(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"do","arguments":"{\"a\":"}}]}}]}`,
		"",
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}`,
		"",
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	a := Analyze(Input{Method: "POST", URL: "/v1/chat/completions", ResponseBody: stream, Streaming: true})
	tc := a.OpenAI.Response.Choices[0].Message.ToolCalls
	if len(tc) != 1 {
		t.Fatalf("expected one aggregated tool call, got %+v", tc)
	}
	if tc[0].ID != "call_1" || tc[0].Name != "do" || tc[0].Arguments != `{"a":1}` {
		t.Fatalf("tool call fragments not aggregated: %+v", tc[0])
	}
}

func TestOpenAI_streamingPartialEventWarns(t *testing.T) {
	// A truncated final chunk (no closing brace) should warn but not abort.
	stream := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\""
	a := Analyze(Input{Method: "POST", URL: "/v1/chat/completions", ResponseBody: stream, Streaming: true})
	if a == nil || a.OpenAI.Response == nil {
		t.Fatalf("partial stream should still produce a response")
	}
	if a.OpenAI.Response.Choices[0].Message.Content != "hi" {
		t.Fatalf("partial content lost: %+v", a.OpenAI.Response.Choices)
	}
	if len(a.Warnings) == 0 {
		t.Fatalf("expected a warning for the half event")
	}
}

func TestOpenAI_completionsLegacy(t *testing.T) {
	req := `{"model":"gpt-3.5-turbo-instruct","prompt":"say hi"}`
	resp := `{"id":"cmpl-1","model":"gpt-3.5-turbo-instruct","choices":[{"index":0,"text":" hi","finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`
	a := Analyze(Input{
		Method:          "POST",
		URL:             "/v1/completions",
		RequestBody:     req,
		ResponseBody:    resp,
		ResponseHeaders: map[string]string{"content-type": "application/json"},
	})
	if a == nil || a.Kind != KindOpenAICompletions {
		t.Fatalf("expected openai.completions, got %+v", a)
	}
	if a.OpenAI.Request == nil || a.OpenAI.Request.Prompt != "say hi" {
		t.Fatalf("prompt not parsed: %+v", a.OpenAI.Request)
	}
	if len(a.OpenAI.Response.Choices) != 1 || a.OpenAI.Response.Choices[0].Text != " hi" {
		t.Fatalf("text choice not parsed: %+v", a.OpenAI.Response.Choices)
	}
	if a.Usage == nil || a.Usage.OutputTokens != 1 {
		t.Fatalf("usage: %+v", a.Usage)
	}
}

func TestOpenAI_completionsNotMatchedByChat(t *testing.T) {
	// /chat/completions must route to the chat analyzer, not completions.
	a := Analyze(Input{Method: "POST", URL: "/v1/chat/completions", RequestBody: `{"model":"gpt-4o"}`})
	if a == nil || a.Kind != KindOpenAIChat {
		t.Fatalf("chat path misrouted: %+v", a)
	}
}

func TestOpenAI_errorBody(t *testing.T) {
	resp := `{"error":{"type":"invalid_request_error","code":"model_not_found","message":"nope"}}`
	a := Analyze(Input{Method: "POST", URL: "/v1/chat/completions", ResponseBody: resp})
	if a.Error == nil || a.Error.Type != "invalid_request_error" || a.Error.Code != "model_not_found" {
		t.Fatalf("error not normalized: %+v", a.Error)
	}
}
