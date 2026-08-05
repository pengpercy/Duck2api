package duckgo

import (
	"encoding/json"
	"strings"
	"testing"

	duckgotypes "aurora/typings/duckgo"
	officialtypes "aurora/typings/official"
)

func TestConvertAPIRequestWithOptions(t *testing.T) {
	req := officialtypes.APIRequest{Model: "gpt-5.4-mini", Messages: []officialtypes.ApiMessage{{Role: "system", Content: "rules"}, {Role: "user", Content: "hello"}}}
	got := ConvertAPIRequestWithOptions(req, "high", true)
	if got.ReasoningEffort != "low" {
		t.Fatalf("reasoning effort = %q", got.ReasoningEffort)
	}
	if !got.Metadata.ToolChoice.WebSearch {
		t.Fatal("web search was not enabled")
	}
	if len(got.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(got.Messages))
	}
	message, ok := got.Messages[0].(duckgotypes.MessageUser)
	if !ok {
		t.Fatalf("message type = %T", got.Messages[0])
	}
	content, ok := message.Content.(duckgotypes.MessageContent)
	if !ok || len(content.Parts) != 2 || content.Parts[0].Text != "rules" || content.Parts[1].Text != "hello" {
		t.Fatalf("merged content = %#v", message.Content)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "rules") || !strings.Contains(text, "hello") {
		t.Fatalf("messages missing from %s", text)
	}
}

func TestConvertDoesNotMergeTwoUserMessages(t *testing.T) {
	req := officialtypes.APIRequest{Model: "gpt-5.4-mini", Messages: []officialtypes.ApiMessage{
		{Role: "user", Content: "first"},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "reply"},
		{Role: "user", Content: "third"},
	}}

	got := ConvertAPIRequest(req)
	if len(got.Messages) != 4 {
		t.Fatalf("messages = %d, want 4", len(got.Messages))
	}
	if got.Messages[2].(duckgotypes.MessageAssistant).Role != "assistant" {
		t.Fatalf("third message = %#v", got.Messages[2])
	}
}

func TestConvertMultimodalImage(t *testing.T) {
	content := []any{map[string]any{"type": "text", "text": "edit"}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AA=="}}}
	got := ConvertAPIRequest(officialtypes.APIRequest{Model: "gpt-5.4-nano", Messages: []officialtypes.ApiMessage{{Role: "user", Content: content}}})
	b, _ := json.Marshal(got)
	text := string(b)
	if !strings.Contains(text, "data:image/webp;base64,AA==") {
		t.Fatalf("image missing from %s", text)
	}
}
