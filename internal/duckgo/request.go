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
	UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Safari/537.36"
)

func createHeader() httpclient.AuroraHeaders {
	header := make(httpclient.AuroraHeaders)
	header.Set("accept-language", "zh-CN,zh;q=0.9")
	header.Set("content-type", "application/json")
	header.Set("origin", "https://duck.ai")
	header.Set("referer", "https://duck.ai/")
	header.Set("user-agent", UA)
	return header
}

func HandleRequestError(c *gin.Context, response *http.Response, provider *Provider) bool {
	if response.StatusCode == http.StatusOK {
		return false
	}

	if response.StatusCode == http.StatusTeapot {
		provider.InvalidateCache()
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		c.JSON(response.StatusCode, gin.H{"error": gin.H{
			"message": "Failed to read error response body",
			"type":    "internal_server_error",
		}})
		return true
	}

	var errorResponse map[string]any
	if json.Unmarshal(body, &errorResponse) == nil && errorResponse["detail"] != nil {
		c.JSON(response.StatusCode, gin.H{"error": gin.H{
			"message": errorResponse["detail"],
			"type":    response.Status,
			"code":    "upstream_error",
		}})
	} else {
		c.JSON(response.StatusCode, gin.H{"error": gin.H{
			"message": "Unknown error from upstream API",
			"type":    "internal_server_error",
			"details": string(body),
		}})
	}
	return true
}

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
				break
			}
			break
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

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

		if stream {
			chunk := officialtypes.NewChatCompletionChunkWithModel(apiResponse.Message, apiResponse.Model)
			responseString := "data: " + chunk.String() + "\n\n"
			if _, err := c.Writer.WriteString(responseString); err != nil {
				break
			}
			c.Writer.Flush()
		}
	}

	return fullMessageBuilder.String()
}
