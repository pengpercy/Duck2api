package official

import "strings"

type APIRequest struct {
	Messages        []ApiMessage `json:"messages"`
	Stream          bool         `json:"stream"`
	Model           string       `json:"model"`
	PluginIDs       []string     `json:"plugin_ids"`
	ReasoningEffort string       `json:"reasoning_effort,omitempty"`
	WebSearch       *bool        `json:"web_search,omitempty"`
	FileIDs         []string     `json:"file_ids,omitempty"`
}

type ApiMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type ResponseAPIRequest struct {
	Model              string                 `json:"model"`
	Input              any                    `json:"input"`
	Instructions       any                    `json:"instructions,omitempty"`
	Stream             bool                   `json:"stream,omitempty"`
	Reasoning          *ResponseReasoning     `json:"reasoning,omitempty"`
	ReasoningEffort    string                 `json:"reasoning_effort,omitempty"`
	PreviousResponseID *string                `json:"previous_response_id,omitempty"`
	Metadata           map[string]interface{} `json:"metadata,omitempty"`
	MaxOutputTokens    *int                   `json:"max_output_tokens,omitempty"`
	ParallelToolCalls  *bool                  `json:"parallel_tool_calls,omitempty"`
	Store              *bool                  `json:"store,omitempty"`
	Temperature        *float64               `json:"temperature,omitempty"`
	TopP               *float64               `json:"top_p,omitempty"`
	ToolChoice         any                    `json:"tool_choice,omitempty"`
	Tools              []any                  `json:"tools,omitempty"`
	Truncation         string                 `json:"truncation,omitempty"`
	Text               any                    `json:"text,omitempty"`
}

func (r ResponseAPIRequest) ToChatCompletionRequest() APIRequest {
	effort := r.ResolvedReasoningEffort()
	req := APIRequest{Model: r.Model, Stream: r.Stream, ReasoningEffort: effort}
	for _, rawTool := range r.Tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		toolType, _ := tool["type"].(string)
		if toolType == "web_search" || toolType == "web_search_preview" {
			enabled := true
			req.WebSearch = &enabled
			break
		}
	}
	if req.Model == "" {
		req.Model = "gpt-5-mini"
	}
	if instructions := responseInstructionsText(r.Instructions); instructions != "" {
		req.Messages = append(req.Messages, ApiMessage{Role: "system", Content: instructions})
	}
	switch input := r.Input.(type) {
	case string:
		req.Messages = append(req.Messages, ApiMessage{Role: "user", Content: input})
	case []any:
		for _, raw := range input {
			if item, ok := raw.(map[string]any); ok {
				itemType, _ := item["type"].(string)
				if itemType == "input_text" || itemType == "input_image" || itemType == "input_file" {
					if content := responseInputContent([]any{item}); content != nil {
						req.Messages = append(req.Messages, ApiMessage{Role: "user", Content: content})
					}
					continue
				}
				role, _ := item["role"].(string)
				if role == "" {
					role = "user"
				}
				if content := responseInputContent(item["content"]); content != nil {
					req.Messages = append(req.Messages, ApiMessage{Role: role, Content: content})
				}
			}
		}
	}
	return req
}

func responseInstructionsText(instructions any) string {
	if text, ok := instructions.(string); ok {
		return text
	}
	items, ok := instructions.([]any)
	if !ok {
		return ""
	}
	var out strings.Builder
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		text := responseContentText(item["content"])
		if text == "" {
			text, _ = item["text"].(string)
		}
		if text != "" {
			if out.Len() > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(text)
		}
	}
	return out.String()
}

func responseInputContent(content any) any {
	if text, ok := content.(string); ok {
		if text == "" {
			return nil
		}
		return text
	}
	parts, ok := content.([]any)
	if !ok {
		return nil
	}
	converted := make([]any, 0, len(parts))
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := part["type"].(string)
		switch kind {
		case "input_text", "output_text", "text", "":
			if text, _ := part["text"].(string); text != "" {
				converted = append(converted, map[string]any{"type": "text", "text": text})
			}
		case "input_image":
			if imageURL, _ := part["image_url"].(string); imageURL != "" {
				converted = append(converted, map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL}})
			}
		case "input_file":
			converted = append(converted, part)
		}
	}
	if len(converted) == 0 {
		return nil
	}
	if len(converted) == 1 {
		if part, ok := converted[0].(map[string]any); ok && part["type"] == "text" {
			return part["text"]
		}
	}
	return converted
}

func (r ResponseAPIRequest) ResolvedReasoningEffort() string {
	if r.ReasoningEffort != "" {
		return r.ReasoningEffort
	}
	if r.Reasoning != nil && r.Reasoning.Effort != nil {
		return *r.Reasoning.Effort
	}
	return ""
}

func responseContentText(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	parts, ok := content.([]any)
	if !ok {
		return ""
	}
	var out strings.Builder
	for _, raw := range parts {
		if part, ok := raw.(map[string]any); ok {
			kind, _ := part["type"].(string)
			if kind == "input_text" || kind == "output_text" || kind == "text" || kind == "" {
				if text, ok := part["text"].(string); ok {
					out.WriteString(text)
				}
			}
		}
	}
	return out.String()
}

type ResponseReasoning struct {
	Effort  *string `json:"effort"`
	Summary *string `json:"summary"`
	Context *string `json:"context,omitempty"`
}

type OpenAISessionToken struct {
	SessionToken string `json:"session_token"`
}

type OpenAIRefreshToken struct {
	RefreshToken string `json:"refresh_token"`
}
