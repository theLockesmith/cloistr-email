package encryption

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"
)

// From cloistr-pr's review of the SSRF fix.

// Redirect to a server the dial guard WOULD allow, serving a valid answer.
// Only the no-redirect rule can refuse this.
func TestReview_RedirectRefusedEvenWhenTargetAllowed(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/final" {
			hits.Add(1)
			_, _ = w.Write([]byte(`{"names":{"bob":"` + testPK + `"}}`))
			return
		}
		http.Redirect(w, req, "https://example.com/final", http.StatusFound)
	}))
	defer srv.Close()
	r := testResolver(t, srv)
	if _, err := r.ResolvePubkey(context.Background(), "bob@example.com"); err == nil {
		t.Errorf("redirect followed (final hits=%d)", hits.Load())
	}
}

// Domain check must refuse before any DNS lookup or dial happens.
func TestReview_DomainCheckRunsBeforeLookup(t *testing.T) {
	var lookups atomic.Int32
	for _, d := range []string{"localhost", "intranet", "printer.local", "x.svc.cluster.local", "0x7f.1", "10.0.0.5"} {
		r := NewNIP05Resolver(zap.NewNop())
		r.lookupIP = func(context.Context, string) ([]net.IP, error) {
			lookups.Add(1)
			return []net.IP{net.ParseIP("8.8.8.8")}, nil
		}
		r.allowAddr = func(net.IP, string) bool { return false }
		_, _ = r.ResolvePubkey(context.Background(), "bob@"+d)
	}
	if n := lookups.Load(); n != 0 {
		t.Errorf("%d lookup(s) reached DNS for internal names", n)
	}
}
