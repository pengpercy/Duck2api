package initialize

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	duckgoConvert "aurora/conversion/requests/duckgo"
	"aurora/internal/duckgo"
	officialtypes "aurora/typings/official"

	"github.com/gin-gonic/gin"
)

func apiError(c *gin.Context, status int, message, code string) {
	c.JSON(status, gin.H{"error": gin.H{"message": message, "type": "invalid_request_error", "code": code}})
}

func attachFiles(req *officialtypes.APIRequest) error {
	if len(req.FileIDs) == 0 {
		return nil
	}
	var context strings.Builder
	for _, id := range req.FileIDs {
		f, ok := getFile(id)
		if !ok {
			return fmt.Errorf("File %s not found", id)
		}
		context.WriteString("\n\n[Attached file: ")
		context.WriteString(f.Filename)
		context.WriteString("]\n")
		context.Write(f.Bytes)
	}
	if len(req.Messages) == 0 {
		req.Messages = []officialtypes.ApiMessage{{Role: "user", Content: context.String()}}
		return nil
	}
	i := len(req.Messages) - 1
	if s, ok := req.Messages[i].Content.(string); ok {
		req.Messages[i].Content = s + context.String()
	} else {
		req.Messages = append(req.Messages, officialtypes.ApiMessage{Role: "user", Content: context.String()})
	}
	return nil
}

func (h *Handler) responses(c *gin.Context) {
	var request officialtypes.ResponseAPIRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apiError(c, 400, "Request body is invalid JSON", err.Error())
		return
	}
	chat := request.ToChatCompletionRequest()
	if len(chat.Messages) == 0 {
		apiError(c, 400, "input is required", "missing_input")
		return
	}
	translated := duckgoConvert.ConvertAPIRequestWithOptions(chat, request.ReasoningEffort, false)
	resp, err := h.duckgoProvider.PostConversation(translated)
	if err != nil {
		apiError(c, 502, "Failed to post conversation: "+err.Error(), "upstream_error")
		return
	}
	defer resp.Body.Close()
	if duckgo.HandleRequestError(c, resp, h.duckgoProvider) {
		return
	}
	if !request.Stream {
		text := duckgo.StreamHandler(c, resp, translated, false)
		c.JSON(200, officialtypes.NewResponseAPIWithModel(text, translated.Model))
		return
	}
	h.responsesStream(c, resp.Body, translated.Model)
}

func (h *Handler) responsesStream(c *gin.Context, body io.Reader, model string) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	created := officialtypes.NewResponseAPIWithModel("", model)
	created.Status = "in_progress"
	created.Output = nil
	writeResponseEvent(c, officialtypes.ResponseStreamEvent{Type: "response.created", Sequence: 1, Response: &created})
	out := officialtypes.NewResponseOutput("")
	out.Status = "in_progress"
	writeResponseEvent(c, officialtypes.ResponseStreamEvent{Type: "response.output_item.added", Sequence: 2, OutputIndex: 0, Item: &out})
	part := officialtypes.ResponseOutputContent{Type: "output_text", Text: "", Annotations: []interface{}{}}
	writeResponseEvent(c, officialtypes.ResponseStreamEvent{Type: "response.content_part.added", Sequence: 3, ItemID: out.ID, OutputIndex: 0, ContentIndex: 0, Part: part})
	reader := bufio.NewScanner(body)
	var full strings.Builder
	seq := 4
	for reader.Scan() {
		line := reader.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if strings.HasPrefix(data, "[") {
			continue
		}
		var d struct {
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(data), &d) != nil || d.Message == "" {
			continue
		}
		full.WriteString(d.Message)
		writeResponseEvent(c, officialtypes.ResponseStreamEvent{Type: "response.output_text.delta", Sequence: seq, ItemID: out.ID, OutputIndex: 0, ContentIndex: 0, Delta: d.Message})
		seq++
	}
	text := full.String()
	done := officialtypes.NewResponseAPIWithModel(text, model)
	writeResponseEvent(c, officialtypes.ResponseStreamEvent{Type: "response.output_text.done", Sequence: seq, ItemID: out.ID, Text: text})
	seq++
	donePart := officialtypes.ResponseOutputContent{Type: "output_text", Text: text, Annotations: []interface{}{}}
	writeResponseEvent(c, officialtypes.ResponseStreamEvent{Type: "response.content_part.done", Sequence: seq, ItemID: out.ID, OutputIndex: 0, ContentIndex: 0, Part: donePart})
	seq++
	writeResponseEvent(c, officialtypes.ResponseStreamEvent{Type: "response.output_item.done", Sequence: seq, OutputIndex: 0, Item: &done.Output[0]})
	seq++
	writeResponseEvent(c, officialtypes.ResponseStreamEvent{Type: "response.completed", Sequence: seq, Response: &done})
}
func writeResponseEvent(c *gin.Context, event officialtypes.ResponseStreamEvent) {
	c.Writer.WriteString("event: " + event.Type + "\n")
	c.Writer.WriteString("data: " + event.String() + "\n\n")
	c.Writer.Flush()
}

func (h *Handler) imageGenerations(c *gin.Context) {
	var req officialtypes.ImageGenerationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, "Request body is invalid JSON", err.Error())
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		apiError(c, 400, "prompt is required", "missing_prompt")
		return
	}
	if req.Model == "" {
		req.Model = "gpt-5.4-nano"
	}
	if req.N <= 0 {
		req.N = 1
	}
	h.doImageRequest(c, req.Prompt, req.Model, req.ReasoningEffort, "", req.N)
}

func (h *Handler) imageEdits(c *gin.Context) {
	contentType := c.GetHeader("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		f, hdr, err := c.Request.FormFile("image")
		if err != nil {
			apiError(c, 400, "image is required", "missing_image")
			return
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, 32<<20+1))
		if err != nil || len(b) > 32<<20 {
			apiError(c, 400, "invalid image", "invalid_image")
			return
		}
		prompt := c.PostForm("prompt")
		if prompt == "" {
			apiError(c, 400, "prompt is required", "missing_prompt")
			return
		}
		model := c.PostForm("model")
		if model == "" {
			model = "gpt-5.4-nano"
		}
		mime := hdr.Header.Get("Content-Type")
		if mime == "" {
			mime = detectMimeType(hdr.Filename)
		}
		dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
		h.doImageRequest(c, prompt, model, c.PostForm("reasoning_effort"), dataURL, 1)
		return
	}
	var req officialtypes.ImageEditRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, "Request body is invalid JSON", err.Error())
		return
	}
	if req.Image == "" || req.Prompt == "" {
		apiError(c, 400, "image and prompt are required", "missing_image_or_prompt")
		return
	}
	if req.Model == "" {
		req.Model = "gpt-5.4-nano"
	}
	image := req.Image
	if !strings.HasPrefix(image, "data:") {
		image = "data:image/png;base64," + image
	}
	h.doImageRequest(c, req.Prompt, req.Model, req.ReasoningEffort, image, req.N)
}

func (h *Handler) doImageRequest(c *gin.Context, prompt, model, effort, image string, n int) {
	var content any = prompt
	if image != "" {
		content = []any{map[string]any{"type": "text", "text": prompt}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": image}}}
	}
	chat := officialtypes.APIRequest{Model: model, Messages: []officialtypes.ApiMessage{{Role: "user", Content: content}}}
	translated := duckgoConvert.ConvertAPIRequestWithOptions(chat, effort, false)
	translated.Metadata.ToolChoice.GenerateImage = true
	translated.CanUseTools = true
	resp, err := h.duckgoProvider.PostConversation(translated)
	if err != nil {
		apiError(c, 502, "Failed to generate image: "+err.Error(), "upstream_error")
		return
	}
	defer resp.Body.Close()
	if duckgo.HandleRequestError(c, resp, h.duckgoProvider) {
		return
	}
	result := duckgo.ReadImageResponse(resp)
	if len(result.Images) == 0 {
		apiError(c, 502, "No images were generated", "no_images")
		return
	}
	data := make([]officialtypes.ImageData, 0, len(result.Images))
	for _, img := range result.Images {
		b64 := img.Result
		if b64 == "" && img.Data != nil {
			b64 = img.Data.B64Image
		}
		if comma := strings.Index(b64, ","); strings.HasPrefix(b64, "data:") && comma >= 0 {
			b64 = b64[comma+1:]
		}
		if b64 != "" {
			data = append(data, officialtypes.ImageData{B64JSON: b64, RevisedPrompt: result.Text})
		}
		if len(data) >= n {
			break
		}
	}
	c.JSON(http.StatusOK, officialtypes.ImageGenerationResponse{Created: time.Now().Unix(), Data: data})
}
