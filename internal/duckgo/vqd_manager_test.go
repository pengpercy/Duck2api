package duckgo

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestGenerateVQDTokenFromJS(t *testing.T) {
	token, err := generateVQDTokenFromJS(`(function () {
		return {client_hashes: ["first", "second"], meta: {v: "4"}};
	})()`)
	if err != nil {
		t.Fatal(err)
	}

	payload, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		ClientHashes []string          `json:"client_hashes"`
		Meta         map[string]string `json:"meta"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatal(err)
	}
	firstHash := sha256.Sum256([]byte("first"))
	if got, want := result.ClientHashes[0], base64.StdEncoding.EncodeToString(firstHash[:]); got != want {
		t.Fatalf("first client hash = %q, want %q", got, want)
	}
	if result.Meta["v"] != "4" || result.Meta["origin"] != defaultVQDOrigin || result.Meta["stack"] == "" {
		t.Fatalf("meta = %#v", result.Meta)
	}
}

func TestRefreshTokenUsesGoja(t *testing.T) {
	originalGenerate := generateVQDToken
	t.Cleanup(func() { generateVQDToken = originalGenerate })

	generateVQDToken = func(jsCode string) (string, error) {
		if jsCode != "challenge" {
			t.Fatalf("challenge = %q", jsCode)
		}
		return "goja-token", nil
	}

	p := &Provider{
		jsCode:          cachedItem[string]{Value: "challenge", ExpireAt: time.Now().Add(time.Minute)},
		tokenExpiration: time.Minute,
	}
	token, err := p.refreshToken()
	if err != nil {
		t.Fatal(err)
	}
	if token != "goja-token" {
		t.Fatalf("token = %q", token)
	}
}

func TestRefreshTokenReturnsGojaErrorWithoutFallback(t *testing.T) {
	originalGenerate := generateVQDToken
	t.Cleanup(func() { generateVQDToken = originalGenerate })
	generateVQDToken = func(string) (string, error) { return "", errors.New("challenge failed") }

	p := &Provider{
		jsCode:          cachedItem[string]{Value: "challenge", ExpireAt: time.Now().Add(time.Minute)},
		tokenExpiration: time.Minute,
	}
	if _, err := p.refreshToken(); err == nil || !strings.Contains(err.Error(), "goja VQD generation failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}
