package official

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

type ChatCompletionChunk struct {
	ID      string    `json:"id"`
	Object  string    `json:"object"`
	Created int64     `json:"created"`
	Model   string    `json:"model"`
	Choices []Choices `json:"choices"`
	// Usage is only populated on the final chunk (stream_options.include_usage).
	Usage *usage `json:"usage,omitempty"`
	// Timing is only populated on the final usage chunk.
	Timing *Timing `json:"timing,omitempty"`
	// ReasoningEffort echoes the resolved thinking-effort level (non-standard extra field).
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

func (chunk *ChatCompletionChunk) String() string {
	resp, _ := json.Marshal(chunk)
	return string(resp)
}

type Choices struct {
	Delta        Delta       `json:"delta"`
	Index        int         `json:"index"`
	FinishReason interface{} `json:"finish_reason"`
}

type Delta struct {
	Content string `json:"content,omitempty"`
	Role    string `json:"role,omitempty"`
}

func NewChatCompletionChunk(text string) ChatCompletionChunk {
	return ChatCompletionChunk{
		ID:      "chatcmpl-QXlha2FBbmROaXhpZUFyZUF3ZXNvbWUK",
		Object:  "chat.completion.chunk",
		Created: 0,
		Model:   "gpt-4o-mini",
		Choices: []Choices{
			{
				Index: 0,
				Delta: Delta{
					Content: text,
				},
				FinishReason: nil,
			},
		},
	}
}

func NewChatCompletionChunkWithModel(text string, model string) ChatCompletionChunk {
	return ChatCompletionChunk{
		ID:      "chatcmpl-QXlha2FBbmROaXhpZUFyZUF3ZXNvbWUK",
		Object:  "chat.completion.chunk",
		Created: 0,
		Model:   model,
		Choices: []Choices{
			{
				Index: 0,
				Delta: Delta{
					Content: text,
				},
				FinishReason: nil,
			},
		},
	}
}

func StopChunkWithModel(reason string, model string) ChatCompletionChunk {
	return ChatCompletionChunk{
		ID:      "chatcmpl-QXlha2FBbmROaXhpZUFyZUF3ZXNvbWUK",
		Object:  "chat.completion.chunk",
		Created: 0,
		Model:   model,
		Choices: []Choices{
			{
				Index:        0,
				FinishReason: reason,
			},
		},
	}
}

func StopChunk(reason string) ChatCompletionChunk {
	return ChatCompletionChunk{
		ID:      "chatcmpl-QXlha2FBbmROaXhpZUFyZUF3ZXNvbWUK",
		Object:  "chat.completion.chunk",
		Created: 0,
		Model:   "gpt-4o-mini",
		Choices: []Choices{
			{
				Index:        0,
				FinishReason: reason,
			},
		},
	}
}

// UsageChunk emits the final chunk carrying usage + timing (OpenAI stream_options.include_usage compatible).
func UsageChunk(model string, promptTokens, completionTokens, cachedTokens int, ttftMs, totalMs int64, effort string) ChatCompletionChunk {
	tt := promptTokens + completionTokens
	var details *promptTokensDetails
	if cachedTokens > 0 {
		details = &promptTokensDetails{CachedTokens: cachedTokens}
	}
	return ChatCompletionChunk{
		ID:      "chatcmpl-QXlha2FBbmROaXhpZUFyZUF3ZXNvbWUK",
		Object:  "chat.completion.chunk",
		Created: 0,
		Model:   model,
		Choices: []Choices{},
		Usage: &usage{
			PromptTokens:        promptTokens,
			CompletionTokens:    completionTokens,
			TotalTokens:         tt,
			PromptTokensDetails: details,
		},
		Timing: &Timing{
			TTFTMs:  ttftMs,
			TotalMs: totalMs,
		},
		ReasoningEffort: effort,
	}
}

type ChatCompletion struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Usage   usage    `json:"usage"`
	Choices []Choice `json:"choices"`
	// Timing carries TTFT and total duration (non-standard extra field).
	Timing *Timing `json:"timing,omitempty"`
	// ReasoningEffort echoes the resolved thinking-effort level (non-standard extra field).
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}
type Msg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type Choice struct {
	Index        int         `json:"index"`
	Message      Msg         `json:"message"`
	FinishReason interface{} `json:"finish_reason"`
}
type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// PromptTokensDetails holds cache breakdown (OpenAI-compatible).
	PromptTokensDetails *promptTokensDetails `json:"prompt_tokens_details,omitempty"`
}

type promptTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

// Timing is attached to the final usage chunk (non-standard but harmless extra fields).
type Timing struct {
	TTFTMs  int64 `json:"ttft_ms,omitempty"`  // time to first token (ms)
	TotalMs int64 `json:"total_ms,omitempty"` // total request time (ms)
}

type ResponseAPI struct {
	ID                 string                 `json:"id"`
	Object             string                 `json:"object"`
	CreatedAt          int64                  `json:"created_at"`
	CompletedAt        *int64                 `json:"completed_at"`
	Status             string                 `json:"status"`
	Model              string                 `json:"model"`
	Output             []ResponseOutput       `json:"output"`
	Usage              *ResponseUsage         `json:"usage"`
	ParallelToolCalls  bool                   `json:"parallel_tool_calls"`
	PreviousResponseID *string                `json:"previous_response_id"`
	Error              interface{}            `json:"error"`
	IncompleteDetails  interface{}            `json:"incomplete_details"`
	Metadata           map[string]interface{} `json:"metadata"`
	Instructions       any                    `json:"instructions"`
	MaxOutputTokens    *int                   `json:"max_output_tokens"`
	Reasoning          ResponseReasoning      `json:"reasoning"`
	Store              bool                   `json:"store"`
	Temperature        float64                `json:"temperature"`
	Text               any                    `json:"text"`
	ToolChoice         any                    `json:"tool_choice"`
	Tools              []any                  `json:"tools"`
	TopP               float64                `json:"top_p"`
	Truncation         string                 `json:"truncation"`
	User               any                    `json:"user"`
}

type ResponseOutput struct {
	ID      string                  `json:"id"`
	Type    string                  `json:"type"`
	Status  string                  `json:"status"`
	Role    string                  `json:"role"`
	Content []ResponseOutputContent `json:"content"`
}

type ResponseOutputContent struct {
	Type        string        `json:"type"`
	Text        string        `json:"text"`
	Annotations []interface{} `json:"annotations"`
}

type ResponseUsage struct {
	InputTokens         int                        `json:"input_tokens"`
	OutputTokens        int                        `json:"output_tokens"`
	TotalTokens         int                        `json:"total_tokens"`
	InputTokensDetails  ResponseInputTokenDetails  `json:"input_tokens_details"`
	OutputTokensDetails ResponseOutputTokenDetails `json:"output_tokens_details"`
}

type ResponseInputTokenDetails struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}

type ResponseOutputTokenDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type ResponseStreamEvent struct {
	Type         string          `json:"type"`
	Sequence     int             `json:"sequence_number"`
	Response     *ResponseAPI    `json:"response,omitempty"`
	Item         *ResponseOutput `json:"item,omitempty"`
	ItemID       string          `json:"item_id,omitempty"`
	Part         any             `json:"part,omitempty"`
	OutputIndex  *int            `json:"output_index,omitempty"`
	ContentIndex *int            `json:"content_index,omitempty"`
	Delta        *string         `json:"delta,omitempty"`
	Text         *string         `json:"text,omitempty"`
	Logprobs     *[]any          `json:"logprobs,omitempty"`
}

func (event ResponseStreamEvent) String() string {
	resp, _ := json.Marshal(event)
	return string(resp)
}

func NewResponseAPIWithModel(text string, model string) ResponseAPI {
	return NewResponseAPIFull(text, model, 0, 0, 0, "")
}

// NewResponseAPIFull builds a completed Response with usage details.
func NewResponseAPIFull(text, model string, inputTokens, outputTokens, cachedTokens int64, effort string) ResponseAPI {
	createdAt := time.Now().Unix()
	completedAt := createdAt
	var effortValue *string
	if effort != "" {
		effortValue = &effort
	}
	return ResponseAPI{
		ID:          newResponseID("resp_"),
		Object:      "response",
		CreatedAt:   createdAt,
		CompletedAt: &completedAt,
		Status:      "completed",
		Model:       model,
		Output: []ResponseOutput{
			NewResponseOutput(text),
		},
		Usage: &ResponseUsage{
			InputTokens:         int(inputTokens),
			OutputTokens:        int(outputTokens),
			TotalTokens:         int(inputTokens + outputTokens),
			InputTokensDetails:  ResponseInputTokenDetails{CachedTokens: int(cachedTokens)},
			OutputTokensDetails: ResponseOutputTokenDetails{},
		},
		ParallelToolCalls: true,
		Metadata:          map[string]interface{}{},
		Reasoning:         ResponseReasoning{Effort: effortValue},
		Store:             true,
		Temperature:       1,
		Text:              map[string]any{"format": map[string]any{"type": "text"}},
		ToolChoice:        "auto",
		Tools:             []any{},
		TopP:              1,
		Truncation:        "disabled",
	}
}

func (response *ResponseAPI) ApplyRequest(request ResponseAPIRequest) {
	response.Instructions = request.Instructions
	response.PreviousResponseID = request.PreviousResponseID
	if request.Metadata != nil {
		response.Metadata = request.Metadata
	}
	response.MaxOutputTokens = request.MaxOutputTokens
	if request.ParallelToolCalls != nil {
		response.ParallelToolCalls = *request.ParallelToolCalls
	}
	if request.Store != nil {
		response.Store = *request.Store
	}
	if request.Temperature != nil {
		response.Temperature = *request.Temperature
	}
	if request.TopP != nil {
		response.TopP = *request.TopP
	}
	if request.Reasoning != nil {
		response.Reasoning = *request.Reasoning
	}
	if request.Text != nil {
		response.Text = request.Text
	}
	if request.ToolChoice != nil {
		response.ToolChoice = request.ToolChoice
	}
	if request.Tools != nil {
		response.Tools = request.Tools
	}
	if request.Truncation != "" {
		response.Truncation = request.Truncation
	}
}

func NewResponseOutput(text string) ResponseOutput {
	return ResponseOutput{
		ID:     newResponseID("msg_"),
		Type:   "message",
		Status: "completed",
		Role:   "assistant",
		Content: []ResponseOutputContent{
			{
				Type:        "output_text",
				Text:        text,
				Annotations: []interface{}{},
			},
		},
	}
}

func newResponseID(prefix string) string {
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		return prefix + hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return prefix + hex.EncodeToString(random)
}

func NewChatCompletionWithModel(text string, model string) ChatCompletion {
	return NewChatCompletionFull(text, model, 0, 0, 0, 0, 0, "")
}

// NewChatCompletionFull builds a non-stream ChatCompletion with usage, cache breakdown and timing.
func NewChatCompletionFull(text, model string, promptTokens, completionTokens, cachedTokens, ttftMs, totalMs int64, effort string) ChatCompletion {
	var details *promptTokensDetails
	if cachedTokens > 0 {
		details = &promptTokensDetails{CachedTokens: int(cachedTokens)}
	}
	return ChatCompletion{
		ID:      "chatcmpl-QXlha2FBbmROaXhpZUFyZUF3ZXNvbWUK",
		Object:  "chat.completion",
		Created: int64(0),
		Model:   model,
		Usage: usage{
			PromptTokens:        int(promptTokens),
			CompletionTokens:    int(completionTokens),
			TotalTokens:         int(promptTokens + completionTokens),
			PromptTokensDetails: details,
		},
		Timing: &Timing{
			TTFTMs:  ttftMs,
			TotalMs: totalMs,
		},
		ReasoningEffort: effort,
		Choices: []Choice{
			{
				Message: Msg{
					Content: text,
					Role:    "assistant",
				},
				Index: 0,
			},
		},
	}
}

func NewChatCompletion(full_test string, input_tokens, output_tokens int) ChatCompletion {
	return ChatCompletion{
		ID:      "chatcmpl-QXlha2FBbmROaXhpZUFyZUF3ZXNvbWUK",
		Object:  "chat.completion",
		Created: int64(0),
		Model:   "gpt-4o-mini",
		Usage: usage{
			PromptTokens:     input_tokens,
			CompletionTokens: output_tokens,
			TotalTokens:      input_tokens + output_tokens,
		},
		Choices: []Choice{
			{
				Message: Msg{
					Content: full_test,
					Role:    "assistant",
				},
				Index: 0,
			},
		},
	}
}
