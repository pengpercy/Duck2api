package initialize

import (
	"io"
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
	result := handleAnthropicStream(context, body, "claude-haiku-4-5", true, time.Now(), 3, 2, 1, "low")
	if result.text != "hello" {
		t.Fatalf("text=%q", result.text)
	}
	output := recorder.Body.String()
	for _, event := range []string{"event: message_start", "event: content_block_start", "event: content_block_delta", "event: content_block_stop", "event: message_delta", "event: message_stop"} {
		if !strings.Contains(output, event) {
			t.Fatalf("missing %q from %s", event, output)
		}
	}
}
