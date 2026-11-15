package routes

import "testing"

// IPv6 clients are rate limited per /64: one host usually owns the whole
// network and could otherwise rotate through 2^64 limiter buckets.
func TestIPLimitKey(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.9":                    "203.0.113.9",
		"::ffff:203.0.113.9":             "203.0.113.9",
		"2001:db8:1:2:aaaa:bbbb:cccc:1":  "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff:ffff:ffff:ff": "2001:db8:1:2::/64",
		"::1":                            "::/64",
		"not-an-ip":                      "not-an-ip",
	} {
		if got := IPLimitKey(in); got != want {
			t.Errorf("IPLimitKey(%q) = %q, want %q", in, got, want)
		}
	}
	if IPLimitKey("2001:db8:1:2::1") == IPLimitKey("2001:db8:1:3::1") {
		t.Error("different /64s share a key")
	}
}
