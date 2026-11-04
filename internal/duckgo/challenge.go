package duckgo

import (
	"aurora/httpclient"
	"aurora/logger"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// generateVQDToken 可在测试中替换。
var generateVQDToken = solveChallenge

// cachedItem 是带过期时间的缓存值，ExpireAt 为零表示未缓存。
type cachedItem[T any] struct {
	Value    T
	ExpireAt time.Time
}

func (c *cachedItem[T]) isValid() bool {
	return !c.ExpireAt.IsZero() && time.Now().Before(c.ExpireAt)
}

// tokenState 是挑战与 token 的缓存，受 Provider.tokMu 保护。
type tokenState struct {
	challenge cachedItem[string] // 解码后的挑战 JS
	token     cachedItem[string] // 求解得到的 x-vqd-hash-1
	issuedAt  time.Time
	uses      int
}

// tokenInfo 描述一次取 token 的结果，用于诊断日志。
type tokenInfo struct {
	value string
	fresh bool
	age   time.Duration
	uses  int // 含本次
}

func (t tokenInfo) String() string {
	src := "cached"
	if t.fresh {
		src = "fresh"
	}
	return fmt.Sprintf("token=%s token_age=%s token_uses=%d", src, fmtDur(t.age), t.uses)
}

// acquireToken 返回可用的 token：缓存未过期且未用满时复用，否则取挑战并求解。
func (p *Provider) acquireToken() (tokenInfo, error) {
	p.tokMu.Lock()
	defer p.tokMu.Unlock()

	t := &p.tok
	if t.token.isValid() && (p.cfg.tokenMaxUses <= 0 || t.uses < p.cfg.tokenMaxUses) {
		t.uses++
		return tokenInfo{value: t.token.Value, age: time.Since(t.issuedAt), uses: t.uses}, nil
	}

	logger.Infof("Refreshing token...")
	token, err := p.refreshToken()
	if err != nil {
		return tokenInfo{}, err
	}
	t.uses = 1
	return tokenInfo{value: token, fresh: true, uses: 1}, nil
}

// refreshToken 取挑战（优先用缓存）、求解并缓存结果。调用方须持有 tokMu。
func (p *Provider) refreshToken() (string, error) {
	t := &p.tok
	js := t.challenge.Value
	if t.challenge.isValid() {
		logger.Infof("Using cached challenge (expires in %s)", fmtDur(time.Until(t.challenge.ExpireAt)))
	} else {
		var err error
		if js, err = p.fetchChallenge(); err != nil {
			return "", fmt.Errorf("failed to get scripts for token generation: %w", err)
		}
		t.challenge = cachedItem[string]{Value: js, ExpireAt: time.Now().Add(p.cfg.scriptsCacheDuration)}
	}

	start := time.Now()
	token, err := generateVQDToken(js)
	logger.Debugf("Solved VQD challenge in %s", fmtDur(time.Since(start)))
	if err != nil {
		return "", fmt.Errorf("goja VQD generation failed: %w", err)
	}
	t.token = cachedItem[string]{Value: token, ExpireAt: time.Now().Add(p.cfg.tokenExpiration)}
	t.issuedAt = time.Now()
	t.uses = 0
	return token, nil
}

// fetchChallenge 请求 /status，返回响应头 x-vqd-hash-1 中的挑战 JS。
func (p *Provider) fetchChallenge() (string, error) {
	client, gen := p.currentClient()
	start := time.Now()
	resp, err := client.Request(httpclient.GET, statusURL, statusHeaders(), nil, nil)
	if err != nil {
		logger.Warnf("[status] gen=%d error after %s: %v", gen, fmtDur(time.Since(start)), err)
		return "", err
	}
	defer resp.Body.Close()

	encoded := resp.Header.Get("x-vqd-hash-1")
	logger.Infof("[status] gen=%d code=%d dur=%s challenge=%t%s", gen, resp.StatusCode, fmtDur(time.Since(start)),
		encoded != "", setCookieSummary(resp.Header))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status request failed with code: %d", resp.StatusCode)
	}
	if encoded == "" {
		return "", errors.New("x-vqd-hash-1 header not found in status response")
	}
	js, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("failed to decode x-vqd-hash-1: %w", err)
	}
	return string(js), nil
}

// cacheChallengeFromHeader 保存响应头里下发的下一个挑战；
// 只有 SCRIPTS_CACHE_SECONDS > 0 时它才会在过期前被使用。
func (p *Provider) cacheChallengeFromHeader(h http.Header) {
	encoded := h.Get("x-vqd-hash-1")
	if encoded == "" {
		return
	}
	js, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		logger.Errorf("Error decoding new script from header: %v", err)
		return
	}
	p.tokMu.Lock()
	p.tok.challenge = cachedItem[string]{Value: string(js), ExpireAt: time.Now().Add(p.cfg.scriptsCacheDuration)}
	p.tokMu.Unlock()
}

func (p *Provider) clearCache() {
	p.tokMu.Lock()
	p.tok = tokenState{}
	p.tokMu.Unlock()
}
