// Package llmparse turns captured HTTP traffic into a structured "analysis"
// the UI can render as a conversation instead of raw JSON. The package is
// extensible: each provider/endpoint maps to its own sub-struct under the
// top-level Analysis, and Kind is the discriminator. Unknown endpoints
// return nil so the renderer falls back to its raw JSON view.
//
// Design goals:
//
//   - Best-effort. Any single missing/unexpected field surfaces as a Warning
//     but never aborts the analysis; the user always gets a partial result
//     plus the unparsed JSON next to it.
//   - Field-oriented, not byte-oriented. We expose what the user wants to
//     see (model, tool calls, tokens, cache hit/miss), not the wire
//     representation of every JSON path.
//   - Forward compatible. Unrecognized blocks land in `unknownFields` /
//     `raw` instead of vanishing; the renderer can show them as a JSON
//     fragment with a hint that this field is new.
package llmparse

// Kind discriminates which analyzer produced an Analysis. New analyzers add
// a new Kind constant and a new pointer field on Analysis.
const (
	KindAnthropicMessages = "anthropic.messages"
)

// Analysis is the top-level structured view of one captured request/response
// pair. Exactly one of the *Analysis fields is non-nil; Kind names which one.
type Analysis struct {
	// Kind names the parser that produced this view. Used by the renderer
	// to pick which structured pane to show.
	Kind string `json:"kind" doc:"Analyzer that produced this view"`
	// Endpoint is a human-readable label, e.g. "POST /v1/messages".
	Endpoint string `json:"endpoint" doc:"Human-readable endpoint label"`
	// Anthropic carries the parsed Messages-API view when Kind == anthropic.messages.
	Anthropic *AnthropicAnalysis `json:"anthropic,omitempty"`
	// Warnings collects soft parse failures: missing fields, unknown event
	// types, JSON that didn't deserialize as expected. Always safe to show
	// to the user — they're not blocking errors.
	Warnings []string `json:"warnings,omitempty"`
}

// AnthropicAnalysis is the parsed view of one /v1/messages call.
type AnthropicAnalysis struct {
	Request  *AnthropicRequest  `json:"request,omitempty"`
	Response *AnthropicResponse `json:"response,omitempty"`
}

// AnthropicRequest mirrors the documented fields of POST /v1/messages.
// Unknown top-level fields go into UnknownFields so the UI can surface them.
type AnthropicRequest struct {
	Model         string             `json:"model,omitempty"`
	MaxTokens     int                `json:"maxTokens,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"topP,omitempty"`
	TopK          *int               `json:"topK,omitempty"`
	Stream        bool               `json:"stream,omitempty"`
	StopSequences []string           `json:"stopSequences,omitempty"`
	System        []AnthropicBlock   `json:"system,omitempty"`
	Tools         []AnthropicTool    `json:"tools,omitempty"`
	ToolChoice    any                `json:"toolChoice,omitempty"`
	Messages      []AnthropicMessage `json:"messages,omitempty"`
	Metadata      any                `json:"metadata,omitempty"`
	// UnknownFields holds top-level keys we don't have a structured slot for
	// yet. Renderer should show them verbatim with a "new field" hint so
	// users know they're looking at something the parser doesn't model.
	UnknownFields map[string]any `json:"unknownFields,omitempty"`
}

// AnthropicMessage is one role-tagged turn in the conversation.
type AnthropicMessage struct {
	Role    string           `json:"role"`
	Content []AnthropicBlock `json:"content"`
}

// AnthropicBlock represents any content block (text / tool_use / tool_result
// / image / thinking / unknown). Type names match the wire format so a
// renderer can switch on Type directly. Fields not relevant for the block's
// type stay at their zero value; Raw carries the original JSON for unknown
// types.
type AnthropicBlock struct {
	Type         string `json:"type"`
	Text         string `json:"text,omitempty"`
	ID           string `json:"id,omitempty"`
	Name         string `json:"name,omitempty"`
	Input        any    `json:"input,omitempty"`
	ToolUseID    string `json:"toolUseId,omitempty"`
	Content      any    `json:"content,omitempty"`
	IsError      bool   `json:"isError,omitempty"`
	CacheControl any    `json:"cacheControl,omitempty"`
	// Raw is the original JSON for blocks whose type the parser doesn't
	// recognize. The renderer should pretty-print it in a fallback box.
	Raw any `json:"raw,omitempty"`
}

// AnthropicTool is one tool definition on the request.
type AnthropicTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"inputSchema,omitempty"`
	Raw         any    `json:"raw,omitempty"`
}

// AnthropicUsage captures the token accounting the response returns. All
// fields are optional because partial streams may only have some of them.
type AnthropicUsage struct {
	InputTokens              int `json:"inputTokens,omitempty"`
	OutputTokens             int `json:"outputTokens,omitempty"`
	CacheReadInputTokens     int `json:"cacheReadInputTokens,omitempty"`
	CacheCreationInputTokens int `json:"cacheCreationInputTokens,omitempty"`
}

// AnthropicError is the Anthropic API error envelope.
type AnthropicError struct {
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
}

// AnthropicResponse is the parsed view of either a non-streamed JSON
// response or the accumulated state of a streamed (SSE) one. Streamed = true
// means we reconstructed the content from event deltas.
type AnthropicResponse struct {
	ID         string           `json:"id,omitempty"`
	Model      string           `json:"model,omitempty"`
	Role       string           `json:"role,omitempty"`
	StopReason string           `json:"stopReason,omitempty"`
	Content    []AnthropicBlock `json:"content,omitempty"`
	Usage      *AnthropicUsage  `json:"usage,omitempty"`
	Streamed   bool             `json:"streamed,omitempty"`
	Error      *AnthropicError  `json:"error,omitempty"`
}
