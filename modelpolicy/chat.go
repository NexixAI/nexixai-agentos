package modelpolicy

// ChatRequest represents an OpenAI-compatible chat completions request.
type ChatRequest struct {
	Model       string         `json:"model"`
	Messages    []ChatMessage  `json:"messages"`
	Tools       []ToolDef      `json:"tools,omitempty"`
	MaxTokens   int            `json:"max_tokens,omitempty"`
	Temperature *float64       `json:"temperature,omitempty"`
	Stream      bool           `json:"stream,omitempty"`
	Extra       map[string]any `json:"-"` // additional fields merged into the JSON payload (e.g. chat_template_kwargs)
}

// ChatMessage represents a single message in a chat conversation.
//
// v11.1: Content changed from `string` to `any` to accept either a plain
// string (backward compat) or a multipart array (OpenAI image_url content
// parts). Downstream readers that need a flat string view should call
// MessageText(msg); JSON serialization is handled natively by Go's
// encoder, so providers re-marshal to upstream correctly either way.
type ChatMessage struct {
	Role       string     `json:"role"`    // system, user, assistant, tool
	Content    any        `json:"content"` // string | []multimodal.ContentPart | []any
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// MessageText returns a flat text view of a ChatMessage's content.
//
// Rules:
//   - string Content → returned as-is (backward compat for every legacy caller).
//   - []any (JSON-decoded multipart) → "text" parts extracted and space-joined.
//   - anything else → empty string.
//
// Safe for call sites that need to feed text into the classifier, PII
// scanner, token estimator, or stub provider — none of which can reason
// about image parts.
func MessageText(m ChatMessage) string {
	switch v := m.Content.(type) {
	case string:
		return v
	case []any:
		return extractTextFromParts(v)
	case []map[string]any:
		converted := make([]any, len(v))
		for i, p := range v {
			converted[i] = p
		}
		return extractTextFromParts(converted)
	default:
		return ""
	}
}

func extractTextFromParts(parts []any) string {
	var chunks []string
	for _, raw := range parts {
		p, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := p["type"].(string); t != "text" {
			continue
		}
		if text, ok := p["text"].(string); ok && text != "" {
			chunks = append(chunks, text)
		}
	}
	if len(chunks) == 0 {
		return ""
	}
	out := chunks[0]
	for _, c := range chunks[1:] {
		out += " " + c
	}
	return out
}

// ToolCall represents a tool invocation requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall represents a function call within a tool call.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ChatResponse represents an OpenAI-compatible chat completions response.
type ChatResponse struct {
	Choices []ChatChoice `json:"choices"`
	Usage   ChatUsage    `json:"usage"`
	Model   string       `json:"model"`
}

// ChatChoice represents a single completion choice.
type ChatChoice struct {
	Index        int         `json:"index,omitempty"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"` // stop, tool_calls, length
}

// ChatUsage represents token usage statistics from a completion.
type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamChunk represents a single chunk in a streaming chat completion.
type StreamChunk struct {
	Index        int         `json:"index,omitempty"`        // choice index, defaults to 0
	Delta        string     `json:"delta,omitempty"`        // text content delta
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`   // partial tool calls
	FinishReason string     `json:"finish_reason,omitempty"`
	Usage        *ChatUsage `json:"usage,omitempty"` // only in final chunk
	Err          error      `json:"-"`               // stream error
}

// ToolDef represents a tool definition sent to the model.
type ToolDef struct {
	Type     string          `json:"type"` // "function"
	Function ToolDefFunction `json:"function"`
}

// ToolDefFunction describes the function within a tool definition.
type ToolDefFunction struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"` // JSON Schema object
}
