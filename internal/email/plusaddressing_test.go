package email

import "testing"

// Plus addressing (RFC 5233 sub-addressing) is something users expect to just
// work — it is how people filter signups without handing out a real address.
// Reported 2026-08-19: mail to alice+tag@ bounced as "no such recipient", which
// is worse than not offering it at all, because the address looks accepted
// right up until mail to it silently fails.
func TestStripPlusTag(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"strips a simple tag", "alice+news@cloistr.xyz", "alice@cloistr.xyz"},
		{"untagged is unchanged", "alice@cloistr.xyz", "alice@cloistr.xyz"},
		{"strips at the FIRST plus, tags may contain plus",
			"alice+a+b@cloistr.xyz", "alice@cloistr.xyz"},
		{"empty tag still strips", "alice+@cloistr.xyz", "alice@cloistr.xyz"},

		// The + must be in the LOCAL part. A + in the domain is a (strange)
		// domain, not a tag — stripping there would rewrite the destination.
		{"plus in domain is untouched", "alice@cloistr+x.xyz", "alice@cloistr+x.xyz"},

		// A local part starting with + would reduce to "@domain": an empty
		// mailbox name that must never be allowed to match a real account.
		{"leading plus is NOT stripped", "+tag@cloistr.xyz", "+tag@cloistr.xyz"},

		// Degenerate inputs must pass through rather than panic or produce "@".
		{"no at sign", "notanaddress", "notanaddress"},
		{"leading at", "@cloistr.xyz", "@cloistr.xyz"},
		{"empty", "", ""},

		// A QUOTED local part means the + is LITERAL — `"a+b"@x` is a mailbox
		// actually named a+b. Stripping would redirect mail to a different
		// mailbox.
		{"quoted local part keeps its plus", `"a+b"@cloistr.xyz`, `"a+b"@cloistr.xyz`},

		// An unquoted @ in the local part is malformed. Leave it rather than
		// rewriting it into a DIFFERENT malformed address.
		{"malformed unquoted at is untouched", "a@b+c@cloistr.xyz", "a@b+c@cloistr.xyz"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := StripPlusTag(c.in); got != c.want {
				t.Errorf("StripPlusTag(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// The stripped form must never be a bare domain — that would be an address with
// no mailbox, and any lookup matching it would deliver mail to the wrong place.
func TestStripPlusTag_NeverProducesEmptyLocalPart(t *testing.T) {
	for _, in := range []string{"+@cloistr.xyz", "+tag@cloistr.xyz", "+@x"} {
		got := StripPlusTag(in)
		if len(got) > 0 && got[0] == '@' {
			t.Errorf("StripPlusTag(%q) = %q — empty local part must not be produced", in, got)
		}
	}
}
