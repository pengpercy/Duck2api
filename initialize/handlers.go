package initialize

import (
	duckgoConvert "aurora/conversion/requests/duckgo"
	"aurora/httpclient"
	"aurora/httpclient/bogdanfinn"
	"aurora/internal/duckgo"
	"aurora/internal/proxys"
	"aurora/logger"
	officialtypes "aurora/typings/official"
	"encoding/json"
	"fmt"

	"github.com/gin-gonic/gin"
)

// Handler 承载所有 HTTP 接口；与 duck.ai 的交互（挑战、会话、重试）都在 duckgo.Provider 中。
type Handler struct {
	duckgoProvider *duckgo.Provider
}

func NewHandler(proxy *proxys.IProxy) (*Handler, error) {
	proxyUrl := proxy.GetProxyIP()
	// 传入客户端工厂而非实例：Provider 重建会话时需要全新的客户端
	newClient := func() httpclient.AuroraHttpClient { return bogdanfinn.NewStdClient() }
	provider, err := duckgo.NewProvider(newClient, proxyUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to create duckgo provider: %w", err)
	}

	logger.Debugf("Provider initialized successfully.")
	duckgoConvert.FileStore = func(fileID string) (string, string, []byte, bool) {
		file, ok := getFile(fileID)
		if !ok {
			return "", "", nil, false
		}
		return file.Filename, file.MimeType, file.Bytes, true
	}
	return &Handler{
		duckgoProvider: provider,
	}, nil
}

// optionsHandler 处理浏览器的 CORS 预检请求。
func optionsHandler(c *gin.Context) {
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Methods", "POST, OPTIONS")
	c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
	c.JSON(200, gin.H{"status": "ok"})
}

// duckduckgo 处理 OpenAI 兼容的 /v1/chat/completions。
func (h *Handler) duckduckgo(c *gin.Context) {
	var original_request officialtypes.APIRequest
	if err := c.BindJSON(&original_request); err != nil {
		c.JSON(400, gin.H{"error": gin.H{
			"message": "Request body is invalid JSON",
			"type":    "invalid_request_error",
		}})
		return
	}
	if err := attachFiles(&original_request); err != nil {
		apiError(c, 400, err.Error(), "file_not_found")
		return
	}
	bodyJSON, err := json.Marshal(original_request)
	if err == nil && bodyJSON != nil {
		logger.Debugf(string(bodyJSON))
	}
	webSearch := original_request.WebSearch != nil && *original_request.WebSearch
	translatedRequest := duckgoConvert.ConvertAPIRequestWithOptions(original_request, original_request.ReasoningEffort, webSearch)

	response, err := h.duckgoProvider.PostConversation(translatedRequest)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to post conversation to upstream: " + err.Error()})
		return
	}
	defer response.Body.Close()

	if duckgo.HandleRequestError(c, response, h.duckgoProvider) {
		return
	}
	stream := original_request.Stream
	fullMessage := duckgo.StreamHandler(c, response, translatedRequest, stream)
	if !stream {
		c.JSON(200, officialtypes.NewChatCompletionWithModel(fullMessage, translatedRequest.Model))
	}
}

// engines 返回支持的模型列表。
func (h *Handler) engines(c *gin.Context) {
	supportedModels := []string{
		"gpt-5.4-mini",
		"gpt-5.4-nano",
		"tinfoil/gpt-oss-120b",
		"claude-haiku-4-5",
		"mistral-small",
	}

	data := make([]gin.H, len(supportedModels))
	for i, modelID := range supportedModels {
		data[i] = gin.H{
			"id":       modelID,
			"object":   "model",
			"created":  1685474247, // 使用一个固定的时间戳
			"owned_by": "duckai",
		}
	}

	c.JSON(200, gin.H{
		"object": "list",
		"data":   data,
	})
}
