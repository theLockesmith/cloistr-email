package encryption

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"
)

const testPK = "ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12"

// The resolver fetches https://<domain>/.well-known/nostr.json for any domain
// a sender names (inbound verification of any Nostr-signed mail, the send
// path, key discovery). It must never reach a loopback, private or otherwise
// internal address, whatever the name resolves to or redirects to.

func TestNIP05_RefusesInternalNamesAndIPLiterals(t *testing.T) {
	// Count raw TCP connections, not HTTP requests: a blind SSRF only needs
	// the backend to CONNECT (a TLS failure afterwards still told the sender
	// the internal port was open).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var conns atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns.Add(1)
			_ = c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	r := NewNIP05Resolver(zap.NewNop()) // real resolver, no test seams
	for _, domain := range []string{
		"127.0.0.1:" + port, "localhost:" + port, "[::ffff:127.0.0.1]:" + port,
		"127.0.0.1", "[::1]", "10.0.0.5", "169.254.169.254", "0x7f.1",
		"localhost", "intranet", "printer.local", "cloistr-email-backend.cloistr.svc",
		"cloistr-email-backend.cloistr.svc.cluster.local",
	} {
		if _, err := r.ResolvePubkey(context.Background(), "bob@"+domain); err == nil {
			t.Errorf("%s: resolved, want refused", domain)
		}
	}
	if n := conns.Load(); n != 0 {
		t.Errorf("backend opened %d connection(s) to an internal address", n)
	}
}

// A public-looking name that resolves to an internal address is refused at
// dial time, after DNS (so DNS rebinding cannot slip past a name check).
func TestNIP05_RefusesNameResolvingToPrivateIP(t *testing.T) {
	for _, ip := range []string{"10.1.2.3", "127.0.0.1", "172.16.0.9", "192.168.1.1", "100.64.0.1", "169.254.169.254", "fd00::1", "fe80::1", "::1", "0.0.0.0"} {
		r := NewNIP05Resolver(zap.NewNop())
		r.lookupIP = func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP(ip)}, nil }
		if _, err := r.ResolvePubkey(context.Background(), "bob@example.com"); err == nil ||
			!strings.Contains(err.Error(), "not a public address") {
			t.Errorf("%s: err = %v, want refused as not public", ip, err)
		}
	}
}

// testResolver points example.com at an httptest TLS server on loopback and,
// for the dial step only, lets that one listener through.
func testResolver(t *testing.T, srv *httptest.Server) *NIP05Resolver {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	r := NewNIP05Resolver(zap.NewNop())
	r.lookupIP = func(_ context.Context, host string) ([]net.IP, error) {
		if host == "example.com" {
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host}
	}
	r.allowAddr = func(ip net.IP, port string) bool { return ip.IsLoopback() && port == u.Port() }
	r.port = u.Port()
	tr := r.client.Transport.(*http.Transport)
	tr.TLSClientConfig = &tls.Config{RootCAs: srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}
	return r
}

func TestNIP05_DoesNotFollowRedirects(t *testing.T) {
	var internalHits atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		internalHits.Add(1)
		_, _ = w.Write([]byte(`{"names":{"bob":"` + testPK + `"}}`))
	}))
	defer internal.Close()
	for _, target := range []string{internal.URL + "/.well-known/nostr.json?name=bob", "https://example.com/elsewhere"} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			http.Redirect(w, req, target, http.StatusFound)
		}))
		r := testResolver(t, srv)
		if _, err := r.ResolvePubkey(context.Background(), "bob@example.com"); err == nil {
			t.Errorf("redirect to %s followed", target)
		}
		srv.Close()
	}
	if internalHits.Load() != 0 {
		t.Error("redirect reached the internal (127.0.0.1, http) server")
	}
}

func TestNIP05_PublicLookupStillWorksAndEncodesName(t *testing.T) {
	var gotName string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotName = req.URL.Query().Get("name")
		_, _ = w.Write([]byte(`{"names":{"bob&x=1":"` + testPK + `"}}`))
	}))
	defer srv.Close()
	r := testResolver(t, srv)
	pk, err := r.ResolvePubkey(context.Background(), "bob&x=1@example.com")
	if err != nil || pk != testPK {
		t.Fatalf("pk=%q err=%v", pk, err)
	}
	if gotName != "bob&x=1" {
		t.Errorf("server saw name=%q: the local part was not query-escaped", gotName)
	}
}

func TestNIP05_CacheIsBounded(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte(`{"names":{"` + req.URL.Query().Get("name") + `":"` + testPK + `"}}`))
	}))
	defer srv.Close()
	r := testResolver(t, srv)
	r.maxCacheEntries = 5
	for i := 0; i < 20; i++ {
		if _, err := r.ResolvePubkey(context.Background(), "u"+strings.Repeat("x", i)+"@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	if n := r.CacheStats(); n > 5 {
		t.Errorf("cache holds %d entries, cap 5", n)
	}
}
