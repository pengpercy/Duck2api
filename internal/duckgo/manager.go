package duckgo

import (
	"aurora/httpclient"
	"aurora/logger"
	duckgotypes "aurora/typings/duckgo"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
)

// cachedItem 结构体用于存储带有过期时间的数据。
type cachedItem[T any] struct {
	Value    T
	ExpireAt time.Time
}

// isValid 检查缓存项是否仍然有效。
func (ci *cachedItem[T]) isValid() bool {
	// 使用一个泛型零值变量来检查Value是否已设置
	// DeepEqual might be too slow, for string, pointer, slice, map, we can just check if it is nil or empty
	// Here we assume T is a pointer type or a type that has a meaningful zero value (like string).
	// This simple check might need to be adjusted if T were, for example, a struct that's valid when empty.
	// For our use case (string, struct pointers), this is sufficient.
	// A simple check for expiration is often enough if we always set a value.
	return !ci.ExpireAt.IsZero() && time.Now().Before(ci.ExpireAt)
}

// Provider 是 DuckDuckGo API 的核心管理器。
// 它封装了获取和缓存 vqd-hash token 的所有逻辑，并管理对 ChromeDP 的调用。
// 这避免了使用全局变量，使代码更易于测试和维护。
type Provider struct {
	client                  httpclient.AuroraHttpClient
	proxyURL                string
	vqdToken                cachedItem[string] // 缓存 vqd-hash token
	jsCode                  cachedItem[string] // 缓存从 header 获取的 JS 代码
	sandboxURL              cachedItem[string] // 缓存用于执行 JS 的沙箱环境 URL
	tokenMutex              sync.Mutex         // 用于保护 token 刷新过程的互斥锁
	browserMutex            sync.Mutex         // Duck.ai 页面自动化串行锁
	browserToken            cachedItem[browserTokenState]
	chromeCancel            context.CancelFunc // 用于在程序结束时关闭 ChromeDP 上下文
	browserCtx              context.Context
	browserCancel           context.CancelFunc
	browserListenerAttached bool
	browserRefreshInFlight  bool
	browserRequestHeadersCh chan network.Headers
	feVersion               string
	// 从环境变量读取的缓存时间
	tokenExpiration      time.Duration
	scriptsCacheDuration time.Duration
	sandboxCacheDuration time.Duration
}

// getDurationFromEnv 是一个辅助函数，用于从环境变量中安全地读取时间（秒）。
func getDurationFromEnv(key string, defaultValue time.Duration) time.Duration {
	valStr := os.Getenv(key)
	if valStr != "" {
		if valInt, err := strconv.Atoi(valStr); err == nil && valInt >= 0 {
			return time.Duration(valInt) * time.Second
		}
	}
	return defaultValue
}

// NewProvider 创建一个新的 Provider 实例。
// 它会初始化 ChromeDP 环境。
func NewProvider(client httpclient.AuroraHttpClient, proxyURL string) (*Provider, error) {
	cancel, err := initChromedp()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize chromedp: %w", err)
	}

	provider := &Provider{
		client:       client,
		proxyURL:     proxyURL,
		chromeCancel: cancel,
		feVersion:    getStringFromEnv("FE_VERSION", "serp_20260424_180649_ET-0bdc33b2a02ebf8f235def65d887787f694720a1"),
		// 初始化缓存时间
		tokenExpiration:      getDurationFromEnv("TOKEN_EXPIRATION_SECONDS", 1*time.Second),
		scriptsCacheDuration: getDurationFromEnv("SCRIPTS_CACHE_SECONDS", 3600*time.Second),
		sandboxCacheDuration: getDurationFromEnv("SANDBOX_CACHE_SECONDS", 86400*time.Second),
	}
	if os.Getenv("DUCKAI_BROWSER_CHAT") == "0" {
		provider.warmSession()
	} else if os.Getenv("DUCKAI_BROWSER_PREWARM") == "1" {
		go provider.prewarmBrowserToken()
	}
	return provider, nil
}

func getStringFromEnv(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}

// InvalidateCache 在 API 返回错误时清空所有缓存。
// 这是线程安全的。
func (p *Provider) InvalidateCache() {
	p.tokenMutex.Lock()
	defer p.tokenMutex.Unlock()

	p.vqdToken = cachedItem[string]{}
	p.jsCode = cachedItem[string]{}
	p.sandboxURL = cachedItem[string]{}
	logger.Warnf("All caches have been invalidated due to an API error.")
}

// Close 优雅地关闭 Provider 所持有的资源，例如 ChromeDP 连接。
func (p *Provider) Close() {
	logger.Infof("Closing Provider resources...")
	p.browserMutex.Lock()
	if p.browserCancel != nil {
		p.browserCancel()
		p.browserCtx = nil
		p.browserCancel = nil
		p.browserListenerAttached = false
		p.browserRequestHeadersCh = nil
	}
	p.browserMutex.Unlock()
	p.chromeCancel()
}

// GetToken 获取一个有效的 vqd-hash token。
// 如果缓存的 token 无效或已过期，它会自动执行刷新流程。
// 这个方法是线程安全的。
func (p *Provider) GetToken() (string, error) {
	p.tokenMutex.Lock()
	defer p.tokenMutex.Unlock()

	if p.vqdToken.isValid() {
		// p.getScripts(false)
		return p.vqdToken.Value, nil
	}

	// 如果 token 无效，则启动完整的刷新流程
	logger.Infof("Refreshing token...")
	return p.refreshToken()
}

// refreshToken 执行获取新 token 的完整流程。
// 注意：此方法不是线程安全的，应由 GetToken 等公共方法在锁的保护下调用。
func (p *Provider) refreshToken() (string, error) {
	// 1. 获取执行 JS 所需的沙箱环境
	sandboxURL, _, err := p.getSandboxURL()
	if err != nil {
		return "", fmt.Errorf("failed to get sandbox url: %w", err)
	}
	// 使用环境变量配置的缓存时间
	p.sandboxURL = cachedItem[string]{Value: sandboxURL, ExpireAt: time.Now().Add(p.sandboxCacheDuration)}

	// 2. 获取 JS
	jsCode := p.jsCode.Value
	if !p.jsCode.isValid() {
		jsCode, err = p.getScripts(true)
		if err != nil {
			return "", fmt.Errorf("failed to get scripts for token generation: %w", err)
		}
		// 使用环境变量配置的缓存时间
		p.jsCode = cachedItem[string]{Value: jsCode, ExpireAt: time.Now().Add(p.scriptsCacheDuration)}
	}

	// 3. 生成 Token
	token, err := p.generateTokenFromJS(jsCode, sandboxURL)
	if err != nil {
		return "", fmt.Errorf("failed to execute obfuscated js: %w", err)
	}

	p.cacheToken(token)
	logger.Debugf("Successfully refreshed VQD token.")
	return token, nil
}

// cacheToken 将新的 token 存入缓存，并设置过期时间。
func (p *Provider) cacheToken(token string) {
	// 使用环境变量配置的缓存时间
	p.vqdToken = cachedItem[string]{
		Value:    token,
		ExpireAt: time.Now().Add(p.tokenExpiration),
	}
}

// getScripts 从 DuckDuckGo status 接口获取用于生成 token 的 JS 代码。
// 它会优先使用缓存。
func (p *Provider) getScripts(fresh bool) (string, error) {
	if p.jsCode.isValid() {
		return p.jsCode.Value, nil
	}

	header := createHeader()
	header.Set("accept", "*/*")
	header.Set("cache-control", "no-store")
	header.Set("pragma", "no-cache")
	if fresh {
		header.Set("x-vqd-accept", "1")
	}

	logger.Infof("Get scripts from /duckchat/v1/status")
	response, err := p.client.Request(httpclient.GET, "https://duck.ai/duckchat/v1/status", header, nil, nil)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status request failed with code: %d", response.StatusCode)
	}
	if !fresh {
		return "PreValidate Successf", nil
	}

	base64EncodedJs := response.Header.Get("x-vqd-hash-1")
	if base64EncodedJs == "" && fresh {
		return "", errors.New("x-vqd-hash-1 header not found in status response")
	}

	decodedJsBytes, err := base64.StdEncoding.DecodeString(base64EncodedJs)
	if err != nil {
		return "", fmt.Errorf("failed to decode x-vqd-hash-1: %w", err)
	}

	return string(decodedJsBytes), nil
}

// PostConversation 发送聊天请求到 DuckAI API。
func (p *Provider) PostConversation(request duckgotypes.ApiRequest) (*http.Response, error) {
	if os.Getenv("DUCKAI_BROWSER_CHAT") != "0" {
		return p.PostConversationViaBrowser(request)
	}

	if p.proxyURL != "" {
		p.client.SetProxy(p.proxyURL)
	}

	bodyJSON, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		token, err := p.GetToken()
		if err != nil {
			return nil, fmt.Errorf("failed to get a valid token for chat: %w", err)
		}

		header := createHeader()
		header.Set("accept", "text/event-stream")
		header.Set("x-vqd-hash-1", token)
		header.Set("x-fe-signals", makeFESignals())
		header.Set("x-fe-version", p.feVersion)

		response, err := p.client.Request(httpclient.POST, "https://duck.ai/duckchat/v1/chat", header, sessionCookies(), bytes.NewBuffer(bodyJSON))
		if err != nil {
			lastErr = err
			continue
		}

		p.updateScriptsFromHeader(response.Header)
		if response.StatusCode != http.StatusTeapot {
			return response, nil
		}

		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		lastErr = fmt.Errorf("duck.ai challenge rejected request: %s", string(body))
		p.InvalidateCache()
		time.Sleep(time.Duration(300+attempt*400+rand.Intn(250)) * time.Millisecond)
	}
	return nil, lastErr
}

// updateScriptsFromHeader 从响应头中提取并更新缓存的 JS 代码。
func (p *Provider) updateScriptsFromHeader(header http.Header) {
	base64EncodedJs := header.Get("x-vqd-hash-1")
	if base64EncodedJs == "" {
		return
	}

	decodedJsBytes, err := base64.StdEncoding.DecodeString(base64EncodedJs)
	if err != nil {
		logger.Errorf("Error decoding new script from header: %v", err)
		return
	}

	p.tokenMutex.Lock()
	defer p.tokenMutex.Unlock()
	p.jsCode = cachedItem[string]{
		Value: string(decodedJsBytes),
		// 使用环境变量配置的缓存时间
		ExpireAt: time.Now().Add(p.scriptsCacheDuration),
	}
	logger.Debugf("Updated JS scripts from response header.")
}

func (p *Provider) warmSession() {
	header := createHeader()
	header.Set("accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	header.Set("sec-fetch-dest", "document")
	header.Set("sec-fetch-mode", "navigate")
	header.Set("sec-fetch-site", "none")
	header.Set("sec-fetch-user", "?1")
	header.Set("upgrade-insecure-requests", "1")

	if resp, err := p.client.Request(httpclient.GET, "https://duck.ai/", header, sessionCookies(), nil); err == nil && resp != nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if resp, err := p.client.Request(httpclient.GET, "https://duckduckgo.com/?q=DuckDuckGo+AI+Chat&ia=chat&duckai=1", header, sessionCookies(), nil); err == nil && resp != nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

func sessionCookies() []*http.Cookie {
	return []*http.Cookie{
		{Name: "5", Value: "1"},
		{Name: "ah", Value: "wt-wt"},
		{Name: "dcs", Value: "1"},
		{Name: "dcm", Value: "3"},
		{Name: "isRecentChatOn", Value: "1"},
	}
}

func makeFESignals() string {
	now := time.Now().UnixMilli()
	duration := int64(3000)
	t := int64(80 + rand.Intn(100))
	events := []map[string]any{
		{"name": "onboarding_impression_1", "delta": t},
	}
	t += int64(120 + rand.Intn(140))
	events = append(events, map[string]any{"name": "onboarding_impression_2", "delta": t})
	t += int64(200 + rand.Intn(300))
	events = append(events, map[string]any{"name": "startNewChat", "delta": t})
	for i := 0; i < 6+rand.Intn(12); i++ {
		t += int64(40 + rand.Intn(140))
		events = append(events, map[string]any{"name": "user_input", "delta": t})
	}
	t += int64(120 + rand.Intn(230))
	events = append(events, map[string]any{"name": "user_submit", "delta": t})
	payload := map[string]any{
		"start":  now - duration,
		"events": events,
		"end":    maxInt64(t+int64(20+rand.Intn(70)), duration),
	}
	data, _ := json.Marshal(payload)
	return base64.StdEncoding.EncodeToString(data)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
