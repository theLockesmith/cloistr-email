package encryption

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// NIP-05 lookups fetch from a domain the sender chooses (the From of any
// Nostr-signed inbound mail, a recipient address, key discovery). Without
// these checks that is a blind SSRF: the backend would connect to loopback,
// cluster-internal or cloud-metadata addresses on a stranger's say-so.

var errNIP05Internal = errors.New("NIP-05 domain is not a public address")

// checkNIP05Domain refuses domains that can only name something internal:
// IP literals (including odd forms like 0x7f.1), single-label names, and the
// local/cluster suffixes. A port in the domain is refused too: NIP-05 is
// always https on 443.
func checkNIP05Domain(domain string) error {
	d := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if d == "" || strings.ContainsAny(d, ":[]/\\@?#% ") {
		return fmt.Errorf("%w: %q", errNIP05Internal, domain)
	}
	if _, err := netip.ParseAddr(d); err == nil {
		return fmt.Errorf("%w: %q is an IP address", errNIP05Internal, domain)
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return fmt.Errorf("%w: %q is a single-label name", errNIP05Internal, domain)
	}
	// A purely numeric/hex final label is an IP spelling (0x7f.1, 127.1), not
	// a TLD; resolvers would happily turn it into an address.
	last := labels[len(labels)-1]
	if strings.Trim(last, "0123456789") == "" || strings.HasPrefix(last, "0x") {
		return fmt.Errorf("%w: %q looks like an IP address", errNIP05Internal, domain)
	}
	for _, suffix := range []string{".local", ".localhost", ".internal", ".svc", ".cluster.local", ".lan", ".home.arpa"} {
		if strings.HasSuffix("."+d, suffix) {
			return fmt.Errorf("%w: %q is an internal name", errNIP05Internal, domain)
		}
	}
	return nil
}

var nonPublicPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, p := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"64:ff9b::/96", "100::/64", "2001:db8::/32",
	} {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

// isPublicIP reports whether ip is a globally routable unicast address:
// not loopback, private (RFC 1918, ULA fc00::/7), link-local (incl. cloud
// metadata 169.254.169.254), CGNAT, unspecified, multicast or reserved.
func isPublicIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() ||
		a.IsLinkLocalMulticast() || a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() {
		return false
	}
	for _, p := range nonPublicPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// dialPublic resolves the host itself and connects only to an address that
// passes allowAddr, so the check applies to the address actually dialled
// (a name that resolves to 10.0.0.1, or re-resolves to it later, is refused).
func (r *NIP05Resolver) dialPublic(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if r.port != "" {
		port = r.port
	}
	ips, err := r.lookupIP(ctx, host)
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	lastErr := fmt.Errorf("%w: %s has no addresses", errNIP05Internal, host)
	for _, ip := range ips {
		if !r.allowAddr(ip, port) {
			lastErr = fmt.Errorf("%w: %s resolves to %s", errNIP05Internal, host, ip)
			continue
		}
		c, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return c, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func defaultLookupIP(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, len(addrs))
	for i, a := range addrs {
		ips[i] = a.IP
	}
	return ips, nil
}

func defaultAllowAddr(ip net.IP, _ string) bool { return isPublicIP(ip) }

// CheckPublicHostname applies the NIP-05 domain rule to any hostname the
// backend would connect to on a user's say-so (e.g. a bunker:// relay): no IP
// literals, single-label names or internal suffixes. It is a name check; use
// ResolvesPublic as well before dialling.
func CheckPublicHostname(host string) error { return checkNIP05Domain(host) }

// ResolvesPublic resolves host and refuses it unless every address is public.
func ResolvesPublic(ctx context.Context, host string) error {
	ips, err := defaultLookupIP(ctx, host)
	if err != nil {
		return err
	}
	if len(ips) == 0 {
		return fmt.Errorf("%w: %s has no addresses", errNIP05Internal, host)
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return fmt.Errorf("%w: %s resolves to %s", errNIP05Internal, host, ip)
		}
	}
	return nil
}
