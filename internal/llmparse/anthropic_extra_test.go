package llmparse

import "testing"

func TestAnthropic_thinkingAndBetasNoWarning(t *testing.T) {
	body := `{
		"model":"claude-opus-4-8",
		"messages":[{"role":"user","content":"hi"}],
		"thinking":{"type":"enabled","budget_tokens":2048},
		"betas":["foo-2025-01-01"],
		"service_tier":"standard"
	}`
	a := Analyze(Input{Method: "POST", URL: "/v1/messages", RequestBody: body})
	if a == nil || a.Anthropic == nil || a.Anthropic.Request == nil {
		t.Fatalf("no request parsed")
	}
	req := a.Anthropic.Request
	if req.Thinking == nil || req.Thinking.Type != "enabled" || req.Thinking.BudgetTokens != 2048 {
		t.Fatalf("thinking not structured: %+v", req.Thinking)
	}
	if len(req.Betas) != 1 || req.Betas[0] != "foo-2025-01-01" {
		t.Fatalf("betas: %+v", req.Betas)
	}
	if req.ServiceTier != "standard" {
		t.Fatalf("service_tier: %q", req.ServiceTier)
	}
	if len(req.UnknownFields) != 0 {
		t.Fatalf("thinking/betas/service_tier should not be unknown: %+v", req.UnknownFields)
	}
	for _, w := range a.Warnings {
		t.Fatalf("unexpected warning: %q", w)
	}
}

func TestAnthropic_imageBlockStructuredNoBase64(t *testing.T) {
	// "AAAA" base64 decodes to 3 bytes; the payload must not be stored.
	body := `{"model":"x","messages":[{"role":"user","content":[
		{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}
	]}]}`
	a := Analyze(Input{Method: "POST", URL: "/v1/messages", RequestBody: body})
	blk := a.Anthropic.Request.Messages[0].Content[0]
	if blk.Image == nil {
		t.Fatalf("image not structured: %+v", blk)
	}
	if blk.Image.MediaType != "image/png" || blk.Image.SourceType != "base64" {
		t.Fatalf("image source fields: %+v", blk.Image)
	}
	if blk.Image.Bytes != 3 {
		t.Fatalf("expected 3 decoded bytes, got %d", blk.Image.Bytes)
	}
}

func TestAnthropic_imageURLSource(t *testing.T) {
	body := `{"model":"x","messages":[{"role":"user","content":[
		{"type":"image","source":{"type":"url","url":"https://e/x.png"}}
	]}]}`
	a := Analyze(Input{Method: "POST", URL: "/v1/messages", RequestBody: body})
	img := a.Anthropic.Request.Messages[0].Content[0].Image
	if img == nil || img.URL != "https://e/x.png" || img.SourceType != "url" {
		t.Fatalf("url image not structured: %+v", img)
	}
}

func TestAnthropic_cacheControlStructured(t *testing.T) {
	body := `{"model":"x","system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":"hi"}]}`
	a := Analyze(Input{Method: "POST", URL: "/v1/messages", RequestBody: body})
	sys := a.Anthropic.Request.System[0]
	if sys.Cache == nil || sys.Cache.Type != "ephemeral" || sys.Cache.TTL != "1h" {
		t.Fatalf("cache_control not structured: %+v", sys.Cache)
	}
}

func TestAnthropic_countTokens(t *testing.T) {
	req := `{"model":"claude-opus-4-8","messages":[{"role":"user","content":"hi"}]}`
	resp := `{"input_tokens":42}`
	a := Analyze(Input{
		Method:          "POST",
		URL:             "/v1/messages/count_tokens",
		RequestBody:     req,
		ResponseBody:    resp,
		ResponseHeaders: map[string]string{"content-type": "application/json"},
	})
	if a == nil || a.Kind != KindAnthropicCountTokens {
		t.Fatalf("expected anthropic.count_tokens, got %+v", a)
	}
	if a.Usage == nil || a.Usage.InputTokens != 42 {
		t.Fatalf("input_tokens not parsed: %+v", a.Usage)
	}
	if !IsUtilityRequest(a) {
		t.Fatalf("count_tokens should classify as utility")
	}
}

func TestAnthropic_normalizedUsageFromResponse(t *testing.T) {
	resp := `{"id":"m","model":"claude-sonnet-4-6","stop_reason":"max_tokens",
		"content":[{"type":"text","text":"x"}],
		"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":3,"cache_creation_input_tokens":2}}`
	a := Analyze(Input{
		Method:          "POST",
		URL:             "/v1/messages",
		ResponseBody:    resp,
		ResponseHeaders: map[string]string{"content-type": "application/json"},
	})
	if a.Usage == nil {
		t.Fatalf("no normalized usage")
	}
	if a.Usage.InputTokens != 10 || a.Usage.OutputTokens != 5 ||
		a.Usage.CacheReadTokens != 3 || a.Usage.CacheWriteTokens != 2 {
		t.Fatalf("normalized usage wrong: %+v", a.Usage)
	}
	if a.StopReason != StopMaxTokens {
		t.Fatalf("stop reason: %q", a.StopReason)
	}
}

func TestCountTokens_notMatchedByMessages(t *testing.T) {
	// Ensure /v1/messages does not capture the count_tokens path.
	a := Analyze(Input{Method: "POST", URL: "/v1/messages", RequestBody: `{"model":"x","messages":[{"role":"user","content":"hi"}]}`})
	if a.Kind != KindAnthropicMessages {
		t.Fatalf("messages misrouted: %q", a.Kind)
	}
}
