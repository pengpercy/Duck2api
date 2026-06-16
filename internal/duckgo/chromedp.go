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
	"net/http"
	"net/url"
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

const duckAIPageURL = "https://duck.ai/?duck2api=1"

// initChromedp 初始化全局的 chromedp Allocator，连接到一个已存在的 Chrome 实例。
// 使用 sync.Once 确保这个初始化过程在整个应用生命周期中只执行一次。
// 返回一个 cancel 函数，用于在程序退出时优雅地关闭 Allocator。
func initChromedp() (context.CancelFunc, error) {
	var cancel context.CancelFunc
	var err error

	allocatorInitOnce.Do(func() {
		wsURL := os.Getenv("DEVTOOLS_URL")
		if wsURL == "" {
			wsURL = "ws://127.0.0.1:9222"
		}

		allocatorCtx, cancelFunc := context.WithCancel(context.Background())
		globalAllocatorCtx, cancel = chromedp.NewRemoteAllocator(allocatorCtx, wsURL)
		go setupGracefulShutdown(cancelFunc)
	})

	if globalAllocatorCtx == nil {
		err = errors.New("chromedp allocator failed to initialize")
	}

	return cancel, err
}

// getSandboxURL 通过执行一段初始化 JS 来获取沙箱环境的 URL 和一个可能的初始 token。
func (p *Provider) getSandboxURL() (string, string, error) {
	if p.sandboxURL.isValid() {
		return p.sandboxURL.Value, "", nil
	}

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
	initialURL := duckAIPageURL
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

	initialToken, err := encodeToToken(result.InitialJSResult)
	if err != nil {
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

	tabCtx, tabCancel := chromedp.NewContext(globalAllocatorCtx)
	defer tabCancel()

	execCtx, execCancel := context.WithTimeout(tabCtx, 30*time.Second)
	defer execCancel()

	err := chromedp.Run(execCtx,
		chromedp.Navigate(url),
		chromedp.Poll(`document.readyState === "complete" && document.querySelectorAll("#jsa").length > 0`, nil),
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
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

	finalJSONBytes, err := json.Marshal(rawJsResult)
	if err != nil {
		return "", fmt.Errorf("failed to serialize final JSON result: %w", err)
	}
	return base64.StdEncoding.EncodeToString(finalJSONBytes), nil
}

func asciiEscape(s string) string {
	var b strings.Builder
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		if r <= 0x7f {
			b.WriteRune(r)
		} else if r <= 0xffff {
			fmt.Fprintf(&b, "\\u%04x", r)
		} else {
			fmt.Fprintf(&b, "\\U%08x", r)
		}
	}
	return b.String()
}

func sha256AndBase64(input string) string {
	h := sha256.New()
	h.Write([]byte(input))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func setupGracefulShutdown(cancel context.CancelFunc) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		logger.Infof("Received exit signal, shutting down gracefully...")
		cancel()
		time.Sleep(1 * time.Second)
		os.Exit(0)
	}()
}

type devtoolsTarget struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	URL  string `json:"url"`
}

func cleanupStaleDuckAITargets() {
	baseURL, err := devtoolsHTTPBaseURL()
	if err != nil {
		logger.Warnf("Could not derive DevTools HTTP URL for stale tab cleanup: %v", err)
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(baseURL + "/json/list")
	if err != nil {
		logger.Warnf("Could not list DevTools targets for stale tab cleanup: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		logger.Warnf("DevTools target list returned status %d", resp.StatusCode)
		return
	}

	var targets []devtoolsTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		logger.Warnf("Could not decode DevTools target list: %v", err)
		return
	}

	closed := 0
	for _, target := range targets {
		if target.ID == "" || target.Type != "page" || !shouldCleanupBrowserTarget(target.URL) {
			continue
		}
		closeURL := baseURL + "/json/close/" + url.PathEscape(target.ID)
		closeResp, err := client.Get(closeURL)
		if err != nil {
			logger.Warnf("Could not close stale Duck.ai target %s: %v", target.ID, err)
			continue
		}
		closeResp.Body.Close()
		if closeResp.StatusCode == http.StatusOK {
			closed++
		}
	}
	if closed > 0 {
		logger.Infof("Closed %d stale Duck.ai browser page(s)", closed)
	}
}

func devtoolsHTTPBaseURL() (string, error) {
	raw := os.Getenv("DEVTOOLS_URL")
	if raw == "" {
		raw = "ws://127.0.0.1:9222"
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "ws":
		parsed.Scheme = "http"
	case "wss":
		parsed.Scheme = "https"
	case "http", "https":
	default:
		return "", fmt.Errorf("unsupported DevTools URL scheme %q", parsed.Scheme)
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func shouldCleanupBrowserTarget(raw string) bool {
	switch strings.ToLower(getStringFromEnv("DUCKAI_BROWSER_CLEANUP_SCOPE", "duckai")) {
	case "duckai":
		return isDuckAIPageURL(raw)
	case "marked":
		return isDuck2APIPageURL(raw)
	default:
		return false
	}
}

func isDuckAIPageURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if parsed.Host == "duck.ai" {
		return true
	}
	return strings.HasSuffix(parsed.Host, "duckduckgo.com") && parsed.Query().Get("duckai") == "1"
}

func isDuck2APIPageURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return parsed.Host == "duck.ai" && parsed.Query().Get("duck2api") == "1"
}
