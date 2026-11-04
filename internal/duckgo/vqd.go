package duckgo

import (
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dop251/goja"
)

// sandboxJS 模拟挑战运行所需的浏览器环境，见 sandbox.js 文件头。
//
//go:embed sandbox.js
var sandboxJS string

const (
	defaultVQDUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"
	defaultVQDOrigin    = "https://duck.ai"
	// 以下两项仅在无法从线上页面解析时使用（见 frontend.go），取自 2026-11-05 的前端
	defaultVQDStack  = "Error\nat l (https://duck.ai/dist/duckai-dist/entry.duckai.74577f9898a32db46c28.js:2:2159967)\nat async https://duck.ai/dist/duckai-dist/entry.duckai.74577f9898a32db46c28.js:2:1876332"
	defaultFEVersion = "serp_20261104_113329_ET-4545a8eec81c4af6c8b0eeb41866f5e4a837945c"
)

// solveChallenge 执行挑战 JS 并按网页的方式封装，返回 x-vqd-hash-1 请求头的值。
// 网页的封装（entry bundle 中 stack:u(new Error) 附近）：
//
//	btoa(JSON.stringify({...r, client_hashes: r.client_hashes.map(sha256base64),
//	                     meta: {...r.meta, origin, stack, duration}}))
func solveChallenge(js string) (string, error) {
	start := time.Now()
	vm := goja.New()
	if err := installSandboxGlobals(vm); err != nil {
		return "", err
	}
	if _, err := vm.RunString(sandboxJS); err != nil {
		return "", fmt.Errorf("initialize vqd browser mock: %w", err)
	}

	value, err := vm.RunString(js)
	if err != nil {
		return "", fmt.Errorf("execute vqd hash script: %s", jsErrorString(vm, err, nil))
	}
	// 挑战是 async 函数；Goja 在 RunString 返回前已跑完微任务，这里只需取结果
	if promise, ok := value.Export().(*goja.Promise); ok {
		switch promise.State() {
		case goja.PromiseStateFulfilled:
			value = promise.Result()
		case goja.PromiseStateRejected:
			return "", fmt.Errorf("vqd hash script rejected: %s", jsErrorString(vm, nil, promise.Result()))
		default:
			return "", errors.New("vqd hash script did not resolve")
		}
	}

	if err := vm.Set("__vqd_result", value); err != nil {
		return "", err
	}
	if err := vm.Set("__vqdDurationMs", fmt.Sprint(time.Since(start).Milliseconds())); err != nil {
		return "", err
	}
	payload, err := vm.RunString(wrapResultJS)
	if err != nil {
		return "", fmt.Errorf("encode vqd hash result: %s", jsErrorString(vm, err, nil))
	}
	if goja.IsUndefined(payload) || goja.IsNull(payload) {
		return "", errors.New("vqd hash script returned an empty result")
	}
	return base64.StdEncoding.EncodeToString([]byte(payload.String())), nil
}

const wrapResultJS = `
(function () {
  var r = __vqd_result;
  if (!r || typeof r !== "object") throw new Error("VQD hash script did not return an object");
  if (!Array.isArray(r.client_hashes)) throw new Error("VQD hash script did not return client_hashes");
  var out = {};
  for (var k in r) if (Object.prototype.hasOwnProperty.call(r, k)) out[k] = r[k];
  out.client_hashes = r.client_hashes.map(function (v) { return __goSha256Base64(String(v)); });
  var meta = {};
  for (var m in r.meta || {}) if (Object.prototype.hasOwnProperty.call(r.meta, m)) meta[m] = r.meta[m];
  meta.origin = __goOrigin;
  meta.stack = __goStack;
  meta.duration = __vqdDurationMs;
  out.meta = meta;
  return JSON.stringify(out);
})();
`

func installSandboxGlobals(vm *goja.Runtime) error {
	fe := currentFrontend()
	globals := map[string]any{
		"__goUserAgent":    vqdUserAgent(),
		"__goOrigin":       vqdOrigin(),
		"__goStack":        vqdStack(),
		"__goBEVersion":    orDefault(fe.beVersion, "dev"),
		"__goFEHash":       orDefault(fe.feHash, "hash"),
		"__goSha256Base64": sha256Base64,
		"__goBtoa":         func(v string) string { return base64.StdEncoding.EncodeToString([]byte(v)) },
		"__goAtob": func(v string) (string, error) {
			b, err := base64.StdEncoding.DecodeString(v)
			return string(b), err
		},
		"__goParseHTMLFragment": func(v string) []any {
			out, n := parseHTMLFragment(v)
			return []any{out, n}
		},
	}
	for name, v := range globals {
		if err := vm.Set(name, v); err != nil {
			return fmt.Errorf("install helper %s: %w", name, err)
		}
	}
	return nil
}

// 以下取值均可用环境变量覆盖：X_USER_AGENT、X_VQD_STACK、X_VQD_ORIGIN。

func vqdUserAgent() string { return envString("X_USER_AGENT", defaultVQDUserAgent) }
func vqdOrigin() string    { return envString("X_VQD_ORIGIN", defaultVQDOrigin) }

func vqdStack() string {
	if v := os.Getenv("X_VQD_STACK"); v != "" {
		return v
	}
	return orDefault(currentFrontend().stack, defaultVQDStack)
}

func sha256Base64(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func jsErrorString(vm *goja.Runtime, err error, value goja.Value) string {
	if err != nil {
		if exc, ok := err.(*goja.Exception); ok {
			return exc.String()
		}
		return err.Error()
	}
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return "<empty>"
	}
	obj := value.ToObject(vm)
	for _, key := range []string{"stack", "message"} {
		if v := obj.Get(key); v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
			return v.String()
		}
	}
	return value.String()
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
