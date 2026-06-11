package llmparse

// OpenAIAnalysis is the parsed view of one /v1/chat/completions or
// /v1/completions call. It mirrors the AnthropicAnalysis shape (Request /
// Response / Warnings live on the top-level Analysis) so the renderer has one
// pattern to follow.
type OpenAIAnalysis struct {
	Request  *OpenAIRequest  `json:"request,omitempty"`
	Response *OpenAIResponse `json:"response,omitempty"`
}

// OpenAIRequest mirrors the documented core fields of a Chat Completions request
// (and the legacy text-completion request, which reuses Model/Stream/usage).
type OpenAIRequest struct {
	Model       string          `json:"model,omitempty"`
	Messages    []OpenAIMessage `json:"messages,omitempty"`
	Tools       []OpenAITool    `json:"tools,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"topP,omitempty"`
	MaxTokens   int             `json:"maxTokens,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	// Prompt holds the legacy text-completion prompt (string or array of
	// strings flattened with newlines). Empty for chat requests.
	Prompt string `json:"prompt,omitempty"`
	// IncludeUsage mirrors stream_options.include_usage — when true the stream
	// ends with a usage-only chunk.
	IncludeUsage bool `json:"includeUsage,omitempty"`
	// UnknownFields holds top-level keys with no structured slot, surfaced as a
	// warning so users see the parser is behind the wire format.
	UnknownFields map[string]any `json:"unknownFields,omitempty"`
}

// OpenAIMessage is one role-tagged message. Content is collapsed to text for
// display; ToolCalls carries the modern tool_calls array and FunctionCall the
// legacy single function_call object.
type OpenAIMessage struct {
	Role         string              `json:"role,omitempty"`
	Content      string              `json:"content,omitempty"`
	Name         string              `json:"name,omitempty"`
	ToolCallID   string              `json:"toolCallId,omitempty"`
	ToolCalls    []OpenAIToolCall    `json:"toolCalls,omitempty"`
	FunctionCall *OpenAIFunctionCall `json:"functionCall,omitempty"`
}

// OpenAIToolCall is one entry of the modern tool_calls array.
type OpenAIToolCall struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// OpenAIFunctionCall is the legacy single function_call object.
type OpenAIFunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// OpenAITool is one tool/function definition from the request.
type OpenAITool struct {
	Type        string `json:"type,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

// OpenAIResponse is the parsed view of a non-streamed JSON response or the
// accumulated state of a streamed one. Streamed = true means it was
// reconstructed from delta chunks.
type OpenAIResponse struct {
	ID       string         `json:"id,omitempty"`
	Model    string         `json:"model,omitempty"`
	Choices  []OpenAIChoice `json:"choices,omitempty"`
	Usage    *OpenAIUsage   `json:"usage,omitempty"`
	Streamed bool           `json:"streamed,omitempty"`
	Error    *OpenAIError   `json:"error,omitempty"`
}

// OpenAIChoice is one completion choice. Message holds the reconstructed
// assistant message; Text holds the legacy text-completion output; FinishReason
// is the raw provider stop reason.
type OpenAIChoice struct {
	Index        int            `json:"index"`
	Message      *OpenAIMessage `json:"message,omitempty"`
	Text         string         `json:"text,omitempty"`
	FinishReason string         `json:"finishReason,omitempty"`
}

// OpenAIUsage is the token accounting. CachedTokens comes from
// prompt_tokens_details.cached_tokens.
type OpenAIUsage struct {
	PromptTokens     int `json:"promptTokens,omitempty"`
	CompletionTokens int `json:"completionTokens,omitempty"`
	TotalTokens      int `json:"totalTokens,omitempty"`
	CachedTokens     int `json:"cachedTokens,omitempty"`
}

// OpenAIError is the OpenAI error envelope.
type OpenAIError struct {
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}
