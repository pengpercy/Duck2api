package duckgo

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// 诊断日志的格式化工具。日志采用 key=value，便于 grep/awk 统计。

// fmtDur 以紧凑形式输出时长：850ms、3.2s、2m5s。
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

// shortEndpoint 去掉 scheme 与域名，只保留路径。
func shortEndpoint(u string) string {
	_, rest, ok := strings.Cut(u, "://")
	if !ok {
		return u
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[i:]
	}
	return "/"
}

// setCookieSummary 列出响应设置的 cookie 名（不含值），没有时返回空串。
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

// summarizeChallenge 把 418 返回体压缩成 key=value。已知类型：
//   - ERR_CHALLENGE：挑战应答被拒，cd.p 是一组哈希，观察到它在重建会话前后不变；
//   - ERR_BN_LIMIT：没有 cd，观察到它跟随会话，重建会话即可恢复。
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

// bodySummary 输出请求体大小与消息条数。
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
