package duckgo

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

var (
	globalAllocatorCtx       context.Context
	globalAllocatorCtxCancel context.CancelFunc
	allocatorInitOnce        sync.Once
)

// sha256AndBase64 使用 SHA-256 对字符串进行哈希，然后将结果进行 Base64 编码。
func sha256AndBase64(input string) string {
	h := sha256.New()
	h.Write([]byte(input))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func ExecuteObfuscatedJs(base64EncodedJs string) (string, error) {
	decodedJsBytes, err := base64.StdEncoding.DecodeString(base64EncodedJs)
	if err != nil {
		return "", fmt.Errorf("解码 Base64 JS 字符串失败: %w", err)
	}
	rawJsResult, err := ExecuteJS(string(decodedJsBytes))
	if err != nil {
		return "", err
	}
	if rawJsResult == nil {
		return "", fmt.Errorf("JS 执行返回空结果")
	}
	if errMsg, ok := rawJsResult["error"].(string); ok && errMsg != "" {
		return "", fmt.Errorf("JS 执行报告错误: %s (详情: %v)", errMsg, rawJsResult["stack"])
	}

	if hashes, ok := rawJsResult["client_hashes"].([]any); ok && len(hashes) >= 3 {
		for i := range len(hashes) {
			if s, ok := hashes[i].(string); ok {
				hashes[i] = sha256AndBase64(s)
			} else {
				log.Printf("警告: client_hashes[%d] 不是字符串或格式不正确，跳过处理。", i)
			}
		}
		if meta, ok := rawJsResult["meta"].(map[string]any); ok {
			meta["origin"] = "https://duckduckgo.com"
		}
	} else {
		log.Printf("警告: 未找到 client_hashes 数组或长度不足，跳过后处理。")
	}

	finalJsonBytes, err := json.Marshal(rawJsResult)
	if err != nil {
		return "", fmt.Errorf("序列化最终 JSON 结果失败: %w", err)
	}
	return base64.StdEncoding.EncodeToString(finalJsonBytes), nil
}

// initChromedp 初始化全局的chromedp Allocator
// 它会连接到一个已存在的Chrome实例。
func initChromedp() {
	allocatorInitOnce.Do(func() {
		//log.Println("Initializing chromedp remote allocator...")
		// 为Allocator创建一个父上下文，当Go程序退出时，可以通过它来取消Allocator
		globalAllocatorCtx, globalAllocatorCtxCancel = context.WithCancel(context.Background())

		// 定义远程Chrome实例的WebSocket URL, 需Chrome已在127.0.0.1:9222端口启动远程调试
		wsURL := os.Getenv("DEVTOOLS_URL")
		if wsURL == "" {
			wsURL = "ws://127.0.0.1:9222"
		}
		globalAllocatorCtx, globalAllocatorCtxCancel = chromedp.NewRemoteAllocator(globalAllocatorCtx, wsURL)

		//log.Printf("Chromedp remote allocator connected to %s", wsURL)
		setupGracefulShutdown()
	})
}

// ExecuteJS 执行给定的JavaScript代码，并返回执行结果。
// 每次调用都会在一个新的干净的页面（about:blank）上执行。
func ExecuteJS(jsCode string) (map[string]any, error) {
	// jsCode = fmt.Sprintf("function wrapper() { return %s; }; wrapper();", jsCode)
	// 确保全局Allocator已经初始化
	if globalAllocatorCtx == nil {
		return nil, errors.New("chromedp allocator not initialized. Call initChromedp() first")
	}

	// 从全局Allocator创建一个新的chromedp上下文。
	// 这将打开一个新标签页或获取一个目标来执行任务。
	tabCtx, tabCancel := chromedp.NewContext(globalAllocatorCtx)
	defer tabCancel() // 确保在函数返回时关闭此标签页

	// 为JS执行设置一个超时，以防止长时间运行的JS阻塞。
	execCtx, execCancel := context.WithTimeout(tabCtx, 30*time.Second)
	defer execCancel() // 确保在任务结束时取消此上下文

	var jsResult map[string]any
	err := chromedp.Run(execCtx,
		chromedp.Navigate("https://duckduckgo.com/?q=DuckDuckGo&ia=chat"),
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
		chromedp.Evaluate(jsCode, &jsResult, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
			return p.WithAwaitPromise(true)
		}),
	)

	if err != nil {
		// 如果JS执行出错，错误信息通常会包含在err中
		return nil, err
	}

	return jsResult, nil
}

// setupGracefulShutdown 设置优雅关闭More actions
func setupGracefulShutdown() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		log.Println("收到退出信号，正在优雅关闭...")

		// 关闭浏览器实例
		if globalAllocatorCtxCancel != nil {
			globalAllocatorCtxCancel()
		}
		os.Exit(0)
	}()
}
