package storage

import (
	"errors"
	"net/url"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// Audit on !210: URL parse errors still carried password text.
//   - redis://u:pa/ss@host: the '/' ends the authority, so Go reads "u:pa" as
//     host:port and reports `invalid port ":pa"`, a fragment no exact-match
//     scrub of "pa/ss" can catch.
//   - postgres://u:p"q@host: url.Error quotes the URL with %q, so the password
//     appears as p\"q, not p"q.
//
// A URL parse error must never echo its input; other errors must also be
// scrubbed in their Go-quoted spelling.
func TestURLParseErrorsDoNotEchoInput(t *testing.T) {
	for _, tc := range []struct {
		name, dsn string
		parts     []string
	}{
		{"slash splits the password", "redis://u:pa/ss@127.0.0.1:6379/0", []string{"pa/ss", ":pa", "u:pa"}},
		{"quote in the password", `postgres://u:p"q@127.0.0.1:notaport/x`, []string{`p"q`, `p\"q`}},
	} {
		t.Run(tc.name+"/redis", func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			_, err := NewRedisSessionStore(tc.dsn, zap.New(core))
			if err == nil {
				t.Fatal("expected an error")
			}
			assertNoSecret(t, "error", err.Error(), tc.parts...)
			assertNoSecret(t, "log", logText(logs), tc.parts...)
		})
		t.Run(tc.name+"/postgres", func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			_, err := NewPostgres(tc.dsn, zap.New(core))
			if err == nil {
				t.Fatal("expected an error")
			}
			assertNoSecret(t, "error", err.Error(), tc.parts...)
			assertNoSecret(t, "log", logText(logs), tc.parts...)
		})
		t.Run(tc.name+"/RedactDSNError", func(t *testing.T) {
			_, perr := url.Parse(tc.dsn)
			if perr == nil {
				perr = &url.Error{Op: "parse", URL: tc.dsn, Err: errors.New("invalid port \":pa\" after host")}
			}
			assertNoSecret(t, "error", RedactDSNError(perr, tc.dsn).Error(), tc.parts...)
		})
	}
}

// A non-URL error that repeats the password in Go-quoted form is scrubbed too.
func TestRedactDSNErrorScrubsQuotedSpelling(t *testing.T) {
	dsn := `host=db password='p"q' dbname=x`
	err := RedactDSNError(errors.New(`pq: unexpected "p\"q" in connection info string`), dsn)
	assertNoSecret(t, "error", err.Error(), `p"q`, `p\"q`)
}
