package llmparse

import (
	"strings"
	"testing"
)

func TestAnalyze_nonMessagesReturnsNil(t *testing.T) {
	if a := Analyze(Input{Method: "GET", URL: "/v1/models"}); a != nil {
		t.Fatalf("non-messages endpoint should return nil, got %+v", a)
	}
}

func TestAnalyze_requestStructured(t *testing.T) {
	body := `{
		"model": "claude-sonnet-4-5",
		"max_tokens": 1024,
		"temperature": 0.2,
		"system": [{"type":"text","text":"you are helpful","cache_control":{"type":"ephemeral"}}],
		"tools": [{"name":"read_file","description":"read","input_schema":{"type":"object"}}],
		"messages": [
			{"role":"user","content":"hi"},
			{"role":"assistant","content":[
				{"type":"text","text":"hello"},
				{"type":"tool_use","id":"tu_1","name":"read_file","input":{"path":"a.go"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"tu_1","content":"contents","is_error":false}
			]}
		],
		"future_field": "ignore-me"
	}`
	a := Analyze(Input{
		Method:      "POST",
		URL:         "/v1/messages",
		RequestBody: body,
	})
	if a == nil || a.Kind != KindAnthropicMessages {
		t.Fatalf("expected anthropic.messages analysis, got %+v", a)
	}
	req := a.Anthropic.Request
	if req == nil {
		t.Fatalf("request should be parsed")
	}
	if req.Model != "claude-sonnet-4-5" {
		t.Fatalf("model: %q", req.Model)
	}
	if req.MaxTokens != 1024 {
		t.Fatalf("max tokens: %d", req.MaxTokens)
	}
	if req.Temperature == nil || *req.Temperature != 0.2 {
		t.Fatalf("temperature: %+v", req.Temperature)
	}
	if len(req.System) != 1 || req.System[0].Type != "text" || req.System[0].Text != "you are helpful" {
		t.Fatalf("system block not parsed: %+v", req.System)
	}
	if req.System[0].CacheControl == nil {
		t.Fatalf("cache_control should be preserved on system block")
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "read_file" {
		t.Fatalf("tool not parsed: %+v", req.Tools)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("messages count: %d", len(req.Messages))
	}
	if req.Messages[1].Content[1].Type != "tool_use" || req.Messages[1].Content[1].Name != "read_file" {
		t.Fatalf("tool_use not parsed: %+v", req.Messages[1].Content[1])
	}
	if req.Messages[2].Content[0].Type != "tool_result" || req.Messages[2].Content[0].ToolUseID != "tu_1" {
		t.Fatalf("tool_result not parsed: %+v", req.Messages[2].Content[0])
	}
	if _, ok := req.UnknownFields["future_field"]; !ok {
		t.Fatalf("unknown field not preserved: %+v", req.UnknownFields)
	}
	if len(a.Warnings) == 0 {
		t.Fatalf("expected a warning about the unknown field")
	}
}

func TestAnalyze_nonStreamingResponse(t *testing.T) {
	resp := `{
		"id":"msg_1","model":"claude-sonnet-4-5","role":"assistant","stop_reason":"end_turn",
		"content":[{"type":"text","text":"hi there"}],
		"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":4}
	}`
	a := Analyze(Input{
		Method:          "POST",
		URL:             "/v1/messages",
		ResponseBody:    resp,
		ResponseHeaders: map[string]string{"content-type": "application/json"},
	})
	if a == nil || a.Anthropic.Response == nil {
		t.Fatalf("response should be parsed")
	}
	r := a.Anthropic.Response
	if r.Streamed {
		t.Fatalf("non-stream response shouldn't be marked streamed")
	}
	if r.ID != "msg_1" || r.StopReason != "end_turn" {
		t.Fatalf("response fields: %+v", r)
	}
	if r.Usage == nil || r.Usage.OutputTokens != 5 || r.Usage.CacheReadInputTokens != 4 {
		t.Fatalf("usage: %+v", r.Usage)
	}
}

func TestAnalyze_streamingResponse(t *testing.T) {
	stream := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_2","model":"claude-sonnet-4-5","role":"assistant","usage":{"input_tokens":12}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_x","name":"do_thing","input":{}}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"1}"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":1}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":8}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	a := Analyze(Input{
		Method:          "POST",
		URL:             "/v1/messages",
		ResponseBody:    stream,
		ResponseHeaders: map[string]string{"content-type": "text/event-stream"},
		Streaming:       true,
	})
	if a == nil || a.Anthropic.Response == nil {
		t.Fatalf("stream should parse")
	}
	r := a.Anthropic.Response
	if !r.Streamed {
		t.Fatalf("should be marked streamed")
	}
	if r.ID != "msg_2" || r.Model != "claude-sonnet-4-5" {
		t.Fatalf("missing message_start fields: %+v", r)
	}
	if r.StopReason != "end_turn" {
		t.Fatalf("stop reason: %q", r.StopReason)
	}
	if len(r.Content) != 2 {
		t.Fatalf("expected two content blocks, got %d", len(r.Content))
	}
	if r.Content[0].Type != "text" || r.Content[0].Text != "Hello world" {
		t.Fatalf("text block not reconstructed: %+v", r.Content[0])
	}
	if r.Content[1].Type != "tool_use" || r.Content[1].Name != "do_thing" {
		t.Fatalf("tool_use block missing: %+v", r.Content[1])
	}
	input, ok := r.Content[1].Input.(map[string]any)
	if !ok || input["a"] != float64(1) {
		t.Fatalf("tool_use input not parsed: %+v", r.Content[1].Input)
	}
	if r.Usage == nil || r.Usage.InputTokens != 12 || r.Usage.OutputTokens != 8 {
		t.Fatalf("usage merged wrong: %+v", r.Usage)
	}
}

func TestAnalyze_streamingPartialBlockKept(t *testing.T) {
	// Capture cut off mid-stream: content_block_delta arrived without a
	// preceding content_block_start. We should still keep the delta text.
	stream := "event: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"orphan\"}}\n\n"
	a := Analyze(Input{
		Method:       "POST",
		URL:          "/v1/messages",
		ResponseBody: stream,
		Streaming:    true,
	})
	if a == nil || a.Anthropic.Response == nil {
		t.Fatalf("partial stream should still produce a response")
	}
	if len(a.Anthropic.Response.Content) != 1 || a.Anthropic.Response.Content[0].Text != "orphan" {
		t.Fatalf("orphan delta not kept: %+v", a.Anthropic.Response.Content)
	}
}

func TestAnalyze_unknownBlockKeepsRaw(t *testing.T) {
	body := `{"model":"x","messages":[{"role":"assistant","content":[{"type":"future_block","foo":"bar"}]}]}`
	a := Analyze(Input{Method: "POST", URL: "/v1/messages", RequestBody: body})
	if a == nil {
		t.Fatalf("should analyze")
	}
	blk := a.Anthropic.Request.Messages[0].Content[0]
	if blk.Type != "future_block" || blk.Raw == nil {
		t.Fatalf("unknown block should preserve Raw: %+v", blk)
	}
}
