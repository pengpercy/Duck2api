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
	"strings"
	"sync"
	"sync/atomic"
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
	sessionAt   time.Time  // 当前客户端创建时间，受 clientMutex 保护
	sessionReqs int        // 当前客户端上发出的上游 API 请求数，受 clientMutex 保护
	clientMutex sync.Mutex // 保护 client/clientGen/lastReset
	proxyURL    string
	vqdToken    cachedItem[string] // 缓存 vqd-hash token
	jsCode      cachedItem[string] // 缓存从 header 获取的 JS 代码
	tokenMutex  sync.Mutex         // 用于保护 token 刷新过程的互斥锁
	tokenAt     time.Time          // 当前 token 生成时间，受 tokenMutex 保护
	tokenUses   int                // 当前 token 已被使用次数，受 tokenMutex 保护
	feVersion   string
	// 从环境变量读取的缓存时间
	tokenExpiration      time.Duration
	tokenMaxUses         int // 单个 token 最多使用次数（0 不限），浏览器每条消息都重新求解
	scriptsCacheDuration time.Duration
	// 418/429 自动重试配置
	maxAttempts       int
	sessionResetAfter int
	// 空闲超过 idleResetAfter 后，下一次请求前先重建会话（0 关闭）
	idleResetAfter time.Duration
	lastSuccess    time.Time // 最近一次上游成功响应时间，受 clientMutex 保护
	// 诊断用计数器
	reqSeq   uint64 // 请求序号，用于串联同一请求的多行日志
	inflight int64  // 正在进行（含仍在读取响应流）的上游请求数
}

const sessionResetCooldown = 5 * time.Second

var generateVQDToken = generateVQDTokenFromJS

// sleep 可在测试中替换，避免真实等待退避时间
var sleep = time.Sleep

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
		sessionAt: time.Now(),
		newClient: newClient,
		proxyURL:  proxyURL,
		feVersion: getStringFromEnv("FE_VERSION", "serp_20260424_180649_ET-0bdc33b2a02ebf8f235def65d887787f694720a1"),
		// 初始化缓存时间
		tokenExpiration: getDurationFromEnv("TOKEN_EXPIRATION_SECONDS", 1*time.Second),
		// 默认一次一用，每次请求都重新求解挑战。
		tokenMaxUses: getIntFromEnv("TOKEN_MAX_USES", 1),
		// 默认不缓存挑战 JS：每个新请求都从 /status 取新挑战，复用旧挑战容易触发 418
		// 必须保持 0：缓存后会改用 chat 响应头下发的挑战求解，实测同一会话 4~8 次后必然 418 ERR_BN_LIMIT，
		// 且首次尝试通过率从 ~58% 降到 ~25%；每次从 /status 取新挑战则未再出现 ERR_BN_LIMIT。
		scriptsCacheDuration: getDurationFromEnv("SCRIPTS_CACHE_SECONDS", 0),
		maxAttempts:          getIntFromEnv("UPSTREAM_MAX_RETRIES", 6) + 1,
		// 实测连续 418 两次就重建，第 3 次尝试的通过率约 40%（4 次才重建时约 12%）。
		// 但若返回体里的 p 在整段时间内不变，通常是 IP 级风控窗口，重建也无济于事，只能靠退避等待。
		// ERR_BN_LIMIT 绑定在会话上，遇到时立即重建（见 doWithRetry）。
		sessionResetAfter: getIntFromEnv("SESSION_RESET_AFTER_FAILURES", 2),
		// 新建会话首个请求 418 的概率很高（实测 10/13），而长时间空闲的老会话并无明显劣势，
		// 因此只在空闲很久后才重建。
		idleResetAfter: time.Duration(getIntFromEnv("IDLE_SESSION_RESET_SECONDS", 1800)) * time.Second,
	}
	provider.warmSession("startup")
	provider.lastSuccess = time.Now()
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
	p.clearCache()
	logger.Warnf("All caches have been invalidated due to an API error.")
}

func (p *Provider) clearCache() {
	p.tokenMutex.Lock()
	defer p.tokenMutex.Unlock()

	p.vqdToken = cachedItem[string]{}
	p.jsCode = cachedItem[string]{}
	p.tokenUses = 0
}

// tokenInfo 描述一次取 token 的结果，用于诊断日志。
type tokenInfo struct {
	value string
	fresh bool          // 本次新生成
	age   time.Duration // 距 token 生成的时间
	uses  int           // 含本次在内的使用次数
}

func (t tokenInfo) String() string {
	src := "cached"
	if t.fresh {
		src = "fresh"
	}
	return fmt.Sprintf("token=%s token_age=%s token_uses=%d", src, fmtDur(t.age), t.uses)
}

// GetToken 获取一个有效的 vqd-hash token。
// 如果缓存的 token 无效或已过期，它会自动执行刷新流程。
// 这个方法是线程安全的。
func (p *Provider) GetToken() (string, error) {
	info, err := p.acquireToken()
	return info.value, err
}

func (p *Provider) acquireToken() (tokenInfo, error) {
	p.tokenMutex.Lock()
	defer p.tokenMutex.Unlock()

	if p.vqdToken.isValid() && (p.tokenMaxUses <= 0 || p.tokenUses < p.tokenMaxUses) {
		p.tokenUses++
		return tokenInfo{value: p.vqdToken.Value, age: time.Since(p.tokenAt), uses: p.tokenUses}, nil
	}

	// 如果 token 无效，则启动完整的刷新流程
	logger.Infof("Refreshing token...")
	token, err := p.refreshToken()
	if err != nil {
		return tokenInfo{}, err
	}
	p.tokenUses = 1
	return tokenInfo{value: token, fresh: true, uses: 1}, nil
}

// refreshToken 执行获取新 token 的完整流程。
// 注意：此方法不是线程安全的，应由 GetToken 等公共方法在锁的保护下调用。
func (p *Provider) refreshToken() (string, error) {
	// 1. 获取挑战 JS
	jsCode := p.jsCode.Value
	if p.jsCode.isValid() {
		logger.Infof("Using cached challenge (expires in %s)", fmtDur(time.Until(p.jsCode.ExpireAt)))
	} else {
		var err error
		jsCode, err = p.getScripts(true)
		if err != nil {
			return "", fmt.Errorf("failed to get scripts for token generation: %w", err)
		}
		// 使用环境变量配置的缓存时间
		p.jsCode = cachedItem[string]{Value: jsCode, ExpireAt: time.Now().Add(p.scriptsCacheDuration)}
	}

	// 2. 在内嵌 Goja 运行时中执行挑战，不依赖外部浏览器。
	start := time.Now()
	token, err := generateVQDToken(jsCode)
	logger.Debugf("Solved VQD challenge in %s", fmtDur(time.Since(start)))
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
	p.tokenAt = time.Now()
	p.tokenUses = 0
}

// getScripts 从 DuckDuckGo status 接口获取用于生成 token 的 JS 代码。
// 它会优先使用缓存。
func (p *Provider) getScripts(fresh bool) (string, error) {
	if p.jsCode.isValid() {
		return p.jsCode.Value, nil
	}

	header := createHeader()
	delete(header, "content-type")
	header.Set("accept", "*/*")
	header.Set("cache-control", "no-store")
	header.Set("pragma", "no-cache")
	if fresh {
		header.Set("x-vqd-accept", "1")
	}

	client, gen := p.currentClient()
	start := time.Now()
	response, err := client.Request(httpclient.GET, "https://duck.ai/duckchat/v1/status", header, nil, nil)
	if err != nil {
		logger.Warnf("[status] gen=%d error after %s: %v", gen, fmtDur(time.Since(start)), err)
		return "", err
	}
	defer response.Body.Close()
	logger.Infof("[status] gen=%d code=%d dur=%s challenge=%t%s", gen, response.StatusCode, fmtDur(time.Since(start)),
		response.Header.Get("x-vqd-hash-1") != "", setCookieSummary(response.Header))

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
//
// 每次尝试都会输出一行 "[upstream] req=N attempt=i/n ..." 的 key=value 日志，便于事后统计
// 418 与会话年龄、token 复用、空闲时长、并发数之间的关系。
func (p *Provider) doWithRetry(method httpclient.HttpMethod, url string, body []byte, decorate func(httpclient.AuroraHeaders)) (*http.Response, error) {
	reqID := atomic.AddUint64(&p.reqSeq, 1)
	inflight := atomic.AddInt64(&p.inflight, 1)
	released := false
	release := func() {
		if !released {
			released = true
			atomic.AddInt64(&p.inflight, -1)
		}
	}
	defer func() {
		// 未把响应交给调用方（出错返回）时在此释放；交出响应时由 body.Close 释放
		release()
	}()

	start := time.Now()
	idleBefore := p.idleSinceSuccess()
	endpoint := shortEndpoint(url)
	logger.Infof("[upstream] req=%d start %s %s idle_before=%s inflight=%d%s", reqID, method, endpoint, fmtDur(idleBefore), inflight, bodySummary(body))

	var lastErr error
	failures := 0
	resets := 0
	if p.resetIfIdle(reqID, idleBefore) {
		resets++
	}
	sessionLimited := false
	for attempt := 0; attempt < p.maxAttempts; attempt++ {
		tag := fmt.Sprintf("req=%d attempt=%d/%d", reqID, attempt+1, p.maxAttempts)
		if sessionLimited {
			// ERR_BN_LIMIT 在原会话上通常持续到重建为止，没必要再等 4 次
			sessionLimited = false
			if p.resetSession(fmt.Sprintf("req=%d ERR_BN_LIMIT", reqID)) {
				resets++
			}
		} else if failures > 0 && p.sessionResetAfter > 0 && failures%p.sessionResetAfter == 0 {
			if p.resetSession(fmt.Sprintf("req=%d after %d consecutive failures", reqID, failures)) {
				resets++
			}
		}

		tok, err := p.acquireToken()
		if err != nil {
			lastErr = fmt.Errorf("failed to get VQD token: %w", err)
			failures++
			p.clearCache()
			delay := retryDelay(attempt, 0)
			logger.Warnf("[upstream] %s token_error elapsed=%s next_delay=%s err=%v", tag, fmtDur(time.Since(start)), fmtDur(delay), err)
			sleep(delay)
			continue
		}

		header := createHeader()
		decorate(header)
		header.Set("x-vqd-hash-1", tok.value)
		header.Set("x-fe-signals", makeFESignals())
		header.Set("x-fe-version", p.feVersion)

		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		client, sess := p.useClient()
		attemptStart := time.Now()
		resp, err := client.Request(method, url, header, sessionCookies(), reader)
		ctx := fmt.Sprintf("%s %s %s %s inflight=%d", tag, sess, tok, fmtElapsed(attemptStart, start), atomic.LoadInt64(&p.inflight))
		if err != nil {
			lastErr = err
			failures++
			delay := retryDelay(attempt, 0)
			logger.Warnf("[upstream] %s network_error next_delay=%s err=%v", ctx, fmtDur(delay), err)
			p.markClientBroken(sess.gen, reqID)
			sleep(delay)
			continue
		}

		newChallenge := resp.Header.Get("x-vqd-hash-1") != ""
		p.updateScriptsFromHeader(resp.Header)
		if !isRetryableStatus(resp.StatusCode) {
			if resp.StatusCode < 400 {
				p.markSuccess()
				logger.Infof("[upstream] %s status=%d ok resets=%d new_challenge=%t%s", ctx, resp.StatusCode, resets, newChallenge, setCookieSummary(resp.Header))
			} else {
				logger.Warnf("[upstream] %s status=%d not_retryable resets=%d%s", ctx, resp.StatusCode, resets, setCookieSummary(resp.Header))
			}
			p.wrapBody(resp, reqID, release)
			return resp, nil
		}

		challengeBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		lastErr = fmt.Errorf("Duck.ai rejected request (%d): %s", resp.StatusCode, string(challengeBody))
		logger.Debugf("[upstream] %s raw_body=%s", tag, string(challengeBody))
		if attempt == p.maxAttempts-1 {
			logger.Warnf("[upstream] %s status=%d gave_up resets=%d %s%s", ctx, resp.StatusCode, resets, summarizeChallenge(challengeBody), setCookieSummary(resp.Header))
			resp.Body = io.NopCloser(bytes.NewReader(challengeBody))
			p.wrapBody(resp, reqID, release)
			return resp, nil
		}

		retryAfter := resp.Header.Get("Retry-After")
		delay := retryDelay(attempt, parseRetryAfter(retryAfter))
		logger.Warnf("[upstream] %s status=%d %s retry_after=%q next_delay=%s new_challenge=%t%s",
			ctx, resp.StatusCode, summarizeChallenge(challengeBody), retryAfter, fmtDur(delay), newChallenge, setCookieSummary(resp.Header))
		failures++
		p.clearCache()
		sessionLimited = bytes.Contains(challengeBody, []byte("ERR_BN_LIMIT"))
		sleep(delay)
	}
	logger.Warnf("[upstream] req=%d failed attempts=%d resets=%d elapsed=%s err=%v", reqID, p.maxAttempts, resets, fmtDur(time.Since(start)), lastErr)
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
	// 1s, 2s, 4s, 6s...：过快的连续重试会被识别为机器人行为，几百毫秒内重发基本必然再次 418
	delay := time.Duration(1000*(1<<attempt)) * time.Millisecond
	if delay > 6*time.Second {
		delay = 6 * time.Second
	}
	return delay + time.Duration(rand.Intn(500))*time.Millisecond
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

func (p *Provider) markSuccess() {
	p.clientMutex.Lock()
	p.lastSuccess = time.Now()
	p.clientMutex.Unlock()
}

// idleSinceSuccess 返回距最近一次上游成功的时长（从未成功返回 0）。
func (p *Provider) idleSinceSuccess() time.Duration {
	p.clientMutex.Lock()
	defer p.clientMutex.Unlock()
	if p.lastSuccess.IsZero() {
		return 0
	}
	return time.Since(p.lastSuccess)
}

// resetIfIdle 在会话空闲过久时提前重建会话，返回是否真的重建了。
func (p *Provider) resetIfIdle(reqID uint64, idle time.Duration) bool {
	if p.idleResetAfter <= 0 || p.newClient == nil {
		return false
	}
	p.clientMutex.Lock()
	isIdle := !p.lastSuccess.IsZero() && time.Since(p.lastSuccess) > p.idleResetAfter
	if isIdle {
		// 先占位，避免并发请求重复触发
		p.lastSuccess = time.Now()
	}
	p.clientMutex.Unlock()
	if !isIdle {
		return false
	}
	return p.resetSession(fmt.Sprintf("req=%d idle %s > %s", reqID, fmtDur(idle), p.idleResetAfter))
}

// sessionInfo 是一次请求所用客户端会话的快照。
type sessionInfo struct {
	gen  uint64
	age  time.Duration
	reqs int // 含本次在内，在该客户端上发出的上游 API 请求数
}

func (s sessionInfo) String() string {
	return fmt.Sprintf("gen=%d sess_age=%s sess_reqs=%d", s.gen, fmtDur(s.age), s.reqs)
}

// currentClient 返回当前 HTTP 客户端及其代际编号。
func (p *Provider) currentClient() (httpclient.AuroraHttpClient, uint64) {
	p.clientMutex.Lock()
	defer p.clientMutex.Unlock()
	return p.client, p.clientGen
}

// useClient 返回当前客户端，并把本次请求计入会话请求数。
func (p *Provider) useClient() (httpclient.AuroraHttpClient, sessionInfo) {
	p.clientMutex.Lock()
	defer p.clientMutex.Unlock()
	p.sessionReqs++
	info := sessionInfo{gen: p.clientGen, reqs: p.sessionReqs}
	if !p.sessionAt.IsZero() {
		info.age = time.Since(p.sessionAt)
	}
	return p.client, info
}

// markClientBroken 在网络错误时重建客户端；仅当 gen 仍为当前代际时才重建，避免并发请求重复重建。
func (p *Provider) markClientBroken(gen uint64, reqID uint64) {
	p.clientMutex.Lock()
	stale := gen != p.clientGen
	p.clientMutex.Unlock()
	if !stale {
		p.resetSession(fmt.Sprintf("req=%d network error on gen=%d", reqID, gen))
	}
}

// resetSession 重建 HTTP 客户端、清空缓存并重新预热会话，相当于进程内“重启”。
// 在 sessionResetCooldown 内只执行一次，避免并发请求同时触发重建。返回是否真的重建了。
func (p *Provider) resetSession(reason string) bool {
	if p.newClient == nil {
		p.clearCache()
		return false
	}
	p.clientMutex.Lock()
	if since := time.Since(p.lastReset); since < sessionResetCooldown {
		gen := p.clientGen
		p.clientMutex.Unlock()
		logger.Infof("[session] reset skipped (%s): gen=%d was rebuilt %s ago, cooldown %s", reason, gen, fmtDur(since), sessionResetCooldown)
		return false
	}
	oldGen, oldAge, oldReqs := p.clientGen, time.Since(p.sessionAt), p.sessionReqs
	client := p.newClient()
	if p.proxyURL != "" {
		client.SetProxy(p.proxyURL)
	}
	p.client = client
	p.clientGen++
	p.lastReset = time.Now()
	p.sessionAt = p.lastReset
	p.sessionReqs = 0
	newGen := p.clientGen
	p.clientMutex.Unlock()

	p.clearCache()
	logger.Warnf("[session] reset gen=%d->%d reason=%q old_age=%s old_reqs=%d", oldGen, newGen, reason, fmtDur(oldAge), oldReqs)
	p.warmSession(reason)
	return true
}

// wrapBody 在响应体关闭时释放 inflight 计数并记录流持续时间。
func (p *Provider) wrapBody(resp *http.Response, reqID uint64, release func()) {
	if resp.Body == nil {
		release()
		return
	}
	resp.Body = &trackedBody{ReadCloser: resp.Body, start: time.Now(), onClose: func(d time.Duration) {
		release()
		logger.Debugf("[upstream] req=%d body closed after %s inflight=%d", reqID, fmtDur(d), atomic.LoadInt64(&p.inflight))
	}}
}

type trackedBody struct {
	io.ReadCloser
	start   time.Time
	once    sync.Once
	onClose func(time.Duration)
}

func (b *trackedBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { b.onClose(time.Since(b.start)) })
	return err
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

func (p *Provider) warmSession(reason string) {
	// 顶层导航请求：浏览器不会带 content-type / origin / referer
	header := createHeader()
	delete(header, "content-type")
	delete(header, "origin")
	delete(header, "referer")
	header.Set("accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	header.Set("sec-fetch-dest", "document")
	header.Set("sec-fetch-mode", "navigate")
	header.Set("sec-fetch-site", "none")
	header.Set("sec-fetch-user", "?1")
	header.Set("upgrade-insecure-requests", "1")

	client, gen := p.currentClient()
	start := time.Now()
	parts := make([]string, 0, 2)
	for _, u := range []string{"https://duck.ai/", "https://duck.ai/chat?ia=chat&duckai=1"} {
		t := time.Now()
		resp, err := client.Request(httpclient.GET, u, header, sessionCookies(), nil)
		if err != nil || resp == nil {
			parts = append(parts, fmt.Sprintf("%s=err(%v)", shortEndpoint(u), err))
			continue
		}
		n, _ := io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		parts = append(parts, fmt.Sprintf("%s=%d/%dB/%s%s", shortEndpoint(u), resp.StatusCode, n, fmtDur(time.Since(t)), setCookieSummary(resp.Header)))
	}
	logger.Infof("[session] warm gen=%d reason=%q total=%s %s", gen, reason, fmtDur(time.Since(start)), strings.Join(parts, " "))
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

// fmtDur 以紧凑形式输出时长，便于日志统计。
func fmtDur(d time.Duration) string {
	switch {
	case d <= 0:
		return "0s"
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return d.Round(time.Second).String()
	}
}

func fmtElapsed(attemptStart, reqStart time.Time) string {
	return fmt.Sprintf("dur=%s elapsed=%s", fmtDur(time.Since(attemptStart)), fmtDur(time.Since(reqStart)))
}

// shortEndpoint 去掉 scheme 与域名，只保留路径部分。
func shortEndpoint(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
		if j := strings.IndexByte(u, '/'); j >= 0 {
			return u[j:]
		}
		return "/"
	}
	return u
}

// setCookieSummary 列出响应设置的 cookie 名称（不含值），无则返回空串。
func setCookieSummary(h http.Header) string {
	cookies := h.Values("Set-Cookie")
	if len(cookies) == 0 {
		return ""
	}
	names := make([]string, 0, len(cookies))
	for _, c := range cookies {
		name, _, _ := strings.Cut(c, "=")
		names = append(names, strings.TrimSpace(name))
	}
	return " set_cookie=" + strings.Join(names, ",")
}

// summarizeChallenge 把 418 返回体压缩为便于统计的 key=value。
// p 是服务端下发的一组哈希，观察到它在重建会话前后不变，疑似与 IP/服务端风控状态绑定。
func summarizeChallenge(body []byte) string {
	var ch struct {
		Type         string `json:"type"`
		OverrideCode string `json:"overrideCode"`
		CD           struct {
			I  string `json:"i"`
			P  string `json:"p"`
			R  string `json:"r"`
			SC int    `json:"sc"`
			E  int    `json:"e"`
		} `json:"cd"`
	}
	if err := json.Unmarshal(body, &ch); err != nil {
		b := string(body)
		if len(b) > 200 {
			b = b[:200] + "..."
		}
		return fmt.Sprintf("body=%q", b)
	}
	segs := strings.Split(ch.CD.P, "-")
	for i, s := range segs {
		if len(s) > 8 {
			segs[i] = s[:8]
		}
	}
	return fmt.Sprintf("ch_type=%s ch_override=%s ch_i=%s ch_sc=%d ch_e=%d ch_r=%s p=%s",
		ch.Type, ch.OverrideCode, ch.CD.I, ch.CD.SC, ch.CD.E, ch.CD.R, strings.Join(segs, ","))
}

// bodySummary 输出请求体大小和消息条数，用于观察 418 是否与对话长度有关。
func bodySummary(body []byte) string {
	if body == nil {
		return ""
	}
	var req struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Messages == nil {
		return fmt.Sprintf(" body_bytes=%d", len(body))
	}
	return fmt.Sprintf(" body_bytes=%d msgs=%d", len(body), len(req.Messages))
}
