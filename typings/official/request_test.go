package official

import "testing"

func TestResponseAPIRequestToChatCompletion(t *testing.T) {
	req := ResponseAPIRequest{Model: "gpt-5.4-nano", Instructions: "be concise", Input: []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hello"}}}}}
	chat := req.ToChatCompletionRequest()
	if len(chat.Messages) != 2 {
		t.Fatalf("got %d messages", len(chat.Messages))
	}
	if chat.Messages[1].Content != "hello" {
		t.Fatalf("content = %#v", chat.Messages[1].Content)
	}
}

func TestResponseAPIRequestNestedReasoning(t *testing.T) {
	effort := "high"
	req := ResponseAPIRequest{
		Model:        "gpt-5.4-nano",
		Instructions: "be concise",
		Input:        "hello",
		Reasoning:    &ResponseReasoning{Effort: &effort},
	}

	chat := req.ToChatCompletionRequest()
	if chat.ReasoningEffort != "high" {
		t.Fatalf("reasoning effort = %q", chat.ReasoningEffort)
	}
	if len(chat.Messages) != 2 || chat.Messages[0].Content != "be concise" || chat.Messages[1].Content != "hello" {
		t.Fatalf("messages = %#v", chat.Messages)
	}
}

func TestResponseAPIRequestPreservesMultimodalInput(t *testing.T) {
	req := ResponseAPIRequest{
		Model: "gpt-5.4-nano",
		Input: []any{map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": "describe"},
				map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AA=="},
				map[string]any{"type": "input_file", "file_id": "file-123"},
			},
		}},
		Tools: []any{map[string]any{"type": "web_search"}},
	}

	chat := req.ToChatCompletionRequest()
	if chat.WebSearch == nil || !*chat.WebSearch {
		t.Fatal("web search tool was not enabled")
	}
	parts, ok := chat.Messages[0].Content.([]any)
	if !ok || len(parts) != 3 {
		t.Fatalf("content = %#v", chat.Messages[0].Content)
	}
	if parts[1].(map[string]any)["type"] != "image_url" || parts[2].(map[string]any)["type"] != "input_file" {
		t.Fatalf("parts = %#v", parts)
	}
}
