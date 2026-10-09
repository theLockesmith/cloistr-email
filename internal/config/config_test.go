package config

import (
	"strings"
	"testing"
)

// setRequired sets every variable Load requires, so a test can unset one.
func setRequired(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"DATABASE_URL":            "postgres://u:p@localhost/db",
		"REDIS_URL":               "redis://localhost:6379",
		"NSECBUNKER_RELAY_URL":    "ws://localhost:4737",
		"MAIL_SIGNER_URL":         "http://signer:7777",
		"CLOISTR_ME_URL":          "http://me:8080",
		"MAIL_NOSTRCONNECT_RELAY": "ws://relay:4737",
	} {
		t.Setenv(k, v)
	}
}

func TestLoadReadsServiceURLs(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SignerURL != "http://signer:7777" || cfg.CloistrMeURL != "http://me:8080" || cfg.NostrConnectRelay != "ws://relay:4737" {
		t.Fatalf("unexpected URLs: signer=%q me=%q relay=%q", cfg.SignerURL, cfg.CloistrMeURL, cfg.NostrConnectRelay)
	}
}

// An unset service URL must refuse to start, naming the variable, rather
// than fall back to a production address.
func TestLoadRefusesUnsetServiceURL(t *testing.T) {
	for _, key := range []string{"MAIL_SIGNER_URL", "CLOISTR_ME_URL", "MAIL_NOSTRCONNECT_RELAY"} {
		t.Run(key, func(t *testing.T) {
			setRequired(t)
			t.Setenv(key, "")
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("Load succeeded with %s unset", key)
				}
				if msg, _ := r.(string); !strings.Contains(msg, key) {
					t.Fatalf("panic %v does not name %s", r, key)
				}
			}()
			_, _ = Load()
		})
	}
}
