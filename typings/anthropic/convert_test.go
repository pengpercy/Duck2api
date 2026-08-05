package anthropic

import (
	duckgoConvert "aurora/conversion/requests/duckgo"
	"encoding/json"
	"strings"
	"testing"
)

func TestToOpenAPIRequestConvertsSystemImageAndTools(t *testing.T) {
	req := MessagesRequest{
		Model:  "claude-haiku-4-5",
		System: json.RawMessage(`[{"type":"text","text":"follow rules"}]`),
		Messages: []Message{
			{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"describe"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]`)},
			{Role: "assistant", Content: json.RawMessage(`[{"type":"tool_use","id":"toolu_1","name":"lookup","input":{"q":"duck"}}]`)},
			{Role: "user", Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"toolu_1","content":"result"}]`)},
		},
	}
	got := ToOpenAPIRequest(req)
	if len(got.Messages) != 4 {
		t.Fatalf("messages = %d", len(got.Messages))
	}
	b, _ := json.Marshal(got)
	text := string(b)
	for _, want := range []string{"follow rules", "data:image/png;base64,AA==", "Tool call: lookup", "Tool result for toolu_1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q from %s", want, text)
		}
	}
	upstream, err := json.Marshal(duckgoConvert.ConvertAPIRequest(got))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(upstream), "data:image/webp;base64,AA==") {
		t.Fatalf("image lost in upstream conversion: %s", upstream)
	}
}

func TestThinkingToEffort(t *testing.T) {
	cases := map[string]string{`{"type":"disabled"}`: "none", `{"type":"enabled","budget_tokens":1024}`: "low", `{"type":"enabled","budget_tokens":6000}`: "medium", `{"type":"enabled","budget_tokens":12000}`: "high"}
	for raw, want := range cases {
		if got := ThinkingToEffort(json.RawMessage(raw)); got != want {
			t.Fatalf("ThinkingToEffort(%s)=%q want %q", raw, got, want)
		}
	}
}
