package duckgo

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// TestChallengeCorpus 把 tools/challenge-compare/collect.mjs 采集的挑战交给 Goja 求解，
// 与真实浏览器的原始答案逐项比对。418 增多时先跑它，确认是否出现了新的检测项。
//
//	CHALLENGE_CORPUS=<目录> go test ./internal/duckgo -run TestChallengeCorpus -v
func TestChallengeCorpus(t *testing.T) {
	dir := os.Getenv("CHALLENGE_CORPUS")
	if dir == "" {
		t.Skip("CHALLENGE_CORPUS not set")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.b64"))
	if len(files) == 0 {
		t.Fatalf("no challenges in %s", dir)
	}
	type answer struct {
		ClientHashes []string       `json:"client_hashes"`
		Meta         map[string]any `json:"meta"`
	}
	mismatches := 0
	for _, f := range files {
		b64, _ := os.ReadFile(f)
		rawJSON, _ := os.ReadFile(strings.TrimSuffix(f, ".b64") + ".raw.json")
		var want answer
		if err := json.Unmarshal(rawJSON, &want); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		js, _ := base64.StdEncoding.DecodeString(string(b64))
		var got answer
		if err := json.Unmarshal([]byte(rawAnswer(t, string(js))), &got); err != nil {
			t.Fatalf("%s: %v", f, err)
		}

		var diffs []string
		for i, w := range want.ClientHashes {
			if strings.HasPrefix(w, "Mozilla/") {
				continue // UA 因环境不同必然不一致
			}
			if i >= len(got.ClientHashes) || got.ClientHashes[i] != w {
				g := ""
				if i < len(got.ClientHashes) {
					g = got.ClientHashes[i]
				}
				diffs = append(diffs, fmt.Sprintf("client_hashes[%d] ours=%s browser=%s", i, g, w))
			}
		}
		if g, w := toString(got.Meta["debug"]), toString(want.Meta["debug"]); g != w {
			diffs = append(diffs, fmt.Sprintf("meta.debug ours=%q browser=%q", g, w))
		}
		if len(diffs) > 0 {
			mismatches++
			t.Errorf("%s: %s", filepath.Base(f), strings.Join(diffs, "; "))
		}
	}
	t.Logf("matched %d/%d", len(files)-mismatches, len(files))
}

// rawAnswer 返回挑战未经封装的结果（client_hashes 尚未做 SHA-256）。
func rawAnswer(t *testing.T, js string) string {
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
	if p, ok := v.Export().(*goja.Promise); ok {
		v = p.Result()
	}
	out, err := json.Marshal(v.Export())
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func toString(v any) string {
	s, _ := v.(string)
	return s
}
