package domains

import (
	"context"
	"net"
	"testing"
)

type stubDNS struct {
	txt map[string][]string
	mx  map[string][]*net.MX
}

func (s stubDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	return s.txt[name], nil
}
func (s stubDNS) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	return s.mx[name], nil
}

const testDKIMValue = "v=DKIM1; k=rsa; p=MIIBIjANBgkqABCDEF"

func TestCheckDomain_AllPresent(t *testing.T) {
	dns := stubDNS{
		txt: map[string][]string{
			"mail._domainkey.cloistr.xyz": {testDKIMValue},
			"cloistr.xyz":                 {"v=spf1 include:_spf.cloistr.xyz -all"},
			"_dmarc.cloistr.xyz":          {"v=DMARC1; p=reject"},
		},
		mx: map[string][]*net.MX{"cloistr.xyz": {{Host: "mail.cloistr.xyz", Pref: 10}}},
	}
	got := CheckDomain(context.Background(), dns, "cloistr.xyz", "mail._domainkey.cloistr.xyz", testDKIMValue)
	if !got.DKIMPresent || !got.MXPresent || !got.SPFPresent || !got.DMARCPresent {
		t.Fatalf("expected all present, got %+v", got)
	}
	if !got.Verified() {
		t.Errorf("Verified() should be true when DKIM present")
	}
}

func TestCheckDomain_DKIMMismatchIsNotVerified(t *testing.T) {
	dns := stubDNS{txt: map[string][]string{
		"mail._domainkey.cloistr.xyz": {"v=DKIM1; k=rsa; p=SOMEOTHERKEY"},
	}}
	got := CheckDomain(context.Background(), dns, "cloistr.xyz", "mail._domainkey.cloistr.xyz", testDKIMValue)
	if got.DKIMPresent {
		t.Errorf("a different published key must not count as present")
	}
	if got.Verified() {
		t.Errorf("Verified() must be false on key mismatch")
	}
}

func TestCheckDomain_ToleratesSplitStringsAndSpacing(t *testing.T) {
	// DNS may return a long TXT split into pieces / with spaces; comparison is
	// on the normalized p= value only.
	dns := stubDNS{txt: map[string][]string{
		"mail._domainkey.cloistr.xyz": {"v=DKIM1;  k=rsa;  p=MIIBIjANBgkq ABCDEF"},
	}}
	got := CheckDomain(context.Background(), dns, "cloistr.xyz", "mail._domainkey.cloistr.xyz", testDKIMValue)
	if !got.DKIMPresent {
		t.Errorf("normalized comparison should match despite spacing")
	}
}

func TestCheckDomain_MissingDKIM(t *testing.T) {
	got := CheckDomain(context.Background(), stubDNS{}, "cloistr.xyz", "mail._domainkey.cloistr.xyz", testDKIMValue)
	if got.Verified() {
		t.Errorf("no records published => not verified")
	}
}
