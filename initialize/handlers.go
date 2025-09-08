package initialize

import (
	duckgoConvert "aurora/conversion/requests/duckgo"
	"aurora/httpclient/bogdanfinn"
	"aurora/internal/duckgo"
	"aurora/internal/proxys"
	officialtypes "aurora/typings/official"
	"fmt"

	"github.com/gin-gonic/gin"
)

// Handler 结构体现在直接依赖于 duckgo.Provider。
// Provider 封装了所有与 DuckDuckGo API 交互的复杂逻辑，
// 包括 HTTP 客户端、代理、以及 Token 的获取和缓存。
type Handler struct {
	duckgoProvider *duckgo.Provider
}

// NewHandler 是 Handler 的构造函数。
// 它负责初始化所有必要的依赖，包括 HTTP 客户端和核心的 duckgo.Provider。
// 由于 Provider 的初始化（特别是 ChromeDP）可能会失败，因此该函数返回一个 error。
func NewHandler(proxy *proxys.IProxy) (*Handler, error) {
	// 1. 获取代理地址
	proxyUrl := proxy.GetProxyIP()

	// 2. 创建一个长生命周期的 HTTP 客户端
	client := bogdanfinn.NewStdClient()

	// 3. 初始化 duckgo.Provider
	// Provider 将管理客户端、代理和 Token 的所有状态
	provider, err := duckgo.NewProvider(client, proxyUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to create duckgo provider: %w", err)
	}

	//log.Println("DuckDuckGo provider initialized successfully.")
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

// duckduckgo 是处理聊天请求的核心处理器。
// 它的职责被大大简化：解析请求 -> 调用 Provider -> 格式化响应。
func (h *Handler) duckduckgo(c *gin.Context) {
	var original_request officialtypes.APIRequest
	if err := c.BindJSON(&original_request); err != nil {
		c.JSON(400, gin.H{"error": gin.H{
			"message": "Request body is invalid JSON",
			"type":    "invalid_request_error",
		}})
		return
	}

	// 将 OpenAI 格式的请求转换为 DuckDuckGo 格式
	translated_request := duckgoConvert.ConvertAPIRequest(original_request)

	// 调用 Provider 的方法来处理会话。
	// Token 获取、缓存、刷新等所有复杂逻辑都在 Provider 内部自动完成。
	response, err := h.duckgoProvider.PostConversation(translated_request)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to post conversation to upstream: " + err.Error()})
		return
	}
	defer response.Body.Close()

	// 使用重构后的错误处理函数
	if duckgo.HandleRequestError(c, response) {
		return
	}

	// 根据请求决定是流式响应还是聚合响应
	if !original_request.Stream {
		// 非流式：一次性读取所有消息片段并聚合成完整响应
		fullMessage := duckgo.StreamHandler(c, response, translated_request)
		c.JSON(200, officialtypes.NewChatCompletionWithModel(fullMessage, translated_request.Model))
	} else {
		// 流式：直接将事件流转发给客户端
		duckgo.StreamHandler(c, response, translated_request)
	}
}

// engines 返回支持的模型列表。
func (h *Handler) engines(c *gin.Context) {
	// 此处为了简洁，直接硬编码模型列表。
	// 在实际应用中，可以考虑从配置或 Provider 中获取。
	supportedModels := []string{
		"gpt-4o-mini",
		"gpt-5-mini",
		"claude-3.5-haiku",
	}

	data := make([]gin.H, len(supportedModels))
	for i, modelID := range supportedModels {
		data[i] = gin.H{
			"id":       modelID,
			"object":   "model",
			"created":  1685474247, // 使用一个固定的时间戳
			"owned_by": "duckduckgo",
		}
	}

	c.JSON(200, gin.H{
		"object": "list",
		"data":   data,
	})
}
