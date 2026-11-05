package duckgo

import (
	"aurora/httpclient"
	"aurora/logger"
	duckgotypes "aurora/typings/duckgo"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// sessionResetCooldown 内只重建一次，避免并发请求同时触发重建。
const sessionResetCooldown = 5 * time.Second

// session 是当前 HTTP 客户端（连接池 + TLS 指纹）及其统计，受 Provider.sessMu 保护。
type session struct {
	client      httpclient.AuroraHttpClient
	gen         uint64 // 代际，每次重建 +1
	createdAt   time.Time
	reqs        int // 在该客户端上发出的上游 API 请求数
	lastReset   time.Time
	lastSuccess time.Time
	// publicKey 是 durableStream 的 RSA 公钥。网页在整个页面生命周期内只生成一次，
	// 跨会话复用；每条消息换一把新钥匙会在沿用挑战时触发 ERR_BN_LIMIT。
	publicKey duckgotypes.PublicKey
}

// sessionInfo 是某次请求所用会话的快照，用于日志。
type sessionInfo struct {
	gen  uint64
	age  time.Duration
	reqs int // 含本次
}

func (s sessionInfo) String() string {
	return fmt.Sprintf("gen=%d sess_age=%s sess_reqs=%d", s.gen, fmtDur(s.age), s.reqs)
}

func (p *Provider) buildClient() httpclient.AuroraHttpClient {
	client := p.newClient()
	if p.proxyURL != "" {
		client.SetProxy(p.proxyURL)
	}
	return client
}

func (p *Provider) newSession(gen uint64) session {
	return session{client: p.buildClient(), gen: gen, createdAt: time.Now(), publicKey: newPublicKey()}
}

// sessionPublicKey 返回当前会话的 durableStream 公钥。
func (p *Provider) sessionPublicKey() duckgotypes.PublicKey {
	p.sessMu.Lock()
	defer p.sessMu.Unlock()
	return p.sess.publicKey
}

// newPublicKey 生成与网页 crypto.subtle.exportKey("jwk") 相同格式的 RSA-OAEP 公钥。
// 私钥用不到：只有恢复中断的流才需要解密。
func newPublicKey() duckgotypes.PublicKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return duckgotypes.PublicKey{}
	}
	return duckgotypes.PublicKey{
		Alg:    "RSA-OAEP-256",
		E:      base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
		Ext:    true,
		KeyOps: []string{"encrypt"},
		Kty:    "RSA",
		N:      base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
		Use:    "enc",
	}
}

func (p *Provider) currentClient() (httpclient.AuroraHttpClient, uint64) {
	p.sessMu.Lock()
	defer p.sessMu.Unlock()
	return p.sess.client, p.sess.gen
}

// useClient 返回当前客户端，并把本次请求计入会话请求数。
func (p *Provider) useClient() (httpclient.AuroraHttpClient, sessionInfo) {
	p.sessMu.Lock()
	defer p.sessMu.Unlock()
	p.sess.reqs++
	info := sessionInfo{gen: p.sess.gen, reqs: p.sess.reqs}
	if !p.sess.createdAt.IsZero() {
		info.age = time.Since(p.sess.createdAt)
	}
	return p.sess.client, info
}

func (p *Provider) markSuccess() {
	p.sessMu.Lock()
	p.sess.lastSuccess = time.Now()
	p.sessMu.Unlock()
}

// idleSinceSuccess 返回距最近一次上游成功的时长，从未成功时为 0。
func (p *Provider) idleSinceSuccess() time.Duration {
	p.sessMu.Lock()
	defer p.sessMu.Unlock()
	if p.sess.lastSuccess.IsZero() {
		return 0
	}
	return time.Since(p.sess.lastSuccess)
}

// resetIfIdle 在空闲超过 idleResetAfter 时重建会话，丢弃可能已被服务端关闭的连接。
func (p *Provider) resetIfIdle(reqID uint64, idle time.Duration) bool {
	if p.cfg.idleResetAfter <= 0 || p.newClient == nil {
		return false
	}
	p.sessMu.Lock()
	isIdle := !p.sess.lastSuccess.IsZero() && time.Since(p.sess.lastSuccess) > p.cfg.idleResetAfter
	if isIdle {
		p.sess.lastSuccess = time.Now() // 占位，防止并发请求重复触发
	}
	p.sessMu.Unlock()
	if !isIdle {
		return false
	}
	return p.resetSession(fmt.Sprintf("req=%d idle %s > %s", reqID, fmtDur(idle), p.cfg.idleResetAfter))
}

// markClientBroken 在网络错误后重建会话；gen 已过期说明别的请求已经重建过。
func (p *Provider) markClientBroken(gen, reqID uint64) {
	p.sessMu.Lock()
	stale := gen != p.sess.gen
	p.sessMu.Unlock()
	if !stale {
		p.resetSession(fmt.Sprintf("req=%d network error on gen=%d", reqID, gen))
	}
}

// resetSession 换一个新的 HTTP 客户端（新连接、新 TLS 会话）、清空挑战缓存并重新预热，
// 效果相当于重启进程。冷却期内直接跳过。返回是否真的重建了。
func (p *Provider) resetSession(reason string) bool {
	if p.newClient == nil {
		p.clearCache()
		return false
	}
	p.sessMu.Lock()
	if since := time.Since(p.sess.lastReset); since < sessionResetCooldown {
		gen := p.sess.gen
		p.sessMu.Unlock()
		logger.Infof("[session] reset skipped (%s): gen=%d was rebuilt %s ago, cooldown %s", reason, gen, fmtDur(since), sessionResetCooldown)
		return false
	}
	old := p.sess
	now := time.Now()
	p.sess = p.newSession(old.gen + 1)
	p.sess.lastReset, p.sess.lastSuccess = now, old.lastSuccess
	p.sessMu.Unlock()

	p.clearCache()
	logger.Warnf("[session] reset gen=%d->%d reason=%q old_age=%s old_reqs=%d", old.gen, old.gen+1, reason, fmtDur(time.Since(old.createdAt)), old.reqs)
	p.warmSession(reason)
	return true
}

// warmSession 像浏览器打开页面一样请求首页和 /chat，并从 /chat 页面更新前端版本信息。
func (p *Provider) warmSession(reason string) {
	client, gen := p.currentClient()
	header := navigationHeaders()
	start := time.Now()
	parts := make([]string, 0, 3)
	for _, u := range []string{"https://duck.ai/", "https://duck.ai/chat?ia=chat&duckai=1"} {
		t := time.Now()
		resp, err := client.Request(httpclient.GET, u, header, nil, nil)
		if err != nil || resp == nil {
			parts = append(parts, fmt.Sprintf("%s=err(%v)", shortEndpoint(u), err))
			continue
		}
		page, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		parts = append(parts, fmt.Sprintf("%s=%d/%dB/%s%s", shortEndpoint(u), resp.StatusCode, len(page), fmtDur(time.Since(t)), setCookieSummary(resp.Header)))
		if resp.StatusCode == http.StatusOK && strings.Contains(u, "/chat") {
			if note := updateFrontendFromHTML(client, page); note != "" {
				parts = append(parts, note)
			}
		}
	}
	logger.Infof("[session] warm gen=%d reason=%q total=%s %s", gen, reason, fmtDur(time.Since(start)), strings.Join(parts, " "))
}

// trackBody 让响应体关闭时才释放 inflight 计数（流式响应可能持续很久）。
func (p *Provider) trackBody(resp *http.Response, c *upstreamCall) {
	if resp.Body == nil {
		c.release()
		return
	}
	start := time.Now()
	release := c.release
	c.release = func() {} // 所有权交给响应体
	resp.Body = &trackedBody{ReadCloser: resp.Body, onClose: func() {
		release()
		logger.Debugf("[upstream] req=%d body closed after %s inflight=%d", c.id, fmtDur(time.Since(start)), atomic.LoadInt64(&p.inflight))
	}}
}

type trackedBody struct {
	io.ReadCloser
	once    sync.Once
	onClose func()
}

func (b *trackedBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.onClose)
	return err
}
