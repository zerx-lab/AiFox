package llmparse

import (
	"encoding/json"
	"sort"
	"strings"
)

// parseOpenAISSE reconstructs a chat-completions response from a streamed SSE
// body. Each `data:` line is a JSON chunk carrying choices[].delta; we merge
// content (concatenate) and tool_calls (aggregate by index: id/name once,
// arguments fragments appended). `data: [DONE]` terminates the stream; a final
// chunk with `usage` (from stream_options.include_usage) carries token counts.
//
// We're forgiving: a half-written chunk (capture truncated mid-stream) lands in
// warnings and the partial result is still returned. We reuse splitSSE, which
// already normalizes CRLF.
func parseOpenAISSE(body string) (*OpenAIResponse, []string) {
	resp := &OpenAIResponse{Streamed: true}
	var warnings []string
	// Choice accumulators keyed by choice index.
	choices := map[int]*streamChoice{}

	for _, ev := range splitSSE(body) {
		data := strings.TrimSpace(ev.Data)
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Choices []struct {
				Index        int             `json:"index"`
				Delta        json.RawMessage `json:"delta"`
				FinishReason *string         `json:"finish_reason"`
			} `json:"choices"`
			Usage map[string]any `json:"usage"`
			Error map[string]any `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			warnings = append(warnings, "could not parse SSE chunk: "+err.Error())
			continue
		}
		if chunk.ID != "" {
			resp.ID = chunk.ID
		}
		if chunk.Model != "" {
			resp.Model = chunk.Model
		}
		if chunk.Error != nil {
			resp.Error = parseOpenAIError(chunk.Error)
		}
		if len(chunk.Usage) > 0 {
			resp.Usage = parseOpenAIUsage(chunk.Usage)
		}
		for _, c := range chunk.Choices {
			sc := choices[c.Index]
			if sc == nil {
				sc = &streamChoice{index: c.Index, toolCalls: map[int]*streamToolCall{}}
				choices[c.Index] = sc
			}
			if c.FinishReason != nil && *c.FinishReason != "" {
				sc.finishReason = *c.FinishReason
			}
			if len(c.Delta) > 0 {
				applyOpenAIDelta(sc, c.Delta, &warnings)
			}
		}
	}

	indices := make([]int, 0, len(choices))
	for idx := range choices {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	for _, idx := range indices {
		resp.Choices = append(resp.Choices, choices[idx].finalize())
	}
	return resp, warnings
}

// streamChoice accumulates one choice's delta state across chunks.
type streamChoice struct {
	index        int
	role         string
	content      strings.Builder
	finishReason string
	toolCalls    map[int]*streamToolCall
	// fnCall accumulates a legacy single function_call delta.
	fnName string
	fnArgs strings.Builder
	hasFn  bool
}

// streamToolCall accumulates one tool_calls[index] entry across chunks.
type streamToolCall struct {
	id   string
	name string
	typ  string
	args strings.Builder
}

func applyOpenAIDelta(sc *streamChoice, delta json.RawMessage, warnings *[]string) {
	var d struct {
		Role      string  `json:"role"`
		Content   *string `json:"content"`
		ToolCalls []struct {
			Index    int    `json:"index"`
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
		FunctionCall *struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function_call"`
	}
	if err := json.Unmarshal(delta, &d); err != nil {
		*warnings = append(*warnings, "could not parse choice delta: "+err.Error())
		return
	}
	if d.Role != "" {
		sc.role = d.Role
	}
	if d.Content != nil {
		sc.content.WriteString(*d.Content)
	}
	for _, tc := range d.ToolCalls {
		acc := sc.toolCalls[tc.Index]
		if acc == nil {
			acc = &streamToolCall{}
			sc.toolCalls[tc.Index] = acc
		}
		if tc.ID != "" {
			acc.id = tc.ID
		}
		if tc.Type != "" {
			acc.typ = tc.Type
		}
		if tc.Function.Name != "" {
			acc.name = tc.Function.Name
		}
		if tc.Function.Arguments != "" {
			acc.args.WriteString(tc.Function.Arguments)
		}
	}
	if d.FunctionCall != nil {
		sc.hasFn = true
		if d.FunctionCall.Name != "" {
			sc.fnName = d.FunctionCall.Name
		}
		sc.fnArgs.WriteString(d.FunctionCall.Arguments)
	}
}

func (sc *streamChoice) finalize() OpenAIChoice {
	msg := &OpenAIMessage{Role: sc.role, Content: sc.content.String()}
	if sc.role == "" {
		msg.Role = "assistant"
	}
	// Tool calls in index order.
	idxs := make([]int, 0, len(sc.toolCalls))
	for i := range sc.toolCalls {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	for _, i := range idxs {
		acc := sc.toolCalls[i]
		msg.ToolCalls = append(msg.ToolCalls, OpenAIToolCall{
			ID:        acc.id,
			Type:      acc.typ,
			Name:      acc.name,
			Arguments: acc.args.String(),
		})
	}
	if sc.hasFn {
		msg.FunctionCall = &OpenAIFunctionCall{Name: sc.fnName, Arguments: sc.fnArgs.String()}
	}
	return OpenAIChoice{Index: sc.index, Message: msg, FinishReason: sc.finishReason}
}
