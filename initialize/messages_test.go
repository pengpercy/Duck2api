package initialize

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestHandleAnthropicStreamEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	body := io.NopCloser(strings.NewReader("data: {\"message\":\"hello\"}\n\ndata: [DONE]\n\n"))
	result, err := handleAnthropicStream(context, body, "claude-haiku-4-5", true, time.Now(), 3, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.text != "hello" {
		t.Fatalf("text=%q", result.text)
	}
	output := recorder.Body.String()
	for _, event := range []string{"event: message_start", "event: content_block_start", "event: content_block_delta", "event: content_block_stop", "event: message_delta", "event: message_stop"} {
		if !strings.Contains(output, event) {
			t.Fatalf("missing %q from %s", event, output)
		}
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
		t.Fatalf("content-type=%q", got)
	}
	if !strings.Contains(output, `"output_tokens":1`) {
		t.Fatalf("message_delta usage is not Anthropic-shaped: %s", output)
	}
	if !strings.Contains(output, `"content_block":{"type":"text","text":""}`) {
		t.Fatalf("text content block start is incomplete: %s", output)
	}
}

func TestHandleAnthropicStreamProcessesFinalLineWithoutNewline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	body := io.NopCloser(strings.NewReader(`data: {"message":"hello"}`))
	result, err := handleAnthropicStream(context, body, "claude-haiku-4-5", false, time.Now(), 3, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.text != "hello" {
		t.Fatalf("text=%q", result.text)
	}
}

func TestWriteAnthropicErrorEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	writeAnthropicError(context, http.StatusBadRequest, "invalid_request_error", "bad request", "req_test")
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	errorBody, _ := body["error"].(map[string]any)
	if body["type"] != "error" || body["request_id"] != "req_test" || errorBody["type"] != "invalid_request_error" {
		t.Fatalf("unexpected error envelope: %#v", body)
	}
}
