package duckgo

import (
	"testing"
	"time"
)

func TestLoadConfigDefaults(t *testing.T) {
	for _, k := range []string{"TOKEN_EXPIRATION_SECONDS", "TOKEN_MAX_USES", "SCRIPTS_CACHE_SECONDS", "UPSTREAM_MAX_RETRIES", "SESSION_RESET_AFTER_FAILURES", "IDLE_SESSION_RESET_SECONDS"} {
		t.Setenv(k, "")
	}
	got := loadConfig()
	want := config{
		tokenExpiration:   time.Second,
		tokenMaxUses:      1,
		challengeTTL:      5 * time.Minute,
		maxAttempts:       7,
		sessionResetAfter: 2,
		idleResetAfter:    30 * time.Minute,
	}
	if got != want {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}

	t.Setenv("SCRIPTS_CACHE_SECONDS", "0")
	if got := loadConfig().challengeTTL; got != 0 {
		t.Fatalf("SCRIPTS_CACHE_SECONDS=0 -> %s, want 0 (disable chaining)", got)
	}
}
