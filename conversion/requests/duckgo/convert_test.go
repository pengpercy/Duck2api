package duckgo

import (
	"encoding/json"
	"strings"
	"testing"

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
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "rules") || !strings.Contains(text, "hello") {
		t.Fatalf("messages missing from %s", text)
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
