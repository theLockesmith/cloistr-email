package storage

import (
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// Audit follow-up to the Redis log mask: the password can also sit in a
// ?password= query parameter, in a key=value DSN, or contain a raw '/'
// (base64-style), and each of those still reached the log or the error.
const pwSlash = "ab/Cd+Ef9z=="

func assertNoSecret(t *testing.T, where, got string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(got, p) {
			t.Errorf("%s carries password fragment %q: %s", where, p, got)
		}
	}
}

func TestMaskDatabaseURLMoreForms(t *testing.T) {
	for in, want := range map[string]string{
		"redis://:" + pwSlash + "@r:6379/0":                    "redis://***:***@r:6379/0",
		"redis://r:6379/0?password=Sup3rSecret&db=1":           "redis://r:6379/0?password=***&db=1",
		"postgres://db/x?sslmode=disable&password=Sup3rSecret": "postgres://db/x?sslmode=disable&password=***",
		"host=db user=u password=Sup3rSecret dbname=x":         "host=db user=u password=*** dbname=x",
		"host=db password='Sup3r Secret' dbname=x":             "host=db password=*** dbname=x",
		"host=db dbname=x":                                     "host=db dbname=x",
	} {
		if got := maskDatabaseURL(in); got != want {
			t.Errorf("maskDatabaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRedisStartupMoreFormsDoNotLeak(t *testing.T) {
	for redisURL, parts := range map[string][]string{
		"redis://:" + pwSlash + "@127.0.0.1:1/0":        {pwSlash, "Cd+Ef9z"},
		"redis://:" + pwSlash + "@127.0.0.1:notaport/0": {pwSlash, "Cd+Ef9z"},
		"redis://127.0.0.1:1/0?password=Sup3rSecret":    {"Sup3rSecret"},
	} {
		core, logs := observer.New(zap.DebugLevel)
		_, err := NewRedisSessionStore(redisURL, zap.New(core))
		if err == nil {
			t.Fatalf("%s: expected a connect/parse error", redisURL)
		}
		assertNoSecret(t, "log", logText(logs), parts...)
		assertNoSecret(t, "error", err.Error(), parts...)
	}
}

func TestPostgresStartupKeyValueDSNDoesNotLeak(t *testing.T) {
	for _, dsn := range []string{
		"host=127.0.0.1 port=1 user=u password=Sup3rSecret dbname=x sslmode=disable connect_timeout=1",
		"host=127.0.0.1 port=1 password=Sup3rSecret junk", // malformed: pq echoes the string
		"postgres://u:" + pwSlash + "@127.0.0.1:notaport/x",
	} {
		core, logs := observer.New(zap.DebugLevel)
		_, err := NewPostgres(dsn, zap.New(core))
		if err == nil {
			t.Fatalf("%s: expected a connect/parse error", dsn)
		}
		parts := []string{"Sup3rSecret", pwSlash, "Cd+Ef9z"}
		assertNoSecret(t, "log", logText(logs), parts...)
		assertNoSecret(t, "error", err.Error(), parts...)
	}
}

// RedactDSNError is what cmd/email uses for errors from libraries it hands the
// DSN to (platform.NewClient), so those cannot log it either.
func TestRedactDSNError(t *testing.T) {
	dsn := "host=db password=Sup3rSecret dbname=x"
	err := RedactDSNError(errors.New(`pq: missing "=" after "Sup3rSecret junk" in connection info string`), dsn)
	assertNoSecret(t, "error", err.Error(), "Sup3rSecret")
	if !strings.Contains(err.Error(), "connection info string") {
		t.Errorf("redaction dropped the rest of the message: %v", err)
	}
	if RedactDSNError(nil, dsn) != nil {
		t.Error("nil error must stay nil")
	}
}
