package duckgo

import (
	"aurora/httpclient"
	duckgotypes "aurora/typings/duckgo"
	officialtypes "aurora/typings/official"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/gin-gonic/gin"
)

var (
	Token         *XqdgToken
	UA            = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36 Edg/137.0.0.0"
	browserCtx    context.Context    // 浏览器运行器的根上下文
	browserCancel context.CancelFunc // 用于取消根上下文并关闭浏览器的函数
	browserOnce   sync.Once          // 确保浏览器初始化只发生一次
	browserError  error              // 存储浏览器初始化过程中可能出现的错误
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
	}
	Token.M.Lock()
	defer Token.M.Unlock()
	if Token.Token == "" || Token.ExpireAt.Before(time.Now()) {
		status, err := postStatus(client, proxyUrl)
		if err != nil {
			return Token, err
		}
		defer status.Body.Close()
		xvqdHash1 := status.Header.Get("X-Vqd-Hash-1")
		if xvqdHash1 == "" {
			return Token, errors.New("no X-Vqd-Hash-1 token")
		}
		// 调用函数执行 JS 并获取处理后的、Base64 编码的结果。
		token, err := ExecuteObfuscatedJs(xvqdHash1)
		if err != nil {
			log.Fatalf("执行 JavaScript 失败: %v", err)
		}

		Token.Token = token
		Token.TokenHash = token //status.Header.Get("X-Vqd-Hash-1")
		Token.ExpireAt = time.Now().Add(time.Minute * 3)
	}

	return Token, nil
}

func postStatus(client httpclient.AuroraHttpClient, proxyUrl string) (*http.Response, error) {
	if proxyUrl != "" {
		client.SetProxy(proxyUrl)
	}
	header := createHeader()
	header.Set("accept", "*/*")
	header.Set("x-vqd-accept", "1")
	response, err := client.Request(httpclient.GET, "https://duckduckgo.com/duckchat/v1/status", header, nil, nil)
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
	response, err := client.Request(httpclient.POST, "https://duckduckgo.com/duckchat/v1/chat", header, nil, bytes.NewBuffer(body_json))
	if err != nil {
		return nil, err
	}
	return response, nil
}

func Handle_request_error(c *gin.Context, response *http.Response) bool {
	if response.StatusCode != 200 {
		// Try read response body as JSON
		var error_response map[string]interface{}
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
	header.Set("origin", "https://duckduckgo.com")
	header.Set("referer", "https://duckduckgo.com/")
	header.Set("sec-ch-ua", `"Google Chrome";v="137", "Chromium";v="137", "Not/A)Brand";v="24"`)
	header.Set("sec-ch-ua-mobile", "?0")
	header.Set("sec-ch-ua-platform", `"Windows"`)
	header.Set("sec-fetch-dest", "empty")
	header.Set("sec-fetch-mode", "cors")
	header.Set("sec-fetch-site", "same-origin")
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

// sha256AndBase64 使用 SHA-256 对字符串进行哈希，然后将结果进行 Base64 编码。
// 这用于对 client_hashes 进行后处理。
func sha256AndBase64(input string) string {
	h := sha256.New()
	h.Write([]byte(input))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// findChromeExecutablePath 根据操作系统尝试查找 Chrome/Chromium 的可执行路径。
// 返回找到的路径，如果未找到则返回空字符串。
func findChromeExecutablePath() string {
	var paths []string

	switch runtime.GOOS {
	case "darwin": // macOS
		paths = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/usr/bin/google-chrome", // Homebrew Cask 可能会安装在这里
		}
	case "windows": // Windows
		// Windows 路径可能更复杂，需要考虑 Program Files (x86)
		paths = []string{
			os.Getenv("PROGRAMFILES") + "\\Google\\Chrome\\Application\\chrome.exe",
			os.Getenv("PROGRAMFILES(X86)") + "\\Google\\Chrome\\Application\\chrome.exe",
			os.Getenv("LOCALAPPDATA") + "\\Google\\Chrome\\Application\\chrome.exe",
			os.Getenv("PROGRAMFILES") + "\\Chromium\\Application\\chrome.exe",
			os.Getenv("PROGRAMFILES(X86)") + "\\Chromium\\Application\\chrome.exe",
			os.Getenv("LOCALAPPDATA") + "\\Chromium\\Application\\chrome.exe",
		}
	case "linux": // Linux
		paths = []string{
			"/usr/bin/google-chrome",
			"/usr/bin/chromium-browser",
			"/usr/bin/chromium",
			"/opt/google/chrome/google-chrome", // 某些安装方式
		}
	default:
		log.Printf("不支持的操作系统: %s, 无法自动查找 Chrome 路径。", runtime.GOOS)
		return ""
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			//log.Printf("找到 Chrome/Chromium 可执行文件路径: %s", path)
			return path
		}
	}

	log.Println("未能自动找到 Chrome/Chromium 可执行文件，chromedp 将尝试在 PATH 中查找。")
	return "" // 返回空字符串，让 chromedp 尝试在 PATH 中查找
}

// InitBrowserInstance 初始化 Chromedp 浏览器单例。
// 它使用 sync.Once 确保线程安全、一次性初始化。
// 浏览器将保持打开状态，供后续的 ExecuteObfuscatedJs 调用复用。
func InitBrowserInstance() error {
	browserOnce.Do(func() {
		//log.Println("正在初始化 Chromedp 浏览器实例...")
		// 动态获取 Chrome 可执行文件路径
		chromeExecPath := findChromeExecutablePath()

		// 构建 chromedp 的 ExecAllocatorOptions
		allocatorOptions := append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.NoSandbox,                 // 建议在 Docker/Linux 环境中使用
			chromedp.Flag("disable-gpu", true), // 服务器上没有 GPU 时通常需要
			chromedp.Flag("ignore-certificate-errors", true),
			chromedp.Flag("disable-dev-shm-usage", true), // 对 Docker/Linux 很重要，防止崩溃
			chromedp.WindowSize(1200, 800),               // 设置一个统一的窗口大小                  // 以无头模式运行（没有可见的用户界面）
			chromedp.UserAgent(UA),
			chromedp.Headless,
		)

		// 如果找到了可执行路径，则添加到选项中
		if chromeExecPath != "" {
			allocatorOptions = append(allocatorOptions, chromedp.ExecPath(chromeExecPath))
		}

		// 为浏览器运行器创建一个根上下文。
		// 使用 chromedp.NewExecAllocator 可以更精细地控制 Chrome 的启动参数。
		allocatorContext, _ := chromedp.NewExecAllocator(
			context.Background(),
			allocatorOptions..., // 使用动态构建的选项
		)
		// browserCtx 是所有后续任务的父上下文。
		// browserCancel 将在程序退出时被调用以关闭浏览器。
		browserCtx, browserCancel = chromedp.NewContext(allocatorContext)

		// 运行一个简单的任务以确保浏览器已启动并响应。
		// 这使用了一个带超时的新上下文进行初始运行。
		ctx, _ := context.WithTimeout(browserCtx, 60*time.Second) // 30秒启动超时
		//defer cancel()

		if err := chromedp.Run(ctx); err != nil {
			browserError = fmt.Errorf("启动 Chromedp 浏览器失败: %w", err)
			log.Printf("浏览器初始化错误: %v", browserError)
			browserCancel() // 如果初始化失败，清理上下文
			return
		}
		//log.Println("Chromedp 浏览器实例初始化并准备就绪。")
	})
	return browserError
}

// ShutdownBrowserInstance 关闭 Chromedp 浏览器实例。
// 在应用程序退出时调用此函数以释放资源。
func ShutdownBrowserInstance() {
	if browserCancel != nil {
		//log.Println("正在关闭 Chromedp 浏览器实例...")
		browserCancel() // 这会取消根上下文，从而关闭浏览器
		log.Println("Chromedp 浏览器实例已停止。")
	}
}

// ExecuteObfuscatedJs 接收一个 Base64 编码的 JS 字符串，在浏览器中执行它，
// 处理其 client_hashes，并返回最终 Base64 编码的 JSON 结果。
func ExecuteObfuscatedJs(base64EncodedJs string) (string, error) {
	// 确保浏览器已初始化。它只会运行一次。
	if err := InitBrowserInstance(); err != nil {
		return "", fmt.Errorf("浏览器初始化失败: %w", err)
	}

	// 解码 Base64 编码的 JavaScript 字符串
	decodedJsBytes, err := base64.StdEncoding.DecodeString(base64EncodedJs)
	if err != nil {
		return "", fmt.Errorf("解码 Base64 JS 字符串失败: %w", err)
	}
	decodedJs := string(decodedJsBytes)

	// 为每次执行创建一个新的上下文（表示一个新的标签页或页面）。
	// 这确保了不同调用 ExecuteObfuscatedJs 之间的隔离。
	// 为每个任务设置超时，以防止挂起。
	taskCtx, cancelTask := context.WithTimeout(browserCtx, 60*time.Second) // 任务执行超时60秒
	defer cancelTask()                                                     // 确保函数退出时任务上下文被取消

	var rawJsResult map[string]interface{} // 使用 map[string]interface{} 来反序列化动态 JSON 结果

	// 定义要在浏览器中运行的任务
	tasks := chromedp.Tasks{
		// 导航到一个空白页，以确保脚本执行有一个干净的环境
		chromedp.Navigate("about:blank"),
		// 在浏览器上下文中执行解码后的 JavaScript 代码
		// JS 执行的结果将被反序列化到 rawJsResult 中
		chromedp.Evaluate(decodedJs, &rawJsResult),
	}

	// 在浏览器中运行任务
	err = chromedp.Run(taskCtx, tasks)
	if err != nil {
		return "", fmt.Errorf("在浏览器中执行 JS 失败: %w", err)
	}

	// 检查 JS 代码自身报告的任何错误
	if rawJsResult == nil {
		return "", fmt.Errorf("JS 执行返回空结果")
	}
	if errMsg, ok := rawJsResult["error"].(string); ok && errMsg != "" {
		return "", fmt.Errorf("JS 执行报告错误: %s (详情: %v)", errMsg, rawJsResult["stack"])
	}

	// --- 后处理：对 client_hashes 进行 SHA-256 和 Base64 编码 ---
	// 安全地访问和修改 client_hashes
	if clientHashesInterface, ok := rawJsResult["client_hashes"].([]interface{}); ok && len(clientHashesInterface) >= 2 {
		// 处理 client_hashes[0] (combinedUserAgentHash)
		if combinedUserAgentHash, ok := clientHashesInterface[0].(string); ok {
			rawJsResult["client_hashes"].([]interface{})[0] = sha256AndBase64(combinedUserAgentHash)
			//log.Printf("已处理 client_hashes[0]: %s -> %s", combinedUserAgentHash, rawJsResult["client_hashes"].([]interface{})[0])
		} else {
			log.Printf("警告: client_hashes[0] 不是字符串或格式不正确，跳过处理。")
		}

		// 处理 client_hashes[1] (htmlParsingBasedHash)
		if htmlParsingBasedHash, ok := clientHashesInterface[1].(string); ok {
			// 如前所述，htmlParsingBasedHash 已经是一个 Base64 编码的 SHA-256 哈希。
			// 再次应用 sha256AndBase64 意味着对“当前 Base64 字符串”进行哈希。
			// 如果这不是期望的行为（例如，如果你想要原始哈希），请在此处调整。
			// 按照严格的提示“分别执行 hex_to_binary 然后 base64 编码”并将其解释为 SHA256 然后 Base64。
			rawJsResult["client_hashes"].([]interface{})[1] = sha256AndBase64(htmlParsingBasedHash)
			//log.Printf("已处理 client_hashes[1]: %s -> %s", htmlParsingBasedHash, rawJsResult["client_hashes"].([]interface{})[1])
		} else {
			log.Printf("警告: client_hashes[1] 不是字符串或格式不正确，跳过处理。")
		}
	} else {
		log.Printf("警告: 未找到 client_hashes 数组或长度不足，跳过后处理。")
	}
	// --- 后处理结束 ---

	// 将修改后的结果 (map[string]interface{}) 重新序列化为 JSON 字节
	finalJsonBytes, err := json.Marshal(rawJsResult)
	if err != nil {
		return "", fmt.Errorf("序列化最终 JSON 结果失败: %w", err)
	}

	// 将整个最终 JSON 字符串进行 Base64 编码
	return base64.StdEncoding.EncodeToString(finalJsonBytes), nil
}
