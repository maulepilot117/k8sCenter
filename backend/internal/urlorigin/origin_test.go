package urlorigin

import "testing"

func TestOf(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain https", "https://am.example.com", "https://am.example.com"},
		{"uppercase scheme and host", "HTTPS://AM.Example.COM", "https://am.example.com"},
		{"path prefix dropped", "https://am.example.com/alertmanager/", "https://am.example.com"},
		{"explicit port kept", "https://am.example.com:9093", "https://am.example.com:9093"},
		// Default ports are not normalized: a mismatch only forces a re-entry.
		{"default port kept as given", "https://am.example.com:443", "https://am.example.com:443"},
		{"query and fragment dropped", "https://am.example.com/x?a=1#f", "https://am.example.com"},
		{"credentials dropped", "https://user:pw@am.example.com", "https://am.example.com"},
		{"ipv6 literal with port", "https://[2001:DB8::1]:9093", "https://[2001:db8::1]:9093"},
		{"empty", "", ""},
		{"no scheme", "//am.example.com", ""},
		{"no host", "https:///path", ""},
		{"opaque", "am.example.com:9093", ""},
		{"unparsable", "https://am.example.com:port%zz", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Of(c.raw); got != c.want {
				t.Errorf("Of(%q) = %q; want %q", c.raw, got, c.want)
			}
		})
	}
}
