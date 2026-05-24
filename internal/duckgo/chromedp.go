package duckgo

import (
	"aurora/logger"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

var (
	allocatorInitOnce  sync.Once
	globalAllocatorCtx context.Context
)

// initChromedp 初始化全局的 chromedp Allocator，连接到一个已存在的 Chrome 实例。
// 使用 sync.Once 确保这个初始化过程在整个应用生命周期中只执行一次。
// 返回一个 cancel 函数，用于在程序退出时优雅地关闭 Allocator。
func initChromedp() (context.CancelFunc, error) {
	var cancel context.CancelFunc
	var err error

	allocatorInitOnce.Do(func() {
		wsURL := os.Getenv("DEVTOOLS_URL")
		if wsURL == "" {
			wsURL = "ws://127.0.0.1:9222" // 默认值
		}

		// 创建一个可以被取消的父 context
		allocatorCtx, cancelFunc := context.WithCancel(context.Background())

		// 使用 RemoteAllocator 连接到 Chrome
		globalAllocatorCtx, cancel = chromedp.NewRemoteAllocator(allocatorCtx, wsURL)

		//log.Printf("Chromedp remote allocator connected to %s", wsURL)

		// 设置信号处理，以便在接收到中断信号时调用 cancel 函数
		go setupGracefulShutdown(cancelFunc)
	})

	if globalAllocatorCtx == nil {
		err = errors.New("chromedp allocator failed to initialize")
	}

	return cancel, err
}

// getSandboxURL 通过执行一段初始化 JS 来获取沙箱环境的 URL 和一个可能的初始 token。
// 这个过程需要在真实的 DuckDuckGo 页面上执行。
func (p *Provider) getSandboxURL() (string, string, error) {
	if p.sandboxURL.isValid() {
		return p.sandboxURL.Value, "", nil
	}

	// 这段 JS 的目的是：
	// 1. 访问 /duckchat/v1/status 获取 X-Vqd-Hash-1 头。
	// 2. 解码并执行头里的 JS 代码，得到一个 JSON 对象。
	// 3. 将当前页面的 HTML 内容编码为 data URI 作为沙箱 URL。
	// 4. 返回沙箱 URL 和第一步中执行 JS 的结果。
	initJS := `
		(async function() {
			const base64DecodeUnicode = str => decodeURIComponent(atob(str).split('').map(c => '%' + ('00' + c.charCodeAt(0).toString(16)).slice(-2)).join(''));
			const executeHeaderCode = async () => {
				try {
					const response = await fetch('https://duck.ai/duckchat/v1/status', { credentials: 'include', headers: { 'x-vqd-accept': '1' } });
					const hash = response.headers.get('X-Vqd-Hash-1');
					if (!hash) throw new Error('Header X-Vqd-Hash-1 not found.');
					return eval(base64DecodeUnicode(hash));
				} catch (error) {
					console.error('Error:', error);
					throw error;
				}
			};
			return {
				"sandboxUrl": 'data:text/html;charset=utf-8,' + encodeURIComponent(window.top.document.documentElement.outerHTML),
				"initialJsResult": await executeHeaderCode()
			};
		})();
	`

	logger.Infof("getting sanboxURL from chromedp")
	initialURL := "https://duck.ai/"
	var result struct {
		SandboxURL      string         `json:"sandboxUrl"`
		InitialJSResult map[string]any `json:"initialJsResult"`
	}

	err := executeJS(initialURL, initJS, &result)
	if err != nil {
		return "", "", fmt.Errorf("failed to execute initial JS for sandbox: %w", err)
	}

	if result.SandboxURL == "" {
		return "", "", errors.New("JS execution did not return a sandbox URL")
	}

	// 尝试从初始 JS 结果中生成一个 token
	initialToken, err := encodeToToken(result.InitialJSResult)
	if err != nil {
		// 即使无法生成初始 token，也可能不是致命错误，可以继续后续流程
		log.Printf("Could not generate initial token from sandbox result: %v", err)
		return result.SandboxURL, "", nil
	}

	return result.SandboxURL, initialToken, nil
}

// generateTokenFromJS 在给定的沙箱环境中执行 JS 代码以生成 token。
func (p *Provider) generateTokenFromJS(jsCode, sandboxURL string) (string, error) {
	var rawJsResult map[string]any
	err := executeJS(sandboxURL, jsCode, &rawJsResult)
	if err != nil {
		return "", err
	}

	if rawJsResult == nil {
		return "", errors.New("JS execution returned empty result")
	}

	return encodeToToken(rawJsResult)
}

// executeJS 是一个通用的辅助函数，用于在新的 ChromeDP 标签页中导航到指定 URL 并执行 JS。
func executeJS(url, jsCode string, result any) error {
	if globalAllocatorCtx == nil {
		return errors.New("chromedp allocator not initialized")
	}

	// 从全局 Allocator 创建一个新的上下文（标签页）
	tabCtx, tabCancel := chromedp.NewContext(globalAllocatorCtx)
	defer tabCancel()

	// 为 JS 执行设置超时
	execCtx, execCancel := context.WithTimeout(tabCtx, 30*time.Second)
	defer execCancel()

	err := chromedp.Run(execCtx,
		chromedp.Navigate(url),
		chromedp.Poll(`document.readyState === "complete" && document.querySelectorAll("#jsa").length > 0`, nil),
		// chromedp.Click("button[data-testid='DUCKAI_ONBOARDING_AGREE']"),
		// 等待页面加载完成，对于 data URI，这通常是瞬间的
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
		// 执行 JS 并将结果 unmarshal 到提供的 result 变量中
		chromedp.Evaluate(jsCode, result, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
			return p.WithAwaitPromise(true)
		}),
	)

	return err
}

// encodeToToken 将 JS 执行返回的 map 编码为最终的 vqd-hash token。
func encodeToToken(rawJsResult map[string]any) (string, error) {
	if hashes, ok := rawJsResult["client_hashes"].([]any); ok && len(hashes) >= 3 {
		hashes[0] = UA
		for i, v := range hashes {
			if s, ok := v.(string); ok {
				hashes[i] = sha256AndBase64(s)
			}
		}
	}

	finalJsonBytes, err := json.Marshal(rawJsResult)
	if err != nil {
		return "", fmt.Errorf("failed to serialize final JSON result: %w", err)
	}
	return base64.StdEncoding.EncodeToString(finalJsonBytes), nil
}

func asciiEscape(s string) string {
	var b strings.Builder
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		if r <= 0x7f {
			b.WriteRune(r)
		} else {
			// 写成 \uXXXX 或 \UXXXXXXXX（根据需要）
			if r <= 0xffff {
				fmt.Fprintf(&b, "\\u%04x", r)
			} else {
				fmt.Fprintf(&b, "\\U%08x", r)
			}
		}
	}
	return b.String()
}

// sha256AndBase64 是一个加密辅助函数。
func sha256AndBase64(input string) string {
	h := sha256.New()
	h.Write([]byte(input))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// setupGracefulShutdown 监听操作系统信号，实现优雅关闭。
func setupGracefulShutdown(cancel context.CancelFunc) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		logger.Infof("Received exit signal, shutting down gracefully...")
		cancel()
		// 给一点时间让资源释放
		time.Sleep(1 * time.Second)
		os.Exit(0)
	}()
}
