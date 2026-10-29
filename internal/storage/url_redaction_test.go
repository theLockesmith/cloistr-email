package storage

import (
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const testSecret = "Sup3r@Secret"

// logText flattens every logged message and field so a test can search it.
func logText(logs *observer.ObservedLogs) string {
	var b strings.Builder
	for _, e := range logs.All() {
		b.WriteString(e.Message)
		for k, v := range e.ContextMap() {
			b.WriteString(" " + k + "=")
			if s, ok := v.(string); ok {
				b.WriteString(s)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// The Redis session store logged its full URL, password included, at startup.
// Neither the log line nor the returned error may carry the password, including
// when the URL is malformed (Go's URL parser repeats the whole URL in its error)
// and when the password itself contains '@'.
func TestRedisStartupDoesNotLeakPassword(t *testing.T) {
	for _, redisURL := range []string{
		"redis://:" + testSecret + "@127.0.0.1:1/0",        // well-formed; connect fails
		"redis://default:" + testSecret + "@127.0.0.1:1/0", // with a username
		"redis://:" + testSecret + "@127.0.0.1:notaport/0", // malformed port
	} {
		core, logs := observer.New(zap.DebugLevel)
		_, err := NewRedisSessionStore(redisURL, zap.New(core))
		if err == nil {
			t.Fatalf("%s: expected a connect/parse error", redisURL)
		}
		got := logText(logs)
		if !strings.Contains(got, "Initializing Redis session store") {
			t.Fatalf("startup line missing; logs:\n%s", got)
		}
		for _, part := range []string{testSecret, "Sup3r", "Secret"} {
			if strings.Contains(got, part) {
				t.Errorf("log carries password fragment %q:\n%s", part, got)
			}
			if strings.Contains(err.Error(), part) {
				t.Errorf("error carries password fragment %q: %v", part, err)
			}
		}
		if !strings.Contains(got, "127.0.0.1") {
			t.Errorf("masked URL should still show the host; logs:\n%s", got)
		}
	}
}

func TestMaskDatabaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"postgres://u:" + testSecret + "@db:5432/x?sslmode=disable": "postgres://***:***@db:5432/x?sslmode=disable",
		"redis://:" + testSecret + "@r:6379/0":                      "redis://***:***@r:6379/0",
		"redis://r:6379/0":                                          "redis://r:6379/0",
		"postgres://db/x?note=a@b":                                  "postgres://db/x?note=a@b",
	} {
		if got := maskDatabaseURL(in); got != want {
			t.Errorf("maskDatabaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}
