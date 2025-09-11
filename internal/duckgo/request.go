package duckgo

import (
	"aurora/httpclient"
	duckgotypes "aurora/typings/duckgo"
	officialtypes "aurora/typings/official"
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

var (
	// User-Agent 保持为全局常量是可接受的
	UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Safari/537.36"
)

// createHeader 创建通用的 HTTP 请求头。
func createHeader() httpclient.AuroraHeaders {
	header := make(httpclient.AuroraHeaders)
	header.Set("accept-language", "zh-CN,zh;q=0.9")
	header.Set("content-type", "application/json")
	header.Set("origin", "https://duckduckgo.com")
	header.Set("referer", "https://duckduckgo.com/")
	header.Set("user-agent", UA)
	return header
}

// HandleRequestError 负责处理 DuckDuckGo API 返回的非 200 状态码。
// 它会尝试解析错误详情并以统一的格式返回给客户端。
func HandleRequestError(c *gin.Context, response *http.Response, provider *Provider) bool {
	if response.StatusCode == http.StatusOK {
		return false
	}

	if response.StatusCode == http.StatusTeapot {
		provider.InvalidateCache()
	}
	// 先将响应体完整读入内存，避免重复读取 stream 导致的问题
	body, err := io.ReadAll(response.Body)
	if err != nil {
		c.JSON(response.StatusCode, gin.H{"error": gin.H{
			"message": "Failed to read error response body",
			"type":    "internal_server_error",
		}})
		return true
	}

	// 尝试将响应体解析为 JSON
	var errorResponse map[string]any
	if json.Unmarshal(body, &errorResponse) == nil && errorResponse["detail"] != nil {
		c.JSON(response.StatusCode, gin.H{"error": gin.H{
			"message": errorResponse["detail"],
			"type":    response.Status,
			"code":    "upstream_error",
		}})
	} else {
		// 如果无法解析为 JSON，则返回原始响应体内容
		c.JSON(response.StatusCode, gin.H{"error": gin.H{
			"message": "Unknown error from upstream API",
			"type":    "internal_server_error",
			"details": string(body),
		}})
	}
	return true
}

// StreamHandler 处理来自 DuckDuckGo 的 SSE (Server-Sent Events) 流。
// 它将 DuckDuckGo 的事件流转换为与 OpenAI 兼容的格式。
func StreamHandler(c *gin.Context, response *http.Response, originalRequest duckgotypes.ApiRequest, stream bool) string {
	contentType := "text/event-stream; charset=utf-8"
	if !stream {
		contentType = "application/json; charset=utf-8"
	}
	c.Header("Content-Type", contentType)
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	reader := bufio.NewReader(response.Body)
	var fullMessageBuilder strings.Builder

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break // 正常结束
			}
			// 记录读取错误，但可能不需要中断整个流程
			// log.Printf("Error reading from stream: %v", err)
			break
		}

		// SSE 数据以 "data: " 开头
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		// DuckDuckGo 流以 "[DONE]" 标记结束
		if stream && strings.HasPrefix(data, "[DONE]") {
			finalChunk := officialtypes.StopChunkWithModel("stop", originalRequest.Model)
			c.Writer.WriteString("data: " + finalChunk.String() + "\n\n")
			c.Writer.Flush()
			break
		}

		var apiResponse duckgotypes.ApiResponse
		if err := json.Unmarshal([]byte(data), &apiResponse); err != nil {
			continue
		}

		if apiResponse.Message == "" {
			continue
		}

		fullMessageBuilder.WriteString(apiResponse.Message)

		chunk := officialtypes.NewChatCompletionChunkWithModel(apiResponse.Message, apiResponse.Model)
		responseString := "data: " + chunk.String() + "\n\n"
		if stream {
			if _, err := c.Writer.WriteString(responseString); err != nil {
				// 客户端可能已经断开连接
				break
			}
			c.Writer.Flush()
		}
	}

	return fullMessageBuilder.String()
}
