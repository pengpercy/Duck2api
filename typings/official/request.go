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
	Model           string `json:"model"`
	Input           any    `json:"input"`
	Instructions    string `json:"instructions,omitempty"`
	Stream          bool   `json:"stream,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

func (r ResponseAPIRequest) ToChatCompletionRequest() APIRequest {
	req := APIRequest{Model: r.Model, Stream: r.Stream, ReasoningEffort: r.ReasoningEffort}
	if req.Model == "" {
		req.Model = "gpt-5-mini"
	}
	if strings.TrimSpace(r.Instructions) != "" {
		req.Messages = append(req.Messages, ApiMessage{Role: "system", Content: r.Instructions})
	}
	switch input := r.Input.(type) {
	case string:
		req.Messages = append(req.Messages, ApiMessage{Role: "user", Content: input})
	case []any:
		for _, raw := range input {
			if item, ok := raw.(map[string]any); ok {
				if itemType, _ := item["type"].(string); itemType == "input_text" {
					if text, _ := item["text"].(string); text != "" {
						req.Messages = append(req.Messages, ApiMessage{Role: "user", Content: text})
					}
					continue
				}
				role, _ := item["role"].(string)
				if role == "" {
					role = "user"
				}
				if content := responseContentText(item["content"]); content != "" {
					req.Messages = append(req.Messages, ApiMessage{Role: role, Content: content})
				}
			}
		}
	}
	return req
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

type OpenAISessionToken struct {
	SessionToken string `json:"session_token"`
}

type OpenAIRefreshToken struct {
	RefreshToken string `json:"refresh_token"`
}
