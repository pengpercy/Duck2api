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
