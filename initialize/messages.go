package initialize

import (
	duckgoConvert "aurora/conversion/requests/duckgo"
	"aurora/internal/duckgo"
	anthropic "aurora/typings/anthropic"
	officialtypes "aurora/typings/official"
	"aurora/util"
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// anthropicStreamResult holds the output-side telemetry for an Anthropic request.
type anthropicStreamResult struct {
	text         string
	outputTokens int
	stopReason   string
	ttftMs       int64
	totalMs      int64
}

func (h *Handler) messagesHandler(c *gin.Context) {
	requestID := "req_" + util.RandomHexadecimalString()
	c.Header("request-id", requestID)

	var req anthropic.MessagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "Request body is not valid JSON: "+err.Error(), requestID)
		return
	}
	if err := anthropic.ValidateMessagesRequest(req); err != nil {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", err.Error(), requestID)
		return
	}

	// thinking (extended thinking) → reasoning_effort.
	effort := anthropic.ThinkingToEffort(req.Thinking)
	if effort == "" {
		effort = os.Getenv("CLAUDE_CODE_EFFORT_LEVEL")
	}

	// Convert Anthropic messages → internal OpenAI-shaped request.
	apiReq := anthropic.ToOpenAPIRequest(req)
	apiReq.ReasoningEffort = effort

	// Input token counting + prompt-cache simulation.
	inputTokens := util.CountMessagesTokens(apiReq.Messages)
	cacheCreation, cacheRead := util.RecordAnthropicCache(req)
	promptHash := util.HashPrompt(messagesText(apiReq.Messages))
	if cacheCreation == 0 && cacheRead == 0 {
		// Fallback to prompt hash simulation if request has no explicit cache_control blocks
		cacheCreation, cacheRead = util.RecordCache(promptHash, inputTokens)
	}
	setCacheHeaders(c, promptHash, cacheCreation, cacheRead)

	// Anthropic defines max_tokens=0 as a prompt-cache-only request.
	if *req.MaxTokens == 0 {
		result := anthropicStreamResult{stopReason: "max_tokens"}
		if req.Stream {
			setAnthropicStreamHeaders(c)
			writeAnthropicStreamStart(c, req.Model, "msg_"+util.RandomHexadecimalString(), inputTokens, cacheCreation, cacheRead)
			writeAnthropicStreamEnd(c, "max_tokens", 0)
			return
		}
		c.JSON(http.StatusOK, buildAnthropicResponse(req.Model, result, inputTokens, cacheCreation, cacheRead))
		return
	}

	translated := duckgoConvert.ConvertAPIRequestWithOptions(apiReq, effort, false)
	response, err := h.duckgoProvider.PostConversation(translated)
	if err != nil {
		writeAnthropicError(c, http.StatusInternalServerError, "api_error", err.Error(), requestID)
		return
	}

	if handleAnthropicUpstreamError(c, response, h.duckgoProvider, requestID) {
		return
	}

	start := time.Now()
	result, streamErr := handleAnthropicStream(c, response.Body, req.Model, req.Stream, start, inputTokens, cacheCreation, cacheRead)
	if streamErr != nil {
		if req.Stream {
			writeEvent(c, "error", anthropic.ErrorResponse{Type: "error", Error: anthropic.AnthropicError{Type: "api_error", Message: streamErr.Error()}, RequestID: requestID})
		} else {
			writeAnthropicError(c, http.StatusInternalServerError, "api_error", streamErr.Error(), requestID)
		}
		return
	}

	// Timing headers (only delivered for non-stream; for stream the same values
	// live in the message_delta event).
	c.Header("X-TTFT-Ms", fmt.Sprintf("%d", result.ttftMs))
	c.Header("X-Total-Time-Ms", fmt.Sprintf("%d", result.totalMs))

	if c.Writer.Status() != 200 {
		return
	}

	if !req.Stream {
		c.JSON(200, buildAnthropicResponse(req.Model, result, inputTokens, cacheCreation, cacheRead))
	}
}

func writeAnthropicError(c *gin.Context, status int, errorType, message, requestID string) {
	c.JSON(status, anthropic.ErrorResponse{
		Type:      "error",
		Error:     anthropic.AnthropicError{Type: errorType, Message: message},
		RequestID: requestID,
	})
}

func handleAnthropicUpstreamError(c *gin.Context, response *http.Response, provider *duckgo.Provider, requestID string) bool {
	if response.StatusCode == http.StatusOK {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTeapot {
		provider.InvalidateCache()
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		writeAnthropicError(c, http.StatusInternalServerError, "api_error", "Failed to read upstream error response", requestID)
		return true
	}
	message := "Upstream API request failed"
	var upstream struct {
		Detail any `json:"detail"`
	}
	if json.Unmarshal(body, &upstream) == nil && upstream.Detail != nil {
		message = fmt.Sprint(upstream.Detail)
	} else if text := strings.TrimSpace(string(body)); text != "" {
		message += ": " + text
	}
	status, errorType := anthropicErrorForStatus(response.StatusCode)
	writeAnthropicError(c, status, errorType, message, requestID)
	return true
}

func anthropicErrorForStatus(status int) (int, string) {
	switch status {
	case http.StatusBadRequest:
		return status, "invalid_request_error"
	case http.StatusUnauthorized:
		return status, "authentication_error"
	case http.StatusForbidden:
		return status, "permission_error"
	case http.StatusNotFound:
		return status, "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return status, "request_too_large"
	case http.StatusTooManyRequests:
		return status, "rate_limit_error"
	case 529:
		return status, "overloaded_error"
	default:
		return http.StatusBadGateway, "api_error"
	}
}

func messagesText(messages []officialtypes.ApiMessage) string {
	var text strings.Builder
	for _, message := range messages {
		text.WriteString(util.MessageText(message.Content))
		text.WriteByte('\n')
	}
	return text.String()
}

func setCacheHeaders(c *gin.Context, promptHash string, creation, read int) {
	c.Header("X-Cache-Creation-Tokens", fmt.Sprintf("%d", creation))
	c.Header("X-Cache-Read-Tokens", fmt.Sprintf("%d", read))
	status, age := util.CacheHeaders(promptHash)
	c.Header("X-Cache", status)
	if status == "HIT" {
		c.Header("Age", fmt.Sprintf("%d", age))
	}
}

// handleAnthropicStream reads DuckDuckGo's text SSE and emits Anthropic SSE events.
// For non-stream it accumulates the text and returns the result.
func handleAnthropicStream(c *gin.Context, body io.ReadCloser, model string, stream bool, start time.Time, inputTokens, cacheCreation, cacheRead int) (anthropicStreamResult, error) {
	defer body.Close()

	reader := bufio.NewReader(body)
	if stream {
		setAnthropicStreamHeaders(c)
	}

	msgID := "msg_" + util.RandomHexadecimalString()

	var sb strings.Builder
	var firstTokenSet bool
	var ttftMs int64

	if stream {
		writeAnthropicStreamStart(c, model, msgID, inputTokens, cacheCreation, cacheRead)
		writeEvent(c, "content_block_start", anthropic.ContentBlockStartEvent{
			Type:         "content_block_start",
			Index:        0,
			ContentBlock: anthropic.ContentBlock{Type: "text", Text: ""},
		})
	}

	for {
		line, readErr := reader.ReadString('\n')
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			if data == "[DONE]" {
				break
			}
			var delta struct {
				Message string `json:"message"`
			}
			if json.Unmarshal([]byte(data), &delta) == nil && delta.Message != "" {
				sb.WriteString(delta.Message)
				if !firstTokenSet {
					firstTokenSet = true
					ttftMs = time.Since(start).Milliseconds()
				}
				if stream {
					writeEvent(c, "content_block_delta", anthropic.ContentBlockDeltaEvent{
						Type: "content_block_delta", Index: 0,
						Delta: anthropic.ContentDelta{Type: "text_delta", Text: delta.Message},
					})
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return anthropicStreamResult{}, fmt.Errorf("upstream stream interrupted: %w", readErr)
		}
	}

	fullText := sb.String()
	outputTokens := util.CountToken(fullText)
	totalMs := time.Since(start).Milliseconds()

	if stream {
		writeEvent(c, "content_block_stop", anthropic.ContentBlockStopEvent{Type: "content_block_stop", Index: 0})
		writeAnthropicStreamEnd(c, "end_turn", outputTokens)
	}

	return anthropicStreamResult{
		text:         fullText,
		outputTokens: outputTokens,
		stopReason:   "end_turn",
		ttftMs:       ttftMs,
		totalMs:      totalMs,
	}, nil
}

func setAnthropicStreamHeaders(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
}

func writeAnthropicStreamStart(c *gin.Context, model, messageID string, inputTokens, cacheCreation, cacheRead int) {
	writeEvent(c, "message_start", anthropic.MessageStartEvent{
		Type: "message_start",
		Message: anthropic.AnthropicStartMessage{
			ID: messageID, Type: "message", Role: "assistant", Model: model,
			Content: []anthropic.ContentBlock{},
			Usage: anthropic.AnthropicUsage{
				InputTokens: inputTokens, CacheCreationInputTokens: cacheCreation, CacheReadInputTokens: cacheRead,
			},
		},
	})
}

func writeAnthropicStreamEnd(c *gin.Context, stopReason string, outputTokens int) {
	writeEvent(c, "message_delta", anthropic.MessageDeltaEvent{
		Type:  "message_delta",
		Delta: anthropic.MessageDelta{StopReason: stopReason},
		Usage: anthropic.MessageDeltaUsage{OutputTokens: outputTokens},
	})
	writeEvent(c, "message_stop", anthropic.MessageStopEvent{Type: "message_stop"})
	c.Writer.Flush()
}

// writeEvent serializes an Anthropic SSE event and writes it to the client.
func writeEvent(c *gin.Context, eventType string, payload interface{}) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	c.Writer.WriteString("event: " + eventType + "\n")
	c.Writer.WriteString("data: " + string(b) + "\n\n")
	c.Writer.Flush()
}

// buildAnthropicResponse builds the non-stream MessagesResponse.
func buildAnthropicResponse(model string, r anthropicStreamResult, inputTokens, cacheCreation, cacheRead int) anthropic.MessagesResponse {
	usage := anthropic.AnthropicUsage{
		InputTokens:              inputTokens,
		OutputTokens:             r.outputTokens,
		CacheCreationInputTokens: cacheCreation,
		CacheReadInputTokens:     cacheRead,
	}
	content := []anthropic.ContentBlock{}
	if r.text != "" {
		content = append(content, anthropic.ContentBlock{Type: "text", Text: r.text})
	}
	stopReason := r.stopReason
	if stopReason == "" {
		stopReason = "end_turn"
	}
	return anthropic.MessagesResponse{
		ID:         "msg_" + util.RandomHexadecimalString(),
		Type:       "message",
		Role:       "assistant",
		Model:      model,
		Content:    content,
		StopReason: stopReason,
		Usage:      usage,
	}
}
