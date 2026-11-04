package duckgo

import (
	"aurora/httpclient"
	"aurora/logger"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"unicode/utf16"
)

// frontendInfo 是线上 duck.ai 前端的版本信息。x-fe-version、挑战里的 window.__DDG_*__
// 和 meta.stack 都随前端发版变化，因此每次预热会话时从 /chat 页面重新解析。
type frontendInfo struct {
	beVersion string // <html data-version-tag>
	feHash    string // <html data-version-sha>
	bundle    string // entry.duckai.<hash>.js
	stack     string // 网页求解挑战时 meta.stack 的值
}

var (
	frontendMu  sync.RWMutex
	frontendCur frontendInfo
)

func currentFrontend() frontendInfo {
	frontendMu.RLock()
	defer frontendMu.RUnlock()
	return frontendCur
}

var (
	reVersionTag = regexp.MustCompile(`data-version-tag="([^"]+)"`)
	reVersionSha = regexp.MustCompile(`data-version-sha="([^"]+)"`)
	reBundle     = regexp.MustCompile(`entry\.duckai\.[0-9a-f]+\.js`)
	// 求解函数：async function l(e,t){const n=Date.now();try{const r=atob(e)...
	reSolveFn = regexp.MustCompile(`async function ([\w$]+)\(e,t\)\{const n=Date\.now\(\);try\{const r=atob\(e\)`)
	// meta.stack 由 stack:u(new Error) 生成，列号指向 new Error
	reStackErr = regexp.MustCompile(`stack:[\w$]+\(new Error\)`)
	// 调用方：t=await Promise.race([(0,i.wR)(e,f.current),n])，列号指向 await
	reSolveCaller = regexp.MustCompile(`=await Promise\.race\(\[\(0,[\w$]+\.[\w$]+\)\(e,[\w$]+\.current\),n\]\)`)
)

// updateFrontendFromHTML 解析 /chat 页面；入口 bundle 变化时下载它重新计算 stack。
// 返回给预热日志的附注（如 "stack=updated"），无变化时为空。
func updateFrontendFromHTML(client httpclient.AuroraHttpClient, page []byte) string {
	info := currentFrontend()
	if m := reVersionTag.FindSubmatch(page); m != nil {
		info.beVersion = string(m[1])
	}
	if m := reVersionSha.FindSubmatch(page); m != nil {
		info.feHash = string(m[1])
	}
	bundle := string(reBundle.Find(page))
	note := ""
	if bundle != "" && bundle != info.bundle {
		if stack, err := fetchBundleStack(client, bundle); err != nil {
			note = fmt.Sprintf("stack_err=%q", err.Error()) // 下次预热会重试
		} else {
			info.bundle, info.stack = bundle, stack
			note = "stack=updated"
		}
	}
	frontendMu.Lock()
	changed := frontendCur != info
	frontendCur = info
	frontendMu.Unlock()
	if changed {
		logger.Infof("[frontend] version=%s-%s bundle=%s %s", info.beVersion, info.feHash, info.bundle, note)
	}
	return note
}

func fetchBundleStack(client httpclient.AuroraHttpClient, bundle string) (string, error) {
	url := "https://duck.ai/dist/duckai-dist/" + bundle
	resp, err := client.Request(httpclient.GET, url, scriptHeaders(), nil, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bundle status %d", resp.StatusCode)
	}
	js, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return computeStack(url, string(js))
}

// computeStack 在 bundle 中定位求解函数，按 V8 格式拼出 "Error\nat fn (url:line:col)\nat async url:line:col"。
// 假定求解函数与调用方在同一行（bundle 是压缩后的单行代码）。
func computeStack(url, js string) (string, error) {
	lines := strings.Split(js, "\n")
	for i, line := range lines {
		errLoc := reStackErr.FindStringIndex(line)
		if errLoc == nil {
			continue
		}
		col1 := utf16Col(line, errLoc[0]+strings.Index(line[errLoc[0]:errLoc[1]], "new Error"))
		fnName := ""
		for _, m := range reSolveFn.FindAllStringSubmatchIndex(line[:errLoc[0]], -1) {
			fnName = line[m[2]:m[3]]
		}
		callLoc := reSolveCaller.FindStringIndex(line)
		if fnName == "" || callLoc == nil {
			return "", fmt.Errorf("solver pattern not found")
		}
		col2 := utf16Col(line, callLoc[0]+1) // 跳过 "=" 指向 await
		return fmt.Sprintf("Error\nat %s (%s:%d:%d)\nat async %s:%d:%d", fnName, url, i+1, col1, url, i+1, col2), nil
	}
	return "", fmt.Errorf("stack pattern not found")
}

// utf16Col 把字节偏移换算成 V8 堆栈里的列号（UTF-16 码元，从 1 开始）。
func utf16Col(line string, byteOff int) int {
	return len(utf16.Encode([]rune(line[:byteOff]))) + 1
}
