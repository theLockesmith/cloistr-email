package domains

import (
	"context"
	"net"
	"strings"
)

// DNSChecker resolves records to verify a domain is correctly configured.
// It is an interface so tests can stub DNS.
type DNSChecker interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
}

// NetDNSChecker is the real resolver.
type NetDNSChecker struct{ resolver *net.Resolver }

// NewNetDNSChecker returns a DNSChecker backed by the system resolver.
func NewNetDNSChecker() *NetDNSChecker { return &NetDNSChecker{resolver: net.DefaultResolver} }

func (c *NetDNSChecker) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return c.resolver.LookupTXT(ctx, name)
}

func (c *NetDNSChecker) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	return c.resolver.LookupMX(ctx, name)
}

// DomainDNSStatus reports which expected records are present.
type DomainDNSStatus struct {
	Domain      string `json:"domain"`
	DKIMPresent bool   `json:"dkim_present"` // the exact expected DKIM TXT is published
	DKIMName    string `json:"dkim_name"`
	MXPresent   bool   `json:"mx_present"`
	SPFPresent  bool   `json:"spf_present"`
	DMARCPresent bool  `json:"dmarc_present"`
}

// Verified is true when the DKIM record — the one that actually matters for
// signed outbound — is published correctly. MX/SPF/DMARC are reported for the
// operator but are not required to flip verified.
func (s DomainDNSStatus) Verified() bool { return s.DKIMPresent }

// CheckDomain resolves the expected records for a domain. dkimName is
// "<selector>._domainkey.<domain>" and dkimValue is the "v=DKIM1; ..." record
// we expect to find there (its p= public key is compared, tolerant of DNS
// string splitting and whitespace).
func CheckDomain(ctx context.Context, checker DNSChecker, domain, dkimName, dkimValue string) DomainDNSStatus {
	status := DomainDNSStatus{Domain: domain, DKIMName: dkimName}

	wantKey := dkimPublicKey(dkimValue)
	if txts, err := checker.LookupTXT(ctx, dkimName); err == nil {
		for _, txt := range txts {
			if wantKey != "" && dkimPublicKey(txt) == wantKey {
				status.DKIMPresent = true
				break
			}
		}
	}

	if mxs, err := checker.LookupMX(ctx, domain); err == nil && len(mxs) > 0 {
		status.MXPresent = true
	}

	if txts, err := checker.LookupTXT(ctx, domain); err == nil {
		for _, txt := range txts {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(txt)), "v=spf1") {
				status.SPFPresent = true
				break
			}
		}
	}

	if txts, err := checker.LookupTXT(ctx, "_dmarc."+domain); err == nil {
		for _, txt := range txts {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(txt)), "v=dmarc1") {
				status.DMARCPresent = true
				break
			}
		}
	}

	return status
}

// dkimPublicKey extracts and normalizes the p= public-key value from a DKIM TXT
// record so comparison ignores tag order, spacing, and DNS string splitting.
func dkimPublicKey(txt string) string {
	// A long TXT record may be returned as multiple concatenated strings.
	txt = strings.ReplaceAll(txt, " ", "")
	for _, part := range strings.Split(txt, ";") {
		if strings.HasPrefix(part, "p=") {
			return strings.TrimPrefix(part, "p=")
		}
	}
	return ""
}
