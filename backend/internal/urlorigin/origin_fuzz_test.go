package urlorigin

import (
	"net/url"
	"strings"
	"testing"
)

// FuzzOf fuzzes the origin a bearer token is bound to. A stored token is sent
// only to a URL whose Of equals the stored origin, so Of deciding "same
// origin" for two URLs that dial different places would hand the token to
// another host.
//
//   - Oracle A: Of never panics.
//   - Oracle B: Of is "" exactly when url.Parse fails or leaves no scheme or
//     no host, so "" (which matches nothing) is never a real origin.
//   - Oracle C: a non-empty origin carries no credentials, path, query or
//     fragment, and the round trip the token check makes never lands on a
//     different origin: the origin the bearer round tripper computes from the
//     parsed request URL's String() is the stored one, or "" when that string
//     no longer parses (an IPv6 zone holding a raw non-UTF-8 byte), which
//     matches nothing and withholds the token.
//   - Oracle D: two URLs with the same non-empty origin have the same scheme
//     and the same host, port included, compared case-insensitively: they
//     dial the same place.
func FuzzOf(f *testing.F) {
	pairs := [][2]string{
		{"https://am.example.com", "https://AM.example.com/alertmanager"},
		{"https://am.example.com:9093", "https://am.example.com:9094"}, // ports differ: the port guard
		{"https://am.example.com", "https://am.example.com:443"},       // default port is not normalized
		{"https://am.example.com", "http://am.example.com"},            // scheme differs
		{"https://user:pw@am.example.com", "https://am.example.com"},   // credentials are not part of the origin
		{"https://am.example.com@evil.example.com", "https://evil.example.com"},
		{"https://evil.example.com#@am.example.com", "https://am.example.com"},
		{"https://evil.example.com?@am.example.com", "https://am.example.com"},
		{"https://[2001:DB8::1]:9093", "https://[2001:db8::1]:9093"},
		{"https://[fe80::1%25eth0]:9093", "https://[fe80::1%25eth1]:9093"},
		{"https://am.example.com.", "https://am.example.com"},
		{"https://%61m.example.com", "https://am.example.com"},
		{"//am.example.com", "am.example.com:9093"},
		{"https:///path", ""},
		{"https://am.example.com:port%zz", "\x00"},
		{"A://[::%25\xbe]", "0"}, // zone with a raw non-UTF-8 byte: its String() no longer parses
	}
	for _, p := range pairs {
		f.Add(p[0], p[1])
	}

	f.Fuzz(func(t *testing.T, a, b string) {
		oa, ob := Of(a), Of(b)
		checkOrigin(t, a, oa)
		checkOrigin(t, b, ob)
		if oa == "" || oa != ob {
			return
		}
		ua, _ := url.Parse(a)
		ub, _ := url.Parse(b)
		if !strings.EqualFold(ua.Scheme, ub.Scheme) || !strings.EqualFold(ua.Host, ub.Host) {
			t.Fatalf("Of(%q) == Of(%q) == %q, but they dial %s://%s and %s://%s", a, b, oa, ua.Scheme, ua.Host, ub.Scheme, ub.Host)
		}
	})
}

func checkOrigin(t *testing.T, raw, origin string) {
	t.Helper()
	u, err := url.Parse(raw)
	valid := raw != "" && err == nil && u.Scheme != "" && u.Host != ""
	if valid != (origin != "") {
		t.Fatalf("Of(%q) = %q, but url.Parse gives scheme=%q host=%q err=%v", raw, origin, schemeOf(u), hostOf(u), err)
	}
	if origin == "" {
		return
	}
	scheme, rest, ok := strings.Cut(origin, "://")
	if !ok || scheme == "" || rest == "" {
		t.Fatalf("Of(%q) = %q is not scheme://host", raw, origin)
	}
	if strings.ContainsAny(rest, "@/?#") {
		t.Fatalf("Of(%q) = %q carries credentials, a path, a query or a fragment", raw, origin)
	}
	if sent := Of(u.String()); sent != "" && sent != origin {
		t.Fatalf("Of(%q) = %q, but the request URL %q has origin %q", raw, origin, u.String(), sent)
	}
}

func schemeOf(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Scheme
}

func hostOf(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Host
}
