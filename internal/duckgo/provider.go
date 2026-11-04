// Package duckgo 是 duck.ai 聊天接口的客户端。
//
// 每个上游请求都要带 x-vqd-hash-1：它是对服务端下发的 JS 挑战的应答。网页在一个
// 沙箱 iframe（#jsa）里执行挑战，挑战会探测一组浏览器环境特征，算出 client_hashes。
// 本包在 Goja 里模拟同样的环境求解（vqd.go + sandbox.js），并尽量让请求头、会话行为
// 与网页一致；上游返回 418/429 时自动退避重试，必要时重建 HTTP 会话。
//
// 文件划分：
//
//	provider.go   Provider、配置与对外 API
//	challenge.go  获取挑战、求解、token 缓存
//	retry.go      带 VQD 的请求与 418/429 重试
//	session.go    HTTP 会话的创建、重建与预热
//	headers.go    模拟网页的请求头
//	frontend.go   跟踪线上前端版本（x-fe-version、meta.stack）
//	vqd.go        Goja 挑战求解器；sandbox.js 是其浏览器环境
//	htmlfrag.go   按 HTML5 规则解析 innerHTML
//	diag.go       诊断日志格式化
package duckgo

import (
	"aurora/httpclient"
	"aurora/logger"
	duckgotypes "aurora/typings/duckgo"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	chatURL   = "https://duck.ai/duckchat/v1/chat"
	statusURL = "https://duck.ai/duckchat/v1/status"
)

// config 汇总所有可通过环境变量调整的参数。
type config struct {
	feVersion         string        // FE_VERSION，为空时使用线上解析值
	tokenExpiration   time.Duration // TOKEN_EXPIRATION_SECONDS
	tokenMaxUses      int           // TOKEN_MAX_USES，0 表示不限
	challengeTTL      time.Duration // SCRIPTS_CACHE_SECONDS：响应头挑战的有效期，0 表示每次走 /status
	maxAttempts       int           // UPSTREAM_MAX_RETRIES + 1
	sessionResetAfter int           // SESSION_RESET_AFTER_FAILURES，0 关闭
	idleResetAfter    time.Duration // IDLE_SESSION_RESET_SECONDS，0 关闭
}

func loadConfig() config {
	return config{
		feVersion: envString("FE_VERSION", ""),
		// token 默认一次一用：网页每发一条消息都会重新求解挑战
		tokenExpiration: envSeconds("TOKEN_EXPIRATION_SECONDS", time.Second),
		tokenMaxUses:    envInt("TOKEN_MAX_USES", 1),
		// 网页沿用上一个响应下发的挑战，实测页面加载 3 分钟后再用仍然有效
		challengeTTL:      envSeconds("SCRIPTS_CACHE_SECONDS", 300*time.Second),
		maxAttempts:       envInt("UPSTREAM_MAX_RETRIES", 6) + 1,
		sessionResetAfter: envInt("SESSION_RESET_AFTER_FAILURES", 2),
		idleResetAfter:    time.Duration(envInt("IDLE_SESSION_RESET_SECONDS", 1800)) * time.Second,
	}
}

// Provider 管理与 duck.ai 的 HTTP 会话、挑战 token 和重试，可被多个请求并发使用。
type Provider struct {
	cfg       config
	newClient func() httpclient.AuroraHttpClient
	proxyURL  string

	sessMu sync.Mutex
	sess   session // 受 sessMu 保护

	tokMu sync.Mutex
	tok   tokenState // 受 tokMu 保护

	reqSeq   uint64 // 请求序号（atomic），串联同一请求的多行日志
	inflight int64  // 进行中的上游请求数（atomic），含仍在读取响应流的
}

// NewProvider 创建 Provider 并预热首个会话。newClient 在会话需要重建时会被再次调用。
func NewProvider(newClient func() httpclient.AuroraHttpClient, proxyURL string) (*Provider, error) {
	p := &Provider{cfg: loadConfig(), newClient: newClient, proxyURL: proxyURL}
	p.sess = session{client: p.buildClient(), createdAt: time.Now()}
	p.warmSession("startup")
	p.sess.lastSuccess = time.Now()
	return p, nil
}

// PostConversation 发送一次聊天请求，返回上游的 SSE 响应。
func (p *Provider) PostConversation(request duckgotypes.ApiRequest) (*http.Response, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}
	return p.doWithRetry(httpclient.POST, chatURL, body, setChatHeaders)
}

// AuthenticatedRequest 以同样的 VQD、重试与会话逻辑访问其他需要挑战的接口（语音、听写等）。
func (p *Provider) AuthenticatedRequest(method, url, contentType, accept string, body []byte) (*http.Response, error) {
	return p.doWithRetry(httpclient.HttpMethod(method), url, body, func(h httpclient.AuroraHeaders) {
		if contentType != "" {
			h.Set("content-type", contentType)
		}
		if accept != "" {
			h.Set("accept", accept)
		}
		h.Set("x-ddg-journey-id", newJourneyID())
	})
}

// InvalidateCache 丢弃缓存的挑战与 token，在调用方最终收到 418 时使用。
func (p *Provider) InvalidateCache() {
	p.clearCache()
	logger.Warnf("All caches have been invalidated due to an API error.")
}

func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v >= 0 {
		return v
	}
	return def
}

func envSeconds(key string, def time.Duration) time.Duration {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v >= 0 {
		return time.Duration(v) * time.Second
	}
	return def
}
