package initialize

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	officialtypes "aurora/typings/official"

	"github.com/gin-gonic/gin"
)

func TestResponsesStreamMatchesOpenAIEventShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	request := officialtypes.ResponseAPIRequest{
		Model:        "gpt-5.4-nano",
		Instructions: "简洁回答",
		Input:        "你好",
		Stream:       true,
	}
	upstream := strings.NewReader("data: {\"message\":\"你好\"}\n\ndata: {\"message\":\"！\"}\n\ndata: [DONE]\n\n")

	(&Handler{}).responsesStream(context, upstream, request.Model, request, 2)

	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	events := decodeResponseEvents(t, recorder.Body.String())
	if len(events) != 9 {
		t.Fatalf("event count = %d, body = %s", len(events), recorder.Body.String())
	}
	for index, event := range events {
		if got := int(event["sequence_number"].(float64)); got != index+1 {
			t.Fatalf("event %d sequence_number = %d", index, got)
		}
	}

	created := events[0]["response"].(map[string]any)
	if created["status"] != "in_progress" || created["usage"] != nil {
		t.Fatalf("created response = %#v", created)
	}
	if output := created["output"].([]any); len(output) != 0 {
		t.Fatalf("created output = %#v", output)
	}
	if created["instructions"] != "简洁回答" || created["created_at"].(float64) <= 0 {
		t.Fatalf("created response metadata = %#v", created)
	}
	responseID := created["id"].(string)

	added := events[1]
	assertZeroIndex(t, added, "output_index")
	item := added["item"].(map[string]any)
	if content := item["content"].([]any); len(content) != 0 {
		t.Fatalf("added item content = %#v", content)
	}
	messageID := item["id"].(string)

	for _, index := range []int{2, 3, 4, 5, 6} {
		assertZeroIndex(t, events[index], "output_index")
		assertZeroIndex(t, events[index], "content_index")
		if events[index]["item_id"] != messageID {
			t.Fatalf("event %d item_id = %#v", index, events[index]["item_id"])
		}
	}
	assertZeroIndex(t, events[7], "output_index")
	if logprobs := events[3]["logprobs"].([]any); len(logprobs) != 0 {
		t.Fatalf("delta logprobs = %#v", logprobs)
	}

	completed := events[8]["response"].(map[string]any)
	if completed["id"] != responseID || completed["status"] != "completed" {
		t.Fatalf("completed response = %#v", completed)
	}
	completedOutput := completed["output"].([]any)[0].(map[string]any)
	if completedOutput["id"] != messageID {
		t.Fatalf("completed message id = %#v", completedOutput["id"])
	}
	usage := completed["usage"].(map[string]any)
	if _, ok := usage["input_tokens_details"].(map[string]any); !ok {
		t.Fatalf("usage = %#v", usage)
	}
	if _, ok := usage["output_tokens_details"].(map[string]any); !ok {
		t.Fatalf("usage = %#v", usage)
	}
	for _, nonStandard := range []string{"output_text", "timing", "reasoning_effort"} {
		if _, exists := completed[nonStandard]; exists {
			t.Fatalf("completed response contains non-standard %q", nonStandard)
		}
	}
}

func decodeResponseEvents(t *testing.T, body string) []map[string]any {
	t.Helper()
	blocks := strings.Split(strings.TrimSpace(body), "\n\n")
	events := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		lines := strings.Split(block, "\n")
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "event: ") || !strings.HasPrefix(lines[1], "data: ") {
			t.Fatalf("invalid SSE block %q", block)
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		if event["type"] != strings.TrimPrefix(lines[0], "event: ") {
			t.Fatalf("event name mismatch in %q", block)
		}
		events = append(events, event)
	}
	return events
}

func assertZeroIndex(t *testing.T, event map[string]any, field string) {
	t.Helper()
	value, exists := event[field]
	if !exists || value != float64(0) {
		t.Fatalf("%s in %#v", field, event)
	}
}
