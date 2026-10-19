package auth

import (
	"strings"
	"testing"
)

// The relay in a user-supplied bunker:// URL is a server-side connection
// target. Today nothing routed dials it (CreateAuthChallenge only stores it),
// but ConnectToBunker would; the URL is held to the same public-host rule as
// NIP-05 lookups so wiring that path later cannot open an SSRF.
func TestParseBunkerURL_RelayMustBePublicWSS(t *testing.T) {
	pk := strings.Repeat("a", 64)
	bunker := func(relay string) string { return "bunker://" + pk + "?relay=" + relay }

	for _, relay := range []string{
		"ws://relay.example.com", "http://relay.example.com", "https://relay.example.com",
		"wss://127.0.0.1", "wss://[::1]:7777", "wss://10.0.0.5", "wss://169.254.169.254",
		"wss://localhost:7777", "wss://relay", "wss://relay.local", "wss://cloistr-relay.cloistr.svc",
		"wss://cloistr-relay.cloistr.svc.cluster.local", "wss://0x7f.1", "wss://", "relay.example.com",
	} {
		if _, err := ParseBunkerURL(bunker(relay)); err == nil {
			t.Errorf("relay %q accepted, want refused", relay)
		}
	}
	for _, relay := range []string{"wss://relay.example.com", "wss://relay.example.com:7777/nostr", "wss%3A%2F%2Frelay.damus.io"} {
		if _, err := ParseBunkerURL(bunker(relay)); err != nil {
			t.Errorf("relay %q refused: %v", relay, err)
		}
	}
}
