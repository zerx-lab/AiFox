package llmparse

import (
	"encoding/json"
	"fmt"
	"strings"
)

// parseAnthropicSSE reconstructs the final response shape from an SSE stream.
// The wire format is documented at:
//
//	https://docs.anthropic.com/en/api/messages-streaming
//
// We're forgiving: unknown event types are noted in warnings but don't abort,
// and partial streams (the user disconnected, or capture truncated) still
// produce a useful result with whatever has been emitted so far.
func parseAnthropicSSE(body string) (*AnthropicResponse, []string) {
	resp := &AnthropicResponse{Streamed: true}
	var warnings []string
	// Block accumulator: keyed by content_block index. We keep both the
	// final block shape and any in-progress JSON for tool_use inputs.
	blocks := map[int]*streamBlock{}

	for _, ev := range splitSSE(body) {
		switch ev.Event {
		case "message_start":
			applyMessageStart(resp, ev.Data, &warnings)
		case "content_block_start":
			applyContentBlockStart(blocks, ev.Data, &warnings)
		case "content_block_delta":
			applyContentBlockDelta(blocks, ev.Data, &warnings)
		case "content_block_stop":
			applyContentBlockStop(blocks, ev.Data, &warnings)
		case "message_delta":
			applyMessageDelta(resp, ev.Data, &warnings)
		case "message_stop":
			// Sentinel — nothing to apply.
		case "ping":
			// Keepalive — ignore.
		case "error":
			applyErrorEvent(resp, ev.Data, &warnings)
		case "":
			// SSE comment / blank line — ignore.
		default:
			warnings = append(warnings, fmt.Sprintf("unrecognized SSE event %q (rendered as raw JSON)", ev.Event))
		}
	}

	// Collect blocks in index order so the final list mirrors the stream.
	indices := make([]int, 0, len(blocks))
	for idx := range blocks {
		indices = append(indices, idx)
	}
	sortInts(indices)
	for _, idx := range indices {
		resp.Content = append(resp.Content, blocks[idx].finalize())
	}

	return resp, warnings
}

type streamBlock struct {
	final        AnthropicBlock
	partialJSON  strings.Builder
	hadJSONDelta bool
}

func (b *streamBlock) finalize() AnthropicBlock {
	out := b.final
	if b.hadJSONDelta {
		// Tool-use inputs arrive as `input_json_delta` chunks that concatenate
		// into a single JSON document. Try to parse it; on failure keep the
		// raw string so the user can still see what was emitted.
		s := b.partialJSON.String()
		if s == "" {
			out.Input = map[string]any{}
		} else {
			var parsed any
			if err := json.Unmarshal([]byte(s), &parsed); err == nil {
				out.Input = parsed
			} else {
				out.Input = s
			}
		}
	}
	return out
}

func applyMessageStart(resp *AnthropicResponse, data string, warnings *[]string) {
	var ev struct {
		Message struct {
			ID    string         `json:"id"`
			Model string         `json:"model"`
			Role  string         `json:"role"`
			Usage map[string]any `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		*warnings = append(*warnings, "message_start: "+err.Error())
		return
	}
	resp.ID = ev.Message.ID
	resp.Model = ev.Message.Model
	resp.Role = ev.Message.Role
	if len(ev.Message.Usage) > 0 {
		resp.Usage = parseUsage(ev.Message.Usage)
	}
}

func applyContentBlockStart(blocks map[int]*streamBlock, data string, warnings *[]string) {
	var ev struct {
		Index        int            `json:"index"`
		ContentBlock map[string]any `json:"content_block"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		*warnings = append(*warnings, "content_block_start: "+err.Error())
		return
	}
	blk := parseBlock(ev.ContentBlock)
	blocks[ev.Index] = &streamBlock{final: blk}
}

func applyContentBlockDelta(blocks map[int]*streamBlock, data string, warnings *[]string) {
	var ev struct {
		Index int            `json:"index"`
		Delta map[string]any `json:"delta"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		*warnings = append(*warnings, "content_block_delta: "+err.Error())
		return
	}
	b, ok := blocks[ev.Index]
	if !ok {
		// Stream events arrived without a preceding content_block_start.
		// Synthesize an empty block so we at least keep the deltas.
		b = &streamBlock{}
		blocks[ev.Index] = b
	}
	deltaType, _ := ev.Delta["type"].(string)
	switch deltaType {
	case "text_delta":
		if s, ok := ev.Delta["text"].(string); ok {
			b.final.Text += s
			if b.final.Type == "" {
				b.final.Type = "text"
			}
		}
	case "input_json_delta":
		if s, ok := ev.Delta["partial_json"].(string); ok {
			b.partialJSON.WriteString(s)
			b.hadJSONDelta = true
			if b.final.Type == "" {
				b.final.Type = "tool_use"
			}
		}
	case "thinking_delta":
		if s, ok := ev.Delta["thinking"].(string); ok {
			b.final.Text += s
			if b.final.Type == "" {
				b.final.Type = "thinking"
			}
		}
	case "signature_delta":
		// Ignored — signature is an opaque attestation that isn't
		// directly user-facing.
	default:
		*warnings = append(*warnings, fmt.Sprintf("content_block_delta has unknown delta type %q", deltaType))
	}
}

func applyContentBlockStop(blocks map[int]*streamBlock, data string, warnings *[]string) {
	// No-op semantically; we finalize blocks at the end.
	// Validate that the index actually exists to surface stream corruption.
	var ev struct {
		Index int `json:"index"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		*warnings = append(*warnings, "content_block_stop: "+err.Error())
		return
	}
	if _, ok := blocks[ev.Index]; !ok {
		*warnings = append(*warnings, fmt.Sprintf("content_block_stop refers to unknown index %d", ev.Index))
	}
}

func applyMessageDelta(resp *AnthropicResponse, data string, warnings *[]string) {
	var ev struct {
		Delta struct {
			StopReason   string `json:"stop_reason"`
			StopSequence string `json:"stop_sequence"`
		} `json:"delta"`
		Usage map[string]any `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		*warnings = append(*warnings, "message_delta: "+err.Error())
		return
	}
	if ev.Delta.StopReason != "" {
		resp.StopReason = ev.Delta.StopReason
	}
	if len(ev.Usage) > 0 {
		// message_delta usage carries the final output_tokens; merge with
		// what message_start reported.
		incoming := parseUsage(ev.Usage)
		if resp.Usage == nil {
			resp.Usage = incoming
		} else {
			if incoming.OutputTokens > 0 {
				resp.Usage.OutputTokens = incoming.OutputTokens
			}
			if incoming.InputTokens > 0 {
				resp.Usage.InputTokens = incoming.InputTokens
			}
			if incoming.CacheReadInputTokens > 0 {
				resp.Usage.CacheReadInputTokens = incoming.CacheReadInputTokens
			}
			if incoming.CacheCreationInputTokens > 0 {
				resp.Usage.CacheCreationInputTokens = incoming.CacheCreationInputTokens
			}
		}
	}
}

func applyErrorEvent(resp *AnthropicResponse, data string, warnings *[]string) {
	var ev struct {
		Error AnthropicError `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		*warnings = append(*warnings, "error event: "+err.Error())
		return
	}
	resp.Error = &ev.Error
}

// sseEvent is one parsed event-stream message.
type sseEvent struct {
	Event string
	Data  string
}

// splitSSE parses a captured SSE stream into discrete events. Events are
// separated by blank lines; within an event each `data:` line is concatenated
// (with `\n` joining) per the spec.
func splitSSE(body string) []sseEvent {
	chunks := strings.Split(body, "\n\n")
	out := make([]sseEvent, 0, len(chunks))
	for _, chunk := range chunks {
		chunk = strings.TrimRight(chunk, "\n")
		if chunk == "" {
			continue
		}
		var ev sseEvent
		var data strings.Builder
		for _, line := range strings.Split(chunk, "\n") {
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			field, value := splitFieldValue(line)
			switch field {
			case "event":
				ev.Event = value
			case "data":
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(value)
			default:
				// id / retry / unknown — ignore for now.
			}
		}
		ev.Data = data.String()
		out = append(out, ev)
	}
	return out
}

func splitFieldValue(line string) (string, string) {
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return line, ""
	}
	field := line[:idx]
	value := line[idx+1:]
	value = strings.TrimPrefix(value, " ")
	return field, value
}

func sortInts(a []int) {
	// Insertion sort; the input is tiny (a handful of content blocks per
	// message).
	for i := 1; i < len(a); i++ {
		j := i
		for j > 0 && a[j-1] > a[j] {
			a[j-1], a[j] = a[j], a[j-1]
			j--
		}
	}
}
