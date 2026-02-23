package duckgo

import (
	"aurora/httpclient"
	duckgotypes "aurora/typings/duckgo"
	officialtypes "aurora/typings/official"
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

var (
	Token  *XqdgToken
	JsCode *Scripts
	UA     = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Safari/537.36"
)

type XqdgToken struct {
	Token     string     `json:"token"`
	TokenHash string     `json:"tokenHash"`
	M         sync.Mutex `json:"-"`
	ExpireAt  time.Time  `json:"expire"`
}

func InitXVQD(client httpclient.AuroraHttpClient, proxyUrl string) (*XqdgToken, error) {
	if Token == nil {
		Token = &XqdgToken{
			Token: "",
			M:     sync.Mutex{},
		}
		initChromedp()
	}
	if Token.Token == "" || Token.ExpireAt.Before(time.Now()) {
		jsCode, err := getScrits(client, proxyUrl)
		if err != nil {
			return Token, err
		}
		return getToken(jsCode)
	}
	return Token, nil
}

func getScrits(client httpclient.AuroraHttpClient, proxyUrl string) (string, error) {
	if JsCode == nil {
		JsCode = &Scripts{
			Content: "",
			M:       sync.Mutex{},
		}
	}
	if JsCode.Content == "" || JsCode.ExpireAt.Before(time.Now()) {
		status, err := postStatus(client, proxyUrl)
		if err != nil {
			return "", errors.New("no X-Vqd-Hash-1 token")
		}
		defer status.Body.Close()
		jsCode := getScriptsByHeader(status.Header)
		return jsCode, nil
	} else {
		return JsCode.Content, nil
	}
}

func setScripts(jsCode string) {
	if JsCode == nil {
		JsCode = &Scripts{
			Content: "",
			M:       sync.Mutex{},
		}
	}
	JsCode.M.Lock()
	defer JsCode.M.Unlock()
	JsCode.Content = jsCode
	JsCode.ExpireAt = time.Now().Add(time.Hour * 1)
}

func getScriptsByHeader(header http.Header) string {
	base64EncodedJs := header.Get("X-Vqd-Hash-1")
	decodedJsBytes, _ := base64.StdEncoding.DecodeString(base64EncodedJs)
	jsCode := string(decodedJsBytes)
	setScripts(jsCode)
	return jsCode
}

func getToken(jsCode string) (*XqdgToken, error) {
	token, err := ExecuteObfuscatedJs(jsCode)
	if err != nil {
		log.Fatalf("执行 JavaScript 失败: %v", err)
	}
	setToken(token)
	return Token, nil
}

func setToken(token string) {
	Token.M.Lock()
	defer Token.M.Unlock()
	Token.Token = token
	expiredSecondStr := os.Getenv("EXPIRED_SECOND")
	expiredSecond := 60 // default value
	if expiredSecondStr != "" {
		if v, err := strconv.Atoi(expiredSecondStr); err == nil {
			expiredSecond = v
		}
	}
	Token.ExpireAt = time.Now().Add(time.Duration(expiredSecond) * time.Second)
}

func postStatus(client httpclient.AuroraHttpClient, proxyUrl string) (*http.Response, error) {
	if proxyUrl != "" {
		client.SetProxy(proxyUrl)
	}
	header := createHeader()
	header.Set("accept", "*/*")
	header.Set("x-vqd-accept", "1")
	response, err := client.Request(httpclient.GET, "https://duck.ai/duckchat/v1/status", header, nil, nil)
	if err != nil {
		return nil, err
	}
	return response, nil
}

func POSTconversation(client httpclient.AuroraHttpClient, request duckgotypes.ApiRequest, token string, proxyUrl string) (*http.Response, error) {
	if proxyUrl != "" {
		client.SetProxy(proxyUrl)
	}
	body_json, err := json.Marshal(request)
	if err != nil {
		return &http.Response{}, err
	}
	header := createHeader()
	header.Set("accept", "text/event-stream")
	header.Set("x-vqd-hash-1", token)
	// fmt.Println(string(body_json))
	response, err := client.Request(httpclient.POST, "https://duck.ai/duckchat/v1/chat", header, nil, bytes.NewBuffer(body_json))
	if err != nil {
		return nil, err
	}
	go getScriptsByHeader(response.Header)
	return response, nil
}

func Handle_request_error(c *gin.Context, response *http.Response) bool {
	if response.StatusCode != 200 {
		// Try read response body as JSON
		var error_response map[string]any
		err := json.NewDecoder(response.Body).Decode(&error_response)
		if err != nil {
			// Read response body
			body, _ := io.ReadAll(response.Body)
			c.JSON(response.StatusCode, gin.H{"error": gin.H{
				"message": "Unknown error",
				"type":    "internal_server_error",
				"param":   nil,
				"code":    "500",
				"details": string(body),
			}})
			return true
		}
		c.JSON(response.StatusCode, gin.H{"error": gin.H{
			"message": error_response["detail"],
			"type":    response.Status,
			"param":   nil,
			"code":    "error",
		}})
		return true
	}
	return false
}

func createHeader() httpclient.AuroraHeaders {
	header := make(httpclient.AuroraHeaders)
	header.Set("accept-language", "zh-CN,zh;q=0.9")
	header.Set("content-type", "application/json")
	header.Set("origin", "https://duck.ai")
	header.Set("referer", "https://duck.ai/")
	header.Set("user-agent", UA)
	return header
}

func Handler(c *gin.Context, response *http.Response, oldRequest duckgotypes.ApiRequest, stream bool) string {
	reader := bufio.NewReader(response.Body)
	if stream {
		// Response content type is text/event-stream
		c.Header("Content-Type", "text/event-stream")
	} else {
		// Response content type is application/json
		c.Header("Content-Type", "application/json")
	}

	var previousText strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return ""
		}
		if len(line) < 6 {
			continue
		}
		line = line[6:]
		if !strings.HasPrefix(line, "[DONE]") {
			var originalResponse duckgotypes.ApiResponse
			err = json.Unmarshal([]byte(line), &originalResponse)
			if err != nil {
				continue
			}
			if originalResponse.Action != "success" {
				c.JSON(500, gin.H{"error": "Error"})
				return ""
			}
			responseString := ""
			if originalResponse.Message != "" {
				previousText.WriteString(originalResponse.Message)
				translatedResponse := officialtypes.NewChatCompletionChunkWithModel(originalResponse.Message, originalResponse.Model)
				responseString = "data: " + translatedResponse.String() + "\n\n"
			}

			if responseString == "" {
				continue
			}

			if stream {
				_, err = c.Writer.WriteString(responseString)
				if err != nil {
					return ""
				}
				c.Writer.Flush()
			}
		} else {
			if stream {
				final_line := officialtypes.StopChunkWithModel("stop", oldRequest.Model)
				c.Writer.WriteString("data: " + final_line.String() + "\n\n")
			}
		}
	}
	return previousText.String()
}
