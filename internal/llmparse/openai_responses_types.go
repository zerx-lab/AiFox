package llmparse

// OpenAI Responses API (Codex CLI) types. The Responses API is structurally
// distinct from Chat Completions: requests carry `input` (string or item array)
// + `instructions`, and responses carry an `output[]` item array instead of
// `choices[]`. We give it its own analysis sub-struct rather than overloading
// OpenAIAnalysis so the renderer and rollups can switch on a single Kind.

// ResponsesAnalysis is the parsed view of one POST /v1/responses call. It
// mirrors the AnthropicAnalysis / OpenAIAnalysis shape (Request / Response on
// the top-level Analysis carry the structured tree; Warnings live on Analysis).
type ResponsesAnalysis struct {
	Request  *ResponsesRequest  `json:"request,omitempty"`
	Response *ResponsesResponse `json:"response,omitempty"`
}

// ResponsesRequest mirrors the documented core fields of a Responses request.
type ResponsesRequest struct {
	Model string `json:"model,omitempty"`
	// Instructions is the system-equivalent prompt.
	Instructions string `json:"instructions,omitempty"`
	// Input holds the conversation items. A string input is normalized into a
	// single user message item so the renderer has one shape to follow.
	Input []ResponsesItem `json:"input,omitempty"`
	Tools []ResponsesTool `json:"tools,omitempty"`
	// PreviousResponseID chains a stateful conversation to its prior response.
	// The session aggregator prefers this over fingerprint matching.
	PreviousResponseID string   `json:"previousResponseId,omitempty"`
	Store              *bool    `json:"store,omitempty"`
	Stream             bool     `json:"stream,omitempty"`
	MaxOutputTokens    int      `json:"maxOutputTokens,omitempty"`
	Temperature        *float64 `json:"temperature,omitempty"`
	// Reasoning is the structured reasoning config (effort / summary).
	Reasoning *ResponsesReasoningConfig `json:"reasoning,omitempty"`
	// Text holds the raw `text` (output format) config; not modeled further.
	Text any `json:"text,omitempty"`
	// UnknownFields holds top-level keys with no structured slot, surfaced as a
	// warning so users see the parser is behind the wire format.
	UnknownFields map[string]any `json:"unknownFields,omitempty"`
}

// ResponsesReasoningConfig is the request's reasoning configuration.
type ResponsesReasoningConfig struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// ResponsesTool is one tool definition. Responses function tools are FLAT
// ({type:"function",name,description,parameters}) unlike Chat Completions'
// nested {type:"function",function:{...}}. Built-in tools (web_search etc.) are
// kept in Raw with their Type recorded.
type ResponsesTool struct {
	Type        string `json:"type,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
	// Raw carries the original JSON for non-function (built-in) tool types so
	// the renderer can show them verbatim.
	Raw any `json:"raw,omitempty"`
}

// ResponsesItem is one item in the request input array or the response output
// array. Type names match the wire format so the renderer can switch on Type.
// Fields irrelevant to the item's type stay at their zero value; Raw carries the
// original JSON for unrecognized item types.
type ResponsesItem struct {
	Type string `json:"type,omitempty"`
	// Role is set for message items (user/assistant/system/developer).
	Role string `json:"role,omitempty"`
	// Content is the flattened text of a message item's content parts
	// (input_text / output_text / refusal / input_image alt is dropped).
	Content string `json:"content,omitempty"`
	// Parts holds the structured content parts for a message item.
	Parts []ResponsesPart `json:"parts,omitempty"`
	// ID / CallID / Name / Arguments populate function_call items; CallID /
	// Output populate function_call_output items.
	ID        string `json:"id,omitempty"`
	CallID    string `json:"callId,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
	// Summary holds the flattened reasoning summary text for reasoning items.
	Summary string `json:"summary,omitempty"`
	// Status mirrors an output item's status (in_progress / completed) when set.
	Status string `json:"status,omitempty"`
	// Raw carries the original JSON for item types the parser doesn't model.
	Raw any `json:"raw,omitempty"`
}

// ResponsesPart is one content part of a message item.
type ResponsesPart struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
	// Image carries the structured image source for input_image parts (URL /
	// detail), never an inlined base64 payload.
	Image *ResponsesImage `json:"image,omitempty"`
	// Raw carries the original JSON for unrecognized part types.
	Raw any `json:"raw,omitempty"`
}

// ResponsesImage is the structured view of an input_image part.
type ResponsesImage struct {
	URL    string `json:"url,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// ResponsesResponse is the parsed view of a non-streamed JSON response or the
// accumulated state of a streamed one. Streamed = true means it was
// reconstructed from SSE events.
type ResponsesResponse struct {
	ID         string               `json:"id,omitempty"`
	Model      string               `json:"model,omitempty"`
	Status     string               `json:"status,omitempty"`
	Output     []ResponsesItem      `json:"output,omitempty"`
	Usage      *ResponsesUsage      `json:"usage,omitempty"`
	Streamed   bool                 `json:"streamed,omitempty"`
	Error      *OpenAIError         `json:"error,omitempty"`
	Incomplete *ResponsesIncomplete `json:"incomplete,omitempty"`
}

// ResponsesIncomplete carries the incomplete_details reason (e.g. max_output_tokens).
type ResponsesIncomplete struct {
	Reason string `json:"reason,omitempty"`
}

// ResponsesUsage is the token accounting. Field names differ from Chat
// Completions: input_tokens / output_tokens, with cached tokens under
// input_tokens_details and reasoning tokens under output_tokens_details.
type ResponsesUsage struct {
	InputTokens     int `json:"inputTokens,omitempty"`
	OutputTokens    int `json:"outputTokens,omitempty"`
	TotalTokens     int `json:"totalTokens,omitempty"`
	CachedTokens    int `json:"cachedTokens,omitempty"`
	ReasoningTokens int `json:"reasoningTokens,omitempty"`
}
