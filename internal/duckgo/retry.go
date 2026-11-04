package duckgo

import (
	"aurora/httpclient"
	"aurora/logger"
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// sleep 可在测试中替换，避免真实等待。
var sleep = time.Sleep

// upstreamCall 是一次 doWithRetry 调用的状态。
type upstreamCall struct {
	id       uint64
	start    time.Time
	failures int  // 连续失败次数
	resets   int  // 本次调用触发的会话重建次数
	bnLimit  bool // 上一次返回了 ERR_BN_LIMIT
	lastErr  error
	release  func() // 释放 inflight 计数，只生效一次
}

// doWithRetry 发送带 VQD 的请求。418/429/网络错误会退避重试，对调用方透明：
//   - 连续失败 sessionResetAfter 次、或遇到 ERR_BN_LIMIT 时，先重建会话再试；
//   - 用尽次数后原样返回最后一个上游响应，交给调用方的错误处理。
//
// 每次尝试输出一行 "[upstream] req=N attempt=i/n ..." 日志，字段供事后统计 418 用，修改时注意兼容。
func (p *Provider) doWithRetry(method httpclient.HttpMethod, url string, body []byte, decorate func(httpclient.AuroraHeaders)) (*http.Response, error) {
	c := p.beginCall(method, url, body)
	defer func() { c.release() }() // 响应交给调用方后 trackBody 会把它换成空操作

	for attempt := 0; attempt < p.cfg.maxAttempts; attempt++ {
		p.resetBeforeAttempt(c)
		resp, done := p.attempt(c, attempt, method, url, body, decorate)
		if done {
			return resp, nil
		}
	}
	logger.Warnf("[upstream] req=%d failed attempts=%d resets=%d elapsed=%s err=%v",
		c.id, p.cfg.maxAttempts, c.resets, fmtDur(time.Since(c.start)), c.lastErr)
	return nil, c.lastErr
}

func (p *Provider) beginCall(method httpclient.HttpMethod, url string, body []byte) *upstreamCall {
	c := &upstreamCall{id: atomic.AddUint64(&p.reqSeq, 1), start: time.Now()}
	inflight := atomic.AddInt64(&p.inflight, 1)
	released := false
	c.release = func() {
		if !released {
			released = true
			atomic.AddInt64(&p.inflight, -1)
		}
	}
	idle := p.idleSinceSuccess()
	logger.Infof("[upstream] req=%d start %s %s idle_before=%s inflight=%d%s",
		c.id, method, shortEndpoint(url), fmtDur(idle), inflight, bodySummary(body))
	if p.resetIfIdle(c.id, idle) {
		c.resets++
	}
	return c
}

func (p *Provider) resetBeforeAttempt(c *upstreamCall) {
	var reason string
	switch {
	case c.bnLimit:
		// ERR_BN_LIMIT 跟随会话，原会话上重试基本无效
		c.bnLimit = false
		reason = fmt.Sprintf("req=%d ERR_BN_LIMIT", c.id)
	case c.failures > 0 && p.cfg.sessionResetAfter > 0 && c.failures%p.cfg.sessionResetAfter == 0:
		reason = fmt.Sprintf("req=%d after %d consecutive failures", c.id, c.failures)
	default:
		return
	}
	if p.resetSession(reason) {
		c.resets++
	}
}

// attempt 执行一次尝试。done 为 true 时 resp 即最终结果；否则已完成退避，应继续下一次。
func (p *Provider) attempt(c *upstreamCall, attempt int, method httpclient.HttpMethod, url string, body []byte, decorate func(httpclient.AuroraHeaders)) (resp *http.Response, done bool) {
	tag := fmt.Sprintf("req=%d attempt=%d/%d", c.id, attempt+1, p.cfg.maxAttempts)

	tok, err := p.acquireToken()
	if err != nil {
		c.lastErr = fmt.Errorf("failed to get VQD token: %w", err)
		c.failures++
		p.clearCache()
		delay := retryDelay(attempt, 0)
		logger.Warnf("[upstream] %s token_error elapsed=%s next_delay=%s err=%v", tag, fmtDur(time.Since(c.start)), fmtDur(delay), err)
		sleep(delay)
		return nil, false
	}

	header := apiHeaders()
	decorate(header)
	header.Set("x-vqd-hash-1", tok.value)
	header.Set("x-fe-signals", makeFESignals())
	header.Set("x-fe-version", p.feVersion())

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	client, sess := p.useClient()
	attemptStart := time.Now()
	resp, err = client.Request(method, url, header, nil, reader)
	ctx := fmt.Sprintf("%s %s %s %s inflight=%d", tag, sess, tok, fmtElapsed(attemptStart, c.start), atomic.LoadInt64(&p.inflight))

	if err != nil {
		c.lastErr = err
		c.failures++
		delay := retryDelay(attempt, 0)
		logger.Warnf("[upstream] %s network_error next_delay=%s err=%v", ctx, fmtDur(delay), err)
		p.markClientBroken(sess.gen, c.id)
		sleep(delay)
		return nil, false
	}

	newChallenge := resp.Header.Get("x-vqd-hash-1") != ""
	p.cacheChallengeFromHeader(resp.Header)

	if !isRetryableStatus(resp.StatusCode) {
		if resp.StatusCode < 400 {
			p.markSuccess()
			logger.Infof("[upstream] %s status=%d ok resets=%d new_challenge=%t%s", ctx, resp.StatusCode, c.resets, newChallenge, setCookieSummary(resp.Header))
		} else {
			logger.Warnf("[upstream] %s status=%d not_retryable resets=%d%s", ctx, resp.StatusCode, c.resets, setCookieSummary(resp.Header))
		}
		p.trackBody(resp, c)
		return resp, true
	}

	// 418/429：读出返回体用于日志与判断类型
	rejected, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	c.lastErr = fmt.Errorf("Duck.ai rejected request (%d): %s", resp.StatusCode, rejected)
	logger.Debugf("[upstream] %s raw_body=%s", tag, rejected)

	if attempt == p.cfg.maxAttempts-1 {
		logger.Warnf("[upstream] %s status=%d gave_up resets=%d %s%s", ctx, resp.StatusCode, c.resets, summarizeChallenge(rejected), setCookieSummary(resp.Header))
		resp.Body = io.NopCloser(bytes.NewReader(rejected))
		p.trackBody(resp, c)
		return resp, true
	}

	retryAfter := resp.Header.Get("Retry-After")
	delay := retryDelay(attempt, parseRetryAfter(retryAfter))
	logger.Warnf("[upstream] %s status=%d %s retry_after=%q next_delay=%s new_challenge=%t%s",
		ctx, resp.StatusCode, summarizeChallenge(rejected), retryAfter, fmtDur(delay), newChallenge, setCookieSummary(resp.Header))
	c.failures++
	c.bnLimit = bytes.Contains(rejected, []byte("ERR_BN_LIMIT"))
	p.clearCache()
	sleep(delay)
	return nil, false
}

// isRetryableStatus 判断是否为可通过重试恢复的风控/限流状态。
func isRetryableStatus(code int) bool {
	return code == http.StatusTeapot || code == http.StatusTooManyRequests
}

// retryDelay 返回第 attempt 次失败后的等待时间：优先 Retry-After（上限 10s），
// 否则 1s、2s、4s、6s… 加最多 500ms 抖动。实测 418 的风控窗口一般持续数秒，过快重试意义不大。
func retryDelay(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return min(retryAfter, 10*time.Second)
	}
	delay := 6 * time.Second
	if attempt < 3 {
		delay = time.Duration(1<<attempt) * time.Second
	}
	return delay + time.Duration(rand.Intn(500))*time.Millisecond
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t)
	}
	return 0
}
