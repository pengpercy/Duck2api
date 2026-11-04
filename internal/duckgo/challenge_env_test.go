package duckgo

import (
	"strconv"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// 以下期望值均取自真实 Chrome 141 在 duck.ai 的 #jsa 沙箱 iframe 中的结果。
func TestParseHTMLFragmentMatchesBrowser(t *testing.T) {
	cases := []struct {
		src, out string
		count    int
	}{
		{"<li><div></li><li></div", "<li><div></div></li><li></li>", 3},
		{"<p><div></p><p></div", "<p></p><div><p></p><p></p></div>", 4},
		{"<br><div></br><br></div", "<br><div><br><br></div>", 4},
		{"<div><div></div><div></div", "<div><div></div><div></div></div>", 3},
		{"<span><div></span><span></div", "<span><div><span></span></div></span>", 3},
		{"<template><div></template><template></div", "<template><div></div></template><template></template>", 2},
	}
	for _, c := range cases {
		out, n := parseHTMLFragment(c.src)
		if out != c.out || n != c.count {
			t.Errorf("%s: got %q (%d), want %q (%d)", c.src, out, n, c.out, c.count)
		}
	}
}

func runInPrelude(t *testing.T, js string) string {
	t.Helper()
	vm := goja.New()
	if err := installSandboxGlobals(vm); err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunString(sandboxJS); err != nil {
		t.Fatal(err)
	}
	v, err := vm.RunString(js)
	if err != nil {
		t.Fatal(err)
	}
	return v.String()
}

func TestPreludeInnerHTMLCheck(t *testing.T) {
	// 挑战原文（去混淆）：String(base + innerHTML.length * querySelectorAll('*').length)
	got := runInPrelude(t, `(function(){var d=document.createElement("div");d.innerHTML="<p><div></p><p></div";return String(6000+d.innerHTML.length*d.querySelectorAll('*').length)})()`)
	if got != "6128" { // 32*4
		t.Fatalf("got %s, want 6128", got)
	}
}

func TestPreludeSandboxIframeCheck(t *testing.T) {
	// 挑战原文（去混淆）：检查 #jsa 的 CSP meta、sandbox 属性和两个 __DDG_*__ 全局变量，全部为真才 +4
	got := runInPrelude(t, `(function(){var top=window.top,f=top.document.querySelector('#jsa');if(!f)return "early";
		var d=f.contentDocument||f.contentWindow&&f.contentWindow.document;if(!d)return "early";
		var m=d.querySelector('meta[http-equiv="Content-Security-Policy"]');if(!m)return "early";
		return String([m.getAttribute("content")==="default-src 'none'; script-src 'unsafe-inline';",f.getAttribute("sandbox")==="allow-scripts allow-same-origin",top.hasOwnProperty("__DDG_BE_VERSION__"),top.hasOwnProperty("__DDG_FE_CHAT_HASH__")].map(Number).reduce(function(a,b){return a+b},7681))})()`)
	if got != "7685" {
		t.Fatalf("got %s, want 7685", got)
	}
}

func TestComputeStack(t *testing.T) {
	js := "/*x*/\n" + `var é=1;async function l(e,t){const n=Date.now();try{const r=atob(e);x={stack:u(new Error),duration:1}}catch(r){}}` +
		`;function q(){let t;t=await Promise.race([(0,i.wR)(e,f.current),n])}`
	got, err := computeStack("https://duck.ai/dist/duckai-dist/entry.duckai.abc.js", js)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Split(js, "\n")[1]
	col1 := len([]rune(line[:strings.Index(line, "new Error")])) + 1
	col2 := len([]rune(line[:strings.Index(line, "await Promise")])) + 1
	want := "Error\nat l (https://duck.ai/dist/duckai-dist/entry.duckai.abc.js:2:" + strconv.Itoa(col1) + ")\nat async https://duck.ai/dist/duckai-dist/entry.duckai.abc.js:2:" + strconv.Itoa(col2)
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestUpdateFrontendFromHTMLParsesVersion(t *testing.T) {
	t.Cleanup(func() { frontendMu.Lock(); frontendCur = frontendInfo{}; frontendMu.Unlock() })
	page := []byte(`<html data-version-tag="serp_X_ET" data-version-sha="abc123">`)
	updateFrontendFromHTML(nil, page)
	p := &Provider{}
	if got := p.feVersion(); got != "serp_X_ET-abc123" {
		t.Fatalf("fe version = %q", got)
	}
}
