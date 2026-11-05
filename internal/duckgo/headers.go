package duckgo

import (
	"aurora/httpclient"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// 请求头尽量与网页一致（2026-11 用 Chrome 抓包核对）。浏览器不会给 duck.ai 带任何
// duckduckgo.com 的偏好 cookie，所以这里也不发 cookie。

// baseHeaders 是所有请求共有的浏览器头。UA 同时用于挑战求解（client_hashes 里包含
// navigator.userAgent），两边必须一致。
func baseHeaders() httpclient.AuroraHeaders {
	ua := vqdUserAgent()
	h := make(httpclient.AuroraHeaders)
	h.Set("accept-language", "en-US,en;q=0.9")
	h.Set("user-agent", ua)
	if major := chromeMajorVersion(ua); major != "" {
		h.Set("sec-ch-ua", `"Chromium";v="`+major+`", "Google Chrome";v="`+major+`", "Not.A/Brand";v="99"`)
		h.Set("sec-ch-ua-mobile", "?0")
		h.Set("sec-ch-ua-platform", `"Windows"`)
	}
	return h
}

// apiHeaders 用于页面内发起的同源 fetch。
func apiHeaders() httpclient.AuroraHeaders {
	h := baseHeaders()
	h.Set("content-type", "application/json")
	h.Set("origin", "https://duck.ai")
	h.Set("referer", "https://duck.ai/")
	h.Set("sec-fetch-dest", "empty")
	h.Set("sec-fetch-mode", "cors")
	h.Set("sec-fetch-site", "same-origin")
	return h
}

func statusHeaders() httpclient.AuroraHeaders {
	h := apiHeaders()
	delete(h, "content-type")
	h.Set("accept", "*/*")
	h.Set("cache-control", "no-store")
	h.Set("pragma", "no-cache")
	h.Set("x-vqd-accept", "1")
	return h
}

// navigationHeaders 用于地址栏打开页面（顶层导航不带 origin/referer）。
func navigationHeaders() httpclient.AuroraHeaders {
	h := baseHeaders()
	h.Set("accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	h.Set("sec-fetch-dest", "document")
	h.Set("sec-fetch-mode", "navigate")
	h.Set("sec-fetch-site", "none")
	h.Set("sec-fetch-user", "?1")
	h.Set("upgrade-insecure-requests", "1")
	return h
}

// scriptHeaders 用于页面加载同源 <script>。
func scriptHeaders() httpclient.AuroraHeaders {
	h := baseHeaders()
	h.Set("accept", "*/*")
	h.Set("referer", "https://duck.ai/")
	h.Set("sec-fetch-dest", "script")
	h.Set("sec-fetch-mode", "no-cors")
	h.Set("sec-fetch-site", "same-origin")
	return h
}

// setChatHeaders 补充网页每次发消息都会带的头。
func setChatHeaders(h httpclient.AuroraHeaders) {
	h.Set("accept", "text/event-stream")
	h.Set("x-duckai-continuation", "resubmit-context")
	h.Set("x-duckai-consent-purposes", "user-location,image-generation,web-search")
	h.Set("x-duckai-tool-approval", "web-search")
	h.Set("x-duckai-response-id", newUUID())
	h.Set("x-ddg-journey-id", newJourneyID())
}

// nextFESignals 生成 x-fe-signals。网页里它是一条连续的时间线：start 是上一次发消息
// （首条则为页面加载）的时间，end 是距今的毫秒数，开新对话时 events 带 startNewChat_free。
// 沿用挑战时服务端能把相邻请求串起来，此前随机生成的时间线会很快触发 ERR_BN_LIMIT。
func (p *Provider) nextFESignals() string {
	p.sessMu.Lock()
	start := p.sess.lastSend
	now := time.Now()
	p.sess.lastSend = now
	p.sessMu.Unlock()

	end := now.Sub(start).Milliseconds()
	// 本服务的每个请求都是新对话（新的 conversationId），新建对话略早于发送
	delta := end - int64(800+rand.Intn(1500))
	if delta < 0 {
		delta = 0
	}
	data, _ := json.Marshal(map[string]any{
		"start":  start.UnixMilli(),
		"events": []map[string]any{{"name": "startNewChat_free", "delta": delta}},
		"end":    end,
	})
	return base64.StdEncoding.EncodeToString(data)
}

// feVersion 返回 x-fe-version：FE_VERSION > 线上解析值 > 内置的已知版本。
func (p *Provider) feVersion() string {
	if p.cfg.feVersion != "" {
		return p.cfg.feVersion
	}
	if f := currentFrontend(); f.beVersion != "" && f.feHash != "" {
		return f.beVersion + "-" + f.feHash
	}
	return defaultFEVersion
}

// chromeMajorVersion 从 UA 中提取 Chrome 主版本号，非 Chrome UA 返回空串。
func chromeMajorVersion(ua string) string {
	_, v, ok := strings.Cut(ua, "Chrome/")
	if !ok {
		return ""
	}
	major, _, _ := strings.Cut(v, ".")
	return major
}

func newUUID() string {
	var b [16]byte
	for i := range b {
		b[i] = byte(rand.Intn(256))
	}
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func newJourneyID() string {
	return fmt.Sprintf("%016x%016x", rand.Uint64(), rand.Uint64())
}
