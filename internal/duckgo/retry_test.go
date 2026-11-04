package duckgo

import (
	"aurora/httpclient"
	duckgotypes "aurora/typings/duckgo"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClient 对 status 接口返回挑战，对 chat 接口依次返回 statuses 中的状态码。
type fakeClient struct {
	statuses []int
	bodies   []string // 可选：与 statuses 对应的 chat 响应体
	calls    *int32
}

func (f *fakeClient) Request(method httpclient.HttpMethod, url string, headers httpclient.AuroraHeaders, cookies []*http.Cookie, body io.Reader) (*http.Response, error) {
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}
	if strings.HasSuffix(url, "/status") {
		resp.Header.Set("x-vqd-hash-1", base64.StdEncoding.EncodeToString([]byte("challenge")))
		return resp, nil
	}
	if !strings.HasSuffix(url, "/chat") {
		return resp, nil
	}
	i := int(atomic.AddInt32(f.calls, 1)) - 1
	if i < len(f.statuses) {
		resp.StatusCode = f.statuses[i]
	}
	if i < len(f.bodies) {
		resp.Body = io.NopCloser(strings.NewReader(f.bodies[i]))
	}
	return resp, nil
}

func (f *fakeClient) SetProxy(string) error { return nil }

func newTestProvider(t *testing.T, statuses []int, maxAttempts int) (*Provider, *int32, *int32) {
	t.Helper()
	originalGenerate := generateVQDToken
	t.Cleanup(func() { generateVQDToken = originalGenerate })
	generateVQDToken = func(string) (string, error) { return "token", nil }
	originalSleep := sleep
	t.Cleanup(func() { sleep = originalSleep })
	sleep = func(time.Duration) {}

	var chatCalls, created int32
	newClient := func() httpclient.AuroraHttpClient {
		atomic.AddInt32(&created, 1)
		return &fakeClient{statuses: statuses, calls: &chatCalls}
	}
	p := &Provider{
		cfg:       config{maxAttempts: maxAttempts, sessionResetAfter: 2},
		newClient: newClient,
	}
	p.sess = session{client: newClient(), createdAt: time.Now()}
	return p, &chatCalls, &created
}

func TestPostConversationRetriesTeapotAndRateLimit(t *testing.T) {
	p, chatCalls, created := newTestProvider(t, []int{http.StatusTeapot, http.StatusTooManyRequests, http.StatusOK}, 5)

	resp, err := p.PostConversation(duckgotypes.ApiRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(chatCalls); got != 3 {
		t.Fatalf("chat calls = %d, want 3", got)
	}
	// 连续两次失败后应重建一次客户端
	if got := atomic.LoadInt32(created); got != 2 {
		t.Fatalf("clients created = %d, want 2", got)
	}
}

func TestPostConversationReturnsLastUpstreamResponse(t *testing.T) {
	p, chatCalls, _ := newTestProvider(t, []int{429, 429, 429}, 3)

	resp, err := p.PostConversation(duckgotypes.ApiRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	if got := atomic.LoadInt32(chatCalls); got != 3 {
		t.Fatalf("chat calls = %d, want 3", got)
	}
}

func TestIdleSessionIsRebuiltBeforeRequest(t *testing.T) {
	p, _, created := newTestProvider(t, []int{http.StatusOK, http.StatusOK}, 3)
	p.cfg.idleResetAfter = time.Minute
	p.sess.lastSuccess = time.Now().Add(-2 * time.Minute)

	if _, err := p.PostConversation(duckgotypes.ApiRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(created); got != 2 {
		t.Fatalf("clients created = %d, want 2 (idle rebuild)", got)
	}

	// 刚成功过，不应再次重建
	if _, err := p.PostConversation(duckgotypes.ApiRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(created); got != 2 {
		t.Fatalf("clients created = %d, want 2 (no rebuild when active)", got)
	}
}

func TestIdleResetDoesNotSwallowLaterFailureReset(t *testing.T) {
	// 空闲重建后紧接着的失败重置会被冷却期跳过；确认请求仍能在后续尝试成功，且不会多建客户端
	p, chatCalls, created := newTestProvider(t, []int{http.StatusTeapot, http.StatusTeapot, http.StatusOK}, 5)
	p.cfg.sessionResetAfter = 1
	p.cfg.idleResetAfter = time.Minute
	p.sess.lastSuccess = time.Now().Add(-2 * time.Minute)

	resp, err := p.PostConversation(duckgotypes.ApiRequest{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := atomic.LoadInt32(chatCalls); got != 3 {
		t.Fatalf("chat calls = %d, want 3", got)
	}
	if got := atomic.LoadInt32(created); got != 2 {
		t.Fatalf("clients created = %d, want 2 (idle rebuild only, failure resets in cooldown)", got)
	}
	if got := atomic.LoadInt64(&p.inflight); got != 0 {
		t.Fatalf("inflight = %d after body close, want 0", got)
	}
}

func TestSummarizeChallenge(t *testing.T) {
	body := `{"action":"error","status":418,"type":"ERR_CHALLENGE","overrideCode":"d78d","cd":{"e":0,"i":"3","p":"036ef386126084b6275ff34fabda050d-57c08aa2aaaaaaaaaaaaaaaaaaaaaaaa","r":"usw","sc":1}}`
	got := summarizeChallenge([]byte(body))
	want := "ch_type=ERR_CHALLENGE ch_override=d78d ch_i=3 ch_sc=1 ch_e=0 ch_r=usw p=036ef386,57c08aa2"
	if got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}

func TestBNLimitResetsSessionImmediately(t *testing.T) {
	p, chatCalls, created := newTestProvider(t, []int{http.StatusTeapot, http.StatusOK}, 5)
	p.cfg.sessionResetAfter = 4
	bn := `{"action":"error","status":418,"type":"ERR_BN_LIMIT","overrideCode":"090c"}`
	bodies := []string{bn}
	p.newClient = func() httpclient.AuroraHttpClient {
		atomic.AddInt32(created, 1)
		return &fakeClient{statuses: []int{http.StatusTeapot, http.StatusOK}, bodies: bodies, calls: chatCalls}
	}
	p.sess.client = p.newClient()

	resp, err := p.PostConversation(duckgotypes.ApiRequest{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(created); got != 3 { // newTestProvider + 上面替换 + BN 重建
		t.Fatalf("clients created = %d, want 3", got)
	}
}

func TestChainedChallengeIsUsedOnce(t *testing.T) {
	// 响应头下发的挑战只能用一次：第一个请求用它，之后没有新挑战时回退到 /status
	p, _, _ := newTestProvider(t, nil, 1)
	p.cfg = config{tokenExpiration: time.Minute, tokenMaxUses: 1, challengeTTL: time.Minute, maxAttempts: 1}
	p.cacheChallengeFromHeader(http.Header{"X-Vqd-Hash-1": {base64.StdEncoding.EncodeToString([]byte("next"))}})

	var sources []string
	for i := 0; i < 3; i++ {
		info, err := p.acquireToken()
		if err != nil {
			t.Fatal(err)
		}
		if !info.fresh {
			t.Fatalf("call %d reused a token", i)
		}
		sources = append(sources, info.challenge)
	}
	if got := strings.Join(sources, ","); got != "chained,status,status" {
		t.Fatalf("challenge sources = %s", got)
	}
}

func TestBodySummary(t *testing.T) {
	if got := bodySummary([]byte(`{"model":"x","messages":[{},{},{}]}`)); got != " body_bytes=35 msgs=3" {
		t.Fatalf("got %q", got)
	}
}
