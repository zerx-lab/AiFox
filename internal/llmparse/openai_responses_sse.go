package llmparse

import (
	"encoding/json"
	"sort"
	"strings"
)

// parseResponsesSSE reconstructs a Responses-API response from a streamed SSE
// body. The Responses event family differs entirely from Chat Completions: each
// event has a named `event:` plus a `data:` JSON payload.
//
// Strategy:
//   - The terminal events (response.completed / .failed / .incomplete) carry the
//     FULL final response object, including usage. That is the source of truth:
//     when present we build the result straight from it.
//   - Until then we accumulate an in-stream view from the incremental events
//     (output_item.added + the per-item text/argument/summary deltas) so a
//     capture truncated before completion still yields a useful partial result.
//   - Unknown event types are tolerated silently (the event family is extended
//     often); we record at most ONE summary warning rather than flooding.
//
// We reuse splitSSE, which already normalizes CRLF.
func parseResponsesSSE(body string) (*ResponsesResponse, []string) {
	var warnings []string
	acc := &responsesAcc{items: map[int]*responsesStreamItem{}}
	var final *ResponsesResponse
	unknownSeen := false

	for _, ev := range splitSSE(body) {
		data := strings.TrimSpace(ev.Data)
		if data == "" && ev.Event == "" {
			continue
		}
		switch ev.Event {
		case "response.created", "response.in_progress":
			acc.applySnapshot(data)
		case "response.output_item.added":
			acc.applyItemAdded(data)
		case "response.output_item.done":
			acc.applyItemDone(data)
		case "response.content_part.added", "response.content_part.done":
			// Part boundaries; text arrives via the *_text.delta events below.
		case "response.output_text.delta":
			acc.applyTextDelta(data)
		case "response.output_text.done":
			// Final text is already accumulated from the deltas.
		case "response.function_call_arguments.delta":
			acc.applyArgsDelta(data)
		case "response.function_call_arguments.done":
			acc.applyArgsDone(data)
		case "response.reasoning_summary_text.delta":
			acc.applyReasoningDelta(data)
		case "response.reasoning_summary_text.done":
			// Final summary is already accumulated from the deltas.
		case "response.completed", "response.failed", "response.incomplete":
			if r := extractResponse(data); r != nil {
				final = r
			}
		case "error":
			acc.applyError(data)
		default:
			// Tolerate unknown / future event types; one summary warning only.
			if !unknownSeen && ev.Event != "" {
				unknownSeen = true
				warnings = append(warnings, "stream contained unrecognized Responses event type(s) (skipped)")
			}
		}
	}

	if final != nil {
		final.Streamed = true
		return final, warnings
	}
	// No terminal event (truncated capture): return the in-stream view.
	return acc.finalize(), warnings
}

// extractResponse pulls the `response` object out of a terminal event payload
// and parses it via the shared responseFromMap.
func extractResponse(data string) *ResponsesResponse {
	var env struct {
		Response map[string]any `json:"response"`
	}
	if err := json.Unmarshal([]byte(data), &env); err != nil || env.Response == nil {
		return nil
	}
	return responseFromMap(env.Response)
}

// responsesAcc accumulates the in-stream view from incremental events, keyed by
// output index so concurrent items (text + a tool call) don't clobber each
// other.
type responsesAcc struct {
	id     string
	model  string
	status string
	items  map[int]*responsesStreamItem
	err    *OpenAIError
}

// responsesStreamItem accumulates one output[] item across its delta events.
type responsesStreamItem struct {
	typ     string
	role    string
	itemID  string
	callID  string
	name    string
	text    strings.Builder
	args    strings.Builder
	summary strings.Builder
}

func (a *responsesAcc) applySnapshot(data string) {
	r := extractResponse(data)
	if r == nil {
		return
	}
	if r.ID != "" {
		a.id = r.ID
	}
	if r.Model != "" {
		a.model = r.Model
	}
	if r.Status != "" {
		a.status = r.Status
	}
}

func (a *responsesAcc) item(index int) *responsesStreamItem {
	it := a.items[index]
	if it == nil {
		it = &responsesStreamItem{}
		a.items[index] = it
	}
	return it
}

func (a *responsesAcc) applyItemAdded(data string) {
	var ev struct {
		OutputIndex int            `json:"output_index"`
		Item        map[string]any `json:"item"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil || ev.Item == nil {
		return
	}
	it := a.item(ev.OutputIndex)
	it.typ, _ = ev.Item["type"].(string)
	it.role, _ = ev.Item["role"].(string)
	it.itemID, _ = ev.Item["id"].(string)
	it.callID, _ = ev.Item["call_id"].(string)
	it.name, _ = ev.Item["name"].(string)
}

func (a *responsesAcc) applyItemDone(data string) {
	// On done the item is fully populated; merge any fields the added event
	// missed (e.g. name/arguments that only appear on completion).
	var ev struct {
		OutputIndex int            `json:"output_index"`
		Item        map[string]any `json:"item"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil || ev.Item == nil {
		return
	}
	it := a.item(ev.OutputIndex)
	if s, _ := ev.Item["type"].(string); s != "" {
		it.typ = s
	}
	if s, _ := ev.Item["name"].(string); s != "" {
		it.name = s
	}
	if s, _ := ev.Item["call_id"].(string); s != "" {
		it.callID = s
	}
}

func (a *responsesAcc) applyTextDelta(data string) {
	var ev struct {
		OutputIndex int    `json:"output_index"`
		Delta       string `json:"delta"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return
	}
	it := a.item(ev.OutputIndex)
	if it.typ == "" {
		it.typ = "message"
	}
	it.text.WriteString(ev.Delta)
}

func (a *responsesAcc) applyArgsDelta(data string) {
	var ev struct {
		OutputIndex int    `json:"output_index"`
		Delta       string `json:"delta"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return
	}
	it := a.item(ev.OutputIndex)
	if it.typ == "" {
		it.typ = "function_call"
	}
	it.args.WriteString(ev.Delta)
}

func (a *responsesAcc) applyArgsDone(data string) {
	var ev struct {
		OutputIndex int    `json:"output_index"`
		Arguments   string `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil || ev.Arguments == "" {
		return
	}
	it := a.item(ev.OutputIndex)
	if it.typ == "" {
		it.typ = "function_call"
	}
	// The done event carries the full argument string; prefer it over the
	// accumulated fragments to guard against a dropped delta.
	it.args.Reset()
	it.args.WriteString(ev.Arguments)
}

func (a *responsesAcc) applyReasoningDelta(data string) {
	var ev struct {
		OutputIndex int    `json:"output_index"`
		Delta       string `json:"delta"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return
	}
	it := a.item(ev.OutputIndex)
	if it.typ == "" {
		it.typ = "reasoning"
	}
	it.summary.WriteString(ev.Delta)
}

func (a *responsesAcc) applyError(data string) {
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return
	}
	// The error event payload is the error object itself.
	a.err = parseOpenAIError(m)
}

// finalize builds the partial in-stream response from accumulated state.
func (a *responsesAcc) finalize() *ResponsesResponse {
	resp := &ResponsesResponse{
		ID:       a.id,
		Model:    a.model,
		Status:   a.status,
		Streamed: true,
		Error:    a.err,
	}
	indices := make([]int, 0, len(a.items))
	for idx := range a.items {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	for _, idx := range indices {
		it := a.items[idx]
		out := ResponsesItem{Type: it.typ, Role: it.role, ID: it.itemID, CallID: it.callID, Name: it.name}
		switch it.typ {
		case "message", "":
			out.Type = "message"
			if out.Role == "" {
				out.Role = "assistant"
			}
			text := it.text.String()
			out.Content = text
			if text != "" {
				out.Parts = []ResponsesPart{{Type: "output_text", Text: text}}
			}
		case "function_call":
			out.Arguments = it.args.String()
		case "reasoning":
			out.Summary = it.summary.String()
		}
		resp.Output = append(resp.Output, out)
	}
	return resp
}
