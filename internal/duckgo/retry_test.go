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
)

// fakeClient 对 status 接口返回挑战，对 chat 接口依次返回 statuses 中的状态码。
type fakeClient struct {
	statuses []int
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
	return resp, nil
}

func (f *fakeClient) SetProxy(string) error { return nil }

func newTestProvider(t *testing.T, statuses []int, maxAttempts int) (*Provider, *int32, *int32) {
	t.Helper()
	originalGenerate := generateVQDToken
	t.Cleanup(func() { generateVQDToken = originalGenerate })
	generateVQDToken = func(string) (string, error) { return "token", nil }

	var chatCalls, created int32
	newClient := func() httpclient.AuroraHttpClient {
		atomic.AddInt32(&created, 1)
		return &fakeClient{statuses: statuses, calls: &chatCalls}
	}
	p := &Provider{
		client:            newClient(),
		newClient:         newClient,
		maxAttempts:       maxAttempts,
		sessionResetAfter: 2,
	}
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
