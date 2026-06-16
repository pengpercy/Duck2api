package duckgo

import (
	"aurora/httpclient"
	"aurora/logger"
	duckgotypes "aurora/typings/duckgo"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

type browserTokenState struct {
	headers httpclient.AuroraHeaders
}

// PostConversationViaBrowser uses the browser only for token/challenge handling.
// The actual user chat request is always sent through Go so upstream SSE can pass through.
func (p *Provider) PostConversationViaBrowser(request duckgotypes.ApiRequest) (*http.Response, error) {
	p.browserMutex.Lock()
	defer p.browserMutex.Unlock()
	defer p.scheduleBrowserPageIdleCloseLocked()

	if p.browserToken.isValid() {
		resp, err := p.postConversationWithBrowserToken(request)
		if err == nil && resp.StatusCode != http.StatusTeapot {
			return resp, nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		p.browserToken = cachedItem[browserTokenState]{}
	}

	if err := p.refreshBrowserToken(); err != nil {
		return nil, err
	}
	return p.postConversationWithBrowserToken(request)
}

func (p *Provider) prewarmBrowserToken() {
	p.browserMutex.Lock()
	defer p.browserMutex.Unlock()
	defer p.scheduleBrowserPageIdleCloseLocked()
	if p.browserToken.isValid() {
		return
	}
	_ = p.refreshBrowserToken()
}

func (p *Provider) refreshBrowserToken() error {
	challenge, err := p.getBrowserChallengeScript()
	if err == nil {
		headers, buildErr := p.buildBrowserHeadersFromChallenge(challenge)
		if buildErr == nil {
			p.cacheDirectBrowserToken(headers)
			if p.browserToken.isValid() {
				return nil
			}
		} else {
			err = buildErr
		}
	}

	seedPrompt := getStringFromEnv("BROWSER_TOKEN_SEED_PROMPT", "ping")
	requestHeaders, seedErr := p.runBrowserSeed(seedPrompt)
	if seedErr != nil {
		if err != nil {
			return fmt.Errorf("browser challenge execution failed: %v; seed fallback failed: %w", err, seedErr)
		}
		return seedErr
	}
	p.cacheBrowserToken(requestHeaders)
	if !p.browserToken.isValid() {
		if err != nil {
			return fmt.Errorf("browser challenge execution failed: %v; browser token seed did not capture x-vqd header", err)
		}
		return errors.New("browser token seed did not capture x-vqd header")
	}
	return nil
}

func (p *Provider) postConversationWithBrowserToken(request duckgotypes.ApiRequest) (*http.Response, error) {
	bodyJSON, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}
	headers := cloneHeaders(p.browserToken.Value.headers)
	resp, err := p.client.Request(httpclient.POST, "https://duck.ai/duckchat/v1/chat", headers, nil, bytes.NewBuffer(bodyJSON))
	if resp != nil {
		p.updateScriptsFromHeader(resp.Header)
		p.scheduleBrowserTokenRefreshLocked()
	}
	return resp, err
}

func (p *Provider) cacheBrowserToken(headers network.Headers) {
	cached := httpclient.AuroraHeaders{}
	for _, key := range []string{
		"X-Vqd-Hash-1",
		"x-vqd-hash-1",
		"x-fe-version",
		"x-fe-signals",
		"x-ddg-journey-id",
		"User-Agent",
		"sec-ch-ua",
		"sec-ch-ua-mobile",
		"sec-ch-ua-platform",
	} {
		if value, ok := headerString(headers, key); ok && value != "" {
			cached.Set(key, value)
		}
	}
	cached.Set("accept", "text/event-stream")
	cached.Set("content-type", "application/json")
	cached.Set("origin", "https://duck.ai")
	cached.Set("referer", "https://duck.ai/")
	if cached["X-Vqd-Hash-1"] == "" && cached["x-vqd-hash-1"] == "" {
		return
	}
	p.browserToken = cachedItem[browserTokenState]{
		Value:    browserTokenState{headers: cached},
		ExpireAt: time.Now().Add(getDurationFromEnv("BROWSER_TOKEN_EXPIRATION_SECONDS", 30*time.Minute)),
	}
}

func (p *Provider) cacheDirectBrowserToken(headers httpclient.AuroraHeaders) {
	if headers == nil {
		return
	}
	token := headers["x-vqd-hash-1"]
	if token == "" {
		token = headers["X-Vqd-Hash-1"]
	}
	if token == "" {
		return
	}
	p.browserToken = cachedItem[browserTokenState]{
		Value:    browserTokenState{headers: cloneHeaders(headers)},
		ExpireAt: time.Now().Add(getDurationFromEnv("BROWSER_TOKEN_EXPIRATION_SECONDS", 30*time.Minute)),
	}
}

func cloneHeaders(headers httpclient.AuroraHeaders) httpclient.AuroraHeaders {
	clone := httpclient.AuroraHeaders{}
	for key, value := range headers {
		clone.Set(key, value)
	}
	return clone
}

func headerString(headers network.Headers, key string) (string, bool) {
	for currentKey, value := range headers {
		if !strings.EqualFold(currentKey, key) {
			continue
		}
		switch v := value.(type) {
		case string:
			return v, true
		default:
			return fmt.Sprint(v), true
		}
	}
	return "", false
}

func latestUserPrompt(request duckgotypes.ApiRequest) (string, error) {
	for i := len(request.Messages) - 1; i >= 0; i-- {
		switch msg := request.Messages[i].(type) {
		case duckgotypes.MessageUser:
			return messageContentText(msg.Content)
		case map[string]any:
			if msg["role"] == "user" {
				return messageContentText(msg["content"])
			}
		}
	}
	return "", errors.New("browser chat requires at least one user message")
}

func messageContentText(content any) (string, error) {
	switch v := content.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return "", errors.New("user message is empty")
		}
		return v, nil
	case []any:
		var b strings.Builder
		for _, part := range v {
			if m, ok := part.(map[string]any); ok && m["type"] == "text" {
				if text, ok := m["text"].(string); ok {
					b.WriteString(text)
				}
			}
		}
		if strings.TrimSpace(b.String()) == "" {
			return "", errors.New("browser chat currently supports text prompts only")
		}
		return b.String(), nil
	default:
		data, _ := json.Marshal(v)
		if len(data) > 0 && string(data) != "null" {
			return string(data), nil
		}
		return "", errors.New("unsupported user message content")
	}
}

func (p *Provider) ensureBrowserPage(ctx context.Context) error {
	if globalAllocatorCtx == nil {
		return errors.New("chromedp allocator not initialized")
	}

	if p.browserCtx != nil {
		if p.browserPageMaxAge > 0 && !p.browserCreatedAt.IsZero() && time.Since(p.browserCreatedAt) >= p.browserPageMaxAge {
			p.closeBrowserPageLocked("max age reached")
		} else if err := chromedp.Run(p.browserCtx, chromedp.Evaluate(`document.readyState`, nil)); err != nil {
			p.closeBrowserPageLocked("health check failed")
		} else {
			p.browserLastUsedAt = time.Now()
			return nil
		}
	}

	p.browserCtx, p.browserCancel = chromedp.NewContext(globalAllocatorCtx)
	if err := chromedp.Run(p.browserCtx,
		network.Enable(),
		chromedp.Navigate("https://duck.ai/"),
		chromedp.WaitVisible("body", chromedp.ByQuery),
		tryClickOnboardingAgree(),
		acceptOnboarding(),
	); err != nil {
		p.closeBrowserPageLocked("initialization failed")
		return err
	}
	p.browserCreatedAt = time.Now()
	p.browserLastUsedAt = p.browserCreatedAt
	p.attachBrowserListener()
	return nil
}

func (p *Provider) closeBrowserPageLocked(reason string) {
	if p.browserIdleTimer != nil {
		p.browserIdleTimer.Stop()
		p.browserIdleTimer = nil
	}
	if p.browserCancel != nil {
		logger.Infof("Closing Duck.ai browser page: %s", reason)
		p.browserCancel()
	}
	p.browserCtx = nil
	p.browserCancel = nil
	p.browserListenerAttached = false
	p.browserRequestHeadersCh = nil
	p.browserCreatedAt = time.Time{}
	p.browserLastUsedAt = time.Time{}
}

func (p *Provider) scheduleBrowserPageIdleCloseLocked() {
	if p.browserCtx == nil || p.browserPageIdleTTL <= 0 {
		return
	}
	p.browserLastUsedAt = time.Now()
	if p.browserIdleTimer != nil {
		p.browserIdleTimer.Stop()
	}
	p.browserIdleTimer = time.AfterFunc(p.browserPageIdleTTL, func() {
		p.browserMutex.Lock()
		defer p.browserMutex.Unlock()
		if p.browserCtx == nil || p.browserPageIdleTTL <= 0 {
			return
		}
		if time.Since(p.browserLastUsedAt) < p.browserPageIdleTTL {
			p.scheduleBrowserPageIdleCloseLocked()
			return
		}
		p.closeBrowserPageLocked("idle timeout")
	})
}

func (p *Provider) attachBrowserListener() {
	if p.browserCtx == nil || p.browserListenerAttached {
		return
	}
	p.browserListenerAttached = true
	chromedp.ListenTarget(p.browserCtx, func(ev any) {
		switch e := ev.(type) {
		case *network.EventRequestWillBeSent:
			if !strings.Contains(e.Request.URL, "/duckchat/v1/chat") {
				return
			}
			if ch := p.browserRequestHeadersCh; ch != nil {
				select {
				case ch <- e.Request.Headers:
				default:
				}
			}
		}
	})
}

func (p *Provider) buildBrowserHeadersFromChallenge(challenge string) (httpclient.AuroraHeaders, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := p.ensureBrowserPage(ctx); err != nil {
		return nil, err
	}

	challengeJSON, err := json.Marshal(challenge)
	if err != nil {
		return nil, err
	}

	js := fmt.Sprintf(`(async () => {
		const toBase64 = bytes => {
			let binary = '';
			for (const b of bytes) binary += String.fromCharCode(b);
			return btoa(binary);
		};
		const sha256Base64 = async input => {
			const bytes = new TextEncoder().encode(input);
			const digest = await crypto.subtle.digest('SHA-256', bytes);
			return toBase64(new Uint8Array(digest));
		};
		const challenge = %s;
		const raw = await eval(decodeURIComponent(
			atob(challenge).split('').map(c => '%%' + c.charCodeAt(0).toString(16).padStart(2, '0')).join('')
		));
		if (!raw || !Array.isArray(raw.client_hashes)) throw new Error('challenge payload malformed');
		raw.client_hashes[0] = navigator.userAgent;
		for (let i = 0; i < raw.client_hashes.length; i++) {
			raw.client_hashes[i] = await sha256Base64(String(raw.client_hashes[i]));
		}
		if (raw.meta && !raw.meta.origin) raw.meta.origin = location.origin;
		const token = btoa(JSON.stringify(raw));
		const headers = {
			'accept': 'text/event-stream',
			'content-type': 'application/json',
			'origin': location.origin,
			'referer': location.origin + '/',
			'user-agent': navigator.userAgent,
			'x-vqd-hash-1': token,
			'x-fe-signals': btoa(JSON.stringify({
				start: Date.now() - 1200,
				events: [
					{name: 'startNewChat_free', delta: 80},
					{name: 'recentChatsListImpression', delta: 180}
				],
				end: 260
			})),
			'x-fe-version': window.__DDG_BE_VERSION__ || window.__DDG_FE_CHAT_HASH__ || '',
			'x-ddg-journey-id': (crypto.randomUUID ? crypto.randomUUID() : String(Date.now())).replace(/-/g, '')
		};
		const meta = document.querySelector('meta[http-equiv="Content-Security-Policy"]');
		if (meta && !headers['x-fe-version']) {
			headers['x-fe-version'] = meta.getAttribute('content') || '';
		}
		return headers;
	})()`, string(challengeJSON))

	var result map[string]string
	if err := chromedp.Run(p.browserCtx, chromedp.Evaluate(js, &result, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithAwaitPromise(true)
	})); err != nil {
		return nil, err
	}
	headers := httpclient.AuroraHeaders{}
	for key, value := range result {
		headers.Set(key, value)
	}
	return headers, nil
}

func (p *Provider) getBrowserChallengeScript() (string, error) {
	p.tokenMutex.Lock()
	if p.jsCode.isValid() && p.jsCode.Value != "" {
		challenge := p.jsCode.Value
		p.tokenMutex.Unlock()
		return challenge, nil
	}
	p.tokenMutex.Unlock()

	challenge, err := p.getScripts(true)
	if err != nil {
		return "", err
	}

	p.tokenMutex.Lock()
	p.jsCode = cachedItem[string]{
		Value:    challenge,
		ExpireAt: time.Now().Add(p.scriptsCacheDuration),
	}
	p.tokenMutex.Unlock()
	return challenge, nil
}

func (p *Provider) scheduleBrowserTokenRefreshLocked() {
	if p.browserRefreshInFlight {
		return
	}
	p.browserRefreshInFlight = true

	go func() {
		defer func() {
			p.browserMutex.Lock()
			p.browserRefreshInFlight = false
			p.browserMutex.Unlock()
		}()

		p.browserMutex.Lock()
		defer p.browserMutex.Unlock()

		challenge, err := p.getBrowserChallengeScript()
		if err != nil {
			return
		}
		headers, err := p.buildBrowserHeadersFromChallenge(challenge)
		if err != nil {
			return
		}
		p.cacheDirectBrowserToken(headers)
	}()
}

func (p *Provider) runBrowserSeed(prompt string) (network.Headers, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := p.ensureBrowserPage(ctx); err != nil {
		return nil, err
	}

	runCtx := p.browserCtx
	requestHeaders := make(chan network.Headers, 1)
	p.browserRequestHeadersCh = requestHeaders
	defer func() { p.browserRequestHeadersCh = nil }()

	if err := chromedp.Run(runCtx,
		prepareNewChat(),
		sendPrompt(prompt),
	); err != nil {
		return nil, err
	}

	select {
	case headers := <-requestHeaders:
		return headers, nil
	case <-time.After(10 * time.Second):
		return nil, errors.New("timed out waiting for browser seed request")
	}
}

func prepareNewChat() chromedp.Action {
	return chromedp.Evaluate(`(async () => {
		const sleep = ms => new Promise(r => setTimeout(r, ms));
		const textOf = el => (el.innerText || el.textContent || el.value || '').trim();
		for (const el of [...document.querySelectorAll('button, [role="button"], a')]) {
			const label = ((el.getAttribute('aria-label') || '') + ' ' + textOf(el)).toLowerCase();
			if (label.includes('new chat') || label.includes('新聊天')) {
				el.click();
				await sleep(500);
				return true;
			}
		}
		return false;
	})()`, nil)
}

func tryClickOnboardingAgree() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var exists bool
		if err := chromedp.Evaluate(`!!document.querySelector("button[data-testid='DUCKAI_ONBOARDING_AGREE']")`, &exists).Do(ctx); err != nil {
			return nil
		}
		if !exists {
			return nil
		}
		if err := chromedp.Click("button[data-testid='DUCKAI_ONBOARDING_AGREE']", chromedp.ByQuery).Do(ctx); err != nil {
			return nil
		}
		return chromedp.Sleep(500 * time.Millisecond).Do(ctx)
	})
}

func acceptOnboarding() chromedp.Action {
	return chromedp.Evaluate(`(async () => {
		const sleep = ms => new Promise(r => setTimeout(r, ms));
		const textOf = el => (el.innerText || el.textContent || el.value || '').trim();
		for (const needle of ['agree', 'accept', 'i agree', 'get started', 'start chatting', 'continue']) {
			for (const el of [...document.querySelectorAll('button, [role="button"], a')]) {
				if (textOf(el).toLowerCase().includes(needle)) {
					el.click();
					await sleep(700);
					return true;
				}
			}
		}
		return false;
	})()`, nil)
}

func sendPrompt(prompt string) chromedp.Action {
	const inputSelector = `textarea, [contenteditable="true"], div[role="textbox"], input[type="text"]`
	submitJS := `(async () => {
		const sleep = ms => new Promise(r => setTimeout(r, ms));
		const textOf = el => (el.innerText || el.textContent || el.value || '').trim();
		for (let i = 0; i < 50; i++) {
			const submit = document.querySelector('button[type="submit"]:not([disabled])') ||
				[...document.querySelectorAll('button, [role="button"]')].find(el => {
					const label = ((el.getAttribute('aria-label') || '') + ' ' + textOf(el)).toLowerCase();
					const disabled = el.disabled || el.getAttribute('aria-disabled') === 'true';
					return !disabled && (label.includes('send') || label.includes('submit') || label.includes('ask'));
				});
			if (submit) {
				submit.click();
				return true;
			}
			await sleep(200);
		}
		throw new Error('enabled submit button not found');
	})()`

	return chromedp.Tasks{
		chromedp.WaitVisible(inputSelector, chromedp.ByQuery),
		chromedp.Click(inputSelector, chromedp.ByQuery),
		chromedp.SendKeys(inputSelector, prompt, chromedp.ByQuery),
		chromedp.Sleep(100 * time.Millisecond),
		chromedp.Evaluate(submitJS, nil),
	}
}
