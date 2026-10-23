package duckgo

import (
	"aurora/httpclient"
	"aurora/logger"
	duckgotypes "aurora/typings/duckgo"
	"bytes"
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
// 它封装了获取和缓存 vqd-hash token 的所有逻辑。
// 这避免了使用全局变量，使代码更易于测试和维护。
type Provider struct {
	client      httpclient.AuroraHttpClient
	newClient   func() httpclient.AuroraHttpClient // 用于在会话被风控时重建客户端
	clientGen   uint64                             // 客户端代际，每次重建 +1
	lastReset   time.Time
	clientMutex sync.Mutex // 保护 client/clientGen/lastReset
	proxyURL    string
	vqdToken    cachedItem[string] // 缓存 vqd-hash token
	jsCode      cachedItem[string] // 缓存从 header 获取的 JS 代码
	tokenMutex  sync.Mutex         // 用于保护 token 刷新过程的互斥锁
	feVersion   string
	// 从环境变量读取的缓存时间
	tokenExpiration      time.Duration
	scriptsCacheDuration time.Duration
	// 418/429 自动重试配置
	maxAttempts       int
	sessionResetAfter int
}

const sessionResetCooldown = 5 * time.Second

var generateVQDToken = generateVQDTokenFromJS

// getDurationFromEnv 是一个辅助函数，用于从环境变量中安全地读取时间（秒）。
func getDurationFromEnv(key string, defaultValue time.Duration) time.Duration {
	valStr := os.Getenv(key)
	if valStr != "" {
		if valInt, err := strconv.Atoi(valStr); err == nil && valInt > 0 {
			return time.Duration(valInt) * time.Second
		}
	}
	return defaultValue
}

// NewProvider 创建一个新的 Provider 实例。
// newClient 用于创建 HTTP 客户端，在会话被风控时会被再次调用以重建客户端。
func NewProvider(newClient func() httpclient.AuroraHttpClient, proxyURL string) (*Provider, error) {
	client := newClient()
	if proxyURL != "" {
		client.SetProxy(proxyURL)
	}
	provider := &Provider{
		client:    client,
		newClient: newClient,
		proxyURL:  proxyURL,
		feVersion: getStringFromEnv("FE_VERSION", "serp_20260424_180649_ET-0bdc33b2a02ebf8f235def65d887787f694720a1"),
		// 初始化缓存时间
		tokenExpiration:      getDurationFromEnv("TOKEN_EXPIRATION_SECONDS", 1*time.Second),
		scriptsCacheDuration: getDurationFromEnv("SCRIPTS_CACHE_SECONDS", 3600*time.Second),
		maxAttempts:          getIntFromEnv("UPSTREAM_MAX_RETRIES", 6) + 1,
		sessionResetAfter:    getIntFromEnv("SESSION_RESET_AFTER_FAILURES", 2),
	}
	provider.warmSession()
	return provider, nil
}

func getIntFromEnv(key string, defaultValue int) int {
	if val, err := strconv.Atoi(os.Getenv(key)); err == nil && val >= 0 {
		return val
	}
	return defaultValue
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
	logger.Warnf("All caches have been invalidated due to an API error.")
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
	// 1. 获取挑战 JS
	jsCode := p.jsCode.Value
	if !p.jsCode.isValid() {
		var err error
		jsCode, err = p.getScripts(true)
		if err != nil {
			return "", fmt.Errorf("failed to get scripts for token generation: %w", err)
		}
		// 使用环境变量配置的缓存时间
		p.jsCode = cachedItem[string]{Value: jsCode, ExpireAt: time.Now().Add(p.scriptsCacheDuration)}
	}

	// 2. 在内嵌 Goja 运行时中执行挑战，不依赖外部浏览器。
	token, err := generateVQDToken(jsCode)
	if err != nil {
		return "", fmt.Errorf("goja VQD generation failed: %w", err)
	}

	p.cacheToken(token)
	logger.Debugf("Successfully refreshed VQD token.")
	return token, nil
}

func generateVQDTokenFromJS(jsCode string) (string, error) {
	payload, err := executeVQDHashScript(jsCode)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString([]byte(payload)), nil
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
	client, _ := p.currentClient()
	response, err := client.Request(httpclient.GET, "https://duck.ai/duckchat/v1/status", header, nil, nil)
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
	bodyJSON, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}
	return p.doWithRetry(httpclient.POST, "https://duck.ai/duckchat/v1/chat", bodyJSON, func(header httpclient.AuroraHeaders) {
		header.Set("accept", "text/event-stream")
	})
}

// AuthenticatedRequest reuses this Provider's VQD acquisition, cache and
// invalidation path for non-chat Duck.ai endpoints.
func (p *Provider) AuthenticatedRequest(method, url, contentType, accept string, body []byte) (*http.Response, error) {
	return p.doWithRetry(httpclient.HttpMethod(method), url, body, func(header httpclient.AuroraHeaders) {
		if contentType != "" {
			header.Set("content-type", contentType)
		}
		if accept != "" {
			header.Set("accept", accept)
		}
		header.Set("x-ddg-journey-id", fmt.Sprintf("%x", rand.Uint64()))
	})
}

// isRetryableStatus 判断上游状态码是否为可通过重试恢复的风控/限流错误。
func isRetryableStatus(code int) bool {
	return code == http.StatusTeapot || code == http.StatusTooManyRequests
}

// doWithRetry 发送带 VQD 的请求，遇到 418/429 或网络错误时自动重试，对调用方透明。
// 连续失败达到 sessionResetAfter 次后会重建 HTTP 客户端并重新预热会话，
// 效果等同于重启容器（清空 cookie、连接池并更换 TLS 指纹）。
// 最后一次仍失败时，原样返回上游响应，交给调用方的错误处理。
func (p *Provider) doWithRetry(method httpclient.HttpMethod, url string, body []byte, decorate func(httpclient.AuroraHeaders)) (*http.Response, error) {
	var lastErr error
	failures := 0
	for attempt := 0; attempt < p.maxAttempts; attempt++ {
		if failures > 0 && p.sessionResetAfter > 0 && failures%p.sessionResetAfter == 0 {
			p.resetSession()
		}

		token, err := p.GetToken()
		if err != nil {
			lastErr = fmt.Errorf("failed to get VQD token: %w", err)
			failures++
			p.InvalidateCache()
			time.Sleep(retryDelay(attempt, 0))
			continue
		}

		header := createHeader()
		decorate(header)
		header.Set("x-vqd-hash-1", token)
		header.Set("x-fe-signals", makeFESignals())
		header.Set("x-fe-version", p.feVersion)

		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		client, gen := p.currentClient()
		resp, err := client.Request(method, url, header, sessionCookies(), reader)
		if err != nil {
			lastErr = err
			failures++
			p.markClientBroken(gen)
			time.Sleep(retryDelay(attempt, 0))
			continue
		}

		p.updateScriptsFromHeader(resp.Header)
		if !isRetryableStatus(resp.StatusCode) {
			return resp, nil
		}
		if attempt == p.maxAttempts-1 {
			return resp, nil
		}

		challengeBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		logger.Warnf("Duck.ai rejected request (%d), retrying (%d/%d): %s", resp.StatusCode, attempt+1, p.maxAttempts-1, string(challengeBody))
		lastErr = fmt.Errorf("Duck.ai rejected request (%d): %s", resp.StatusCode, string(challengeBody))
		failures++
		p.InvalidateCache()
		time.Sleep(retryDelay(attempt, parseRetryAfter(resp.Header.Get("Retry-After"))))
	}
	return nil, lastErr
}

// retryDelay 计算指数退避+抖动的等待时间，上游给出 Retry-After 时优先使用（上限 10s）。
func retryDelay(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > 10*time.Second {
			retryAfter = 10 * time.Second
		}
		return retryAfter
	}
	delay := time.Duration(300*(1<<attempt)) * time.Millisecond
	if delay > 5*time.Second {
		delay = 5 * time.Second
	}
	return delay + time.Duration(rand.Intn(300))*time.Millisecond
}

func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	if secs, err := strconv.Atoi(value); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil {
		return time.Until(t)
	}
	return 0
}

// currentClient 返回当前 HTTP 客户端及其代际编号。
func (p *Provider) currentClient() (httpclient.AuroraHttpClient, uint64) {
	p.clientMutex.Lock()
	defer p.clientMutex.Unlock()
	return p.client, p.clientGen
}

// markClientBroken 在网络错误时重建客户端；仅当 gen 仍为当前代际时才重建，避免并发请求重复重建。
func (p *Provider) markClientBroken(gen uint64) {
	p.clientMutex.Lock()
	stale := gen != p.clientGen
	p.clientMutex.Unlock()
	if !stale {
		p.resetSession()
	}
}

// resetSession 重建 HTTP 客户端、清空缓存并重新预热会话，相当于进程内“重启”。
// 在 sessionResetCooldown 内只执行一次，避免并发请求同时触发重建。
func (p *Provider) resetSession() {
	if p.newClient == nil {
		p.InvalidateCache()
		return
	}
	p.clientMutex.Lock()
	if time.Since(p.lastReset) < sessionResetCooldown {
		p.clientMutex.Unlock()
		return
	}
	client := p.newClient()
	if p.proxyURL != "" {
		client.SetProxy(p.proxyURL)
	}
	p.client = client
	p.clientGen++
	p.lastReset = time.Now()
	p.clientMutex.Unlock()

	p.InvalidateCache()
	logger.Warnf("Duck.ai session reset: rebuilt HTTP client and re-warming session.")
	p.warmSession()
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

	client, _ := p.currentClient()
	if resp, err := client.Request(httpclient.GET, "https://duck.ai/", header, sessionCookies(), nil); err == nil && resp != nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if resp, err := client.Request(httpclient.GET, "https://duck.ai/chat?ia=chat&duckai=1", header, sessionCookies(), nil); err == nil && resp != nil {
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
