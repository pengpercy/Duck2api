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
	challenge cachedItem[string] // 上一个响应下发的下一个挑战，只能用一次
	token     cachedItem[string] // 求解得到的 x-vqd-hash-1
	issuedAt  time.Time
	uses      int
}

// tokenInfo 描述一次取 token 的结果，用于诊断日志。
type tokenInfo struct {
	value     string
	fresh     bool
	challenge string // 本次求解的挑战来源：chained（响应头下发）或 status
	age       time.Duration
	uses      int // 含本次
}

func (t tokenInfo) String() string {
	if !t.fresh {
		return fmt.Sprintf("token=cached token_age=%s token_uses=%d", fmtDur(t.age), t.uses)
	}
	return fmt.Sprintf("token=fresh challenge=%s token_age=0s token_uses=1", t.challenge)
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

	token, source, err := p.refreshToken()
	if err != nil {
		return tokenInfo{}, err
	}
	t.uses = 1
	return tokenInfo{value: token, fresh: true, challenge: source, uses: 1}, nil
}

// refreshToken 取一个挑战并求解，返回 token 与挑战来源。调用方须持有 tokMu。
//
// 与网页一致：优先用上一个响应头下发的挑战，用过即丢弃；没有（首个请求、并发请求、
// 418 之后或已过期）时才请求 /status。
func (p *Provider) refreshToken() (token, source string, err error) {
	t := &p.tok
	js, source := t.challenge.Value, "chained"
	if t.challenge.isValid() {
		t.challenge = cachedItem[string]{}
	} else {
		source = "status"
		if js, err = p.fetchChallenge(); err != nil {
			return "", "", fmt.Errorf("failed to get scripts for token generation: %w", err)
		}
	}

	start := time.Now()
	token, err = generateVQDToken(js)
	logger.Debugf("Solved %s challenge in %s", source, fmtDur(time.Since(start)))
	if err != nil {
		return "", "", fmt.Errorf("goja VQD generation failed: %w", err)
	}
	t.token = cachedItem[string]{Value: token, ExpireAt: time.Now().Add(p.cfg.tokenExpiration)}
	t.issuedAt = time.Now()
	t.uses = 0
	return token, source, nil
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

// cacheChallengeFromHeader 保存响应头里下发的下一个挑战，供下一个请求使用。
// SCRIPTS_CACHE_SECONDS 为 0 时立即过期，即每次都走 /status。
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
	p.tok.challenge = cachedItem[string]{Value: string(js), ExpireAt: time.Now().Add(p.cfg.challengeTTL)}
	p.tokMu.Unlock()
}

func (p *Provider) clearCache() {
	p.tokMu.Lock()
	p.tok = tokenState{}
	p.tokMu.Unlock()
}
