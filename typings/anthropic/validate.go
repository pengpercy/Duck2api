package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ValidateMessagesRequest checks the required and bounded fields from the
// public Messages API before any upstream work starts.
func ValidateMessagesRequest(req MessagesRequest) error {
	if strings.TrimSpace(req.Model) == "" {
		return fmt.Errorf("model: Field required")
	}
	if req.MaxTokens == nil {
		return fmt.Errorf("max_tokens: Field required")
	}
	if *req.MaxTokens < 0 {
		return fmt.Errorf("max_tokens: Input should be greater than or equal to 0")
	}
	if len(req.Messages) == 0 {
		return fmt.Errorf("messages: List should have at least 1 item")
	}
	if req.Temperature != nil && (*req.Temperature < 0 || *req.Temperature > 1) {
		return fmt.Errorf("temperature: Input should be between 0 and 1")
	}
	if req.TopP != nil && (*req.TopP < 0 || *req.TopP > 1) {
		return fmt.Errorf("top_p: Input should be between 0 and 1")
	}
	if req.TopK != nil && *req.TopK < 0 {
		return fmt.Errorf("top_k: Input should be greater than or equal to 0")
	}
	if len(req.System) > 0 && !validStringOrBlockArray(req.System) {
		return fmt.Errorf("system: Input should be a valid string or content block array")
	}
	for i, message := range req.Messages {
		if message.Role != "user" && message.Role != "assistant" {
			return fmt.Errorf("messages.%d.role: Input should be 'user' or 'assistant'", i)
		}
		if !validStringOrBlockArray(message.Content) {
			return fmt.Errorf("messages.%d.content: Input should be a valid string or content block array", i)
		}
	}
	return nil
}

func validStringOrBlockArray(raw json.RawMessage) bool {
	if len(raw) == 0 || !json.Valid(raw) {
		return false
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return true
	}
	var blocks []json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return false
	}
	for _, block := range blocks {
		var typed struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(block, &typed) != nil || typed.Type == "" {
			return false
		}
	}
	return true
}
