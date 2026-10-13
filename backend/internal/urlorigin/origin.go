// Package urlorigin computes the origin a bearer token is bound to. It is the
// one place every stored and compared token origin comes from, so the store,
// the binding API and the client resolver cannot disagree about what "the
// same host" means. Standard library only: it is a leaf package.
package urlorigin

import (
	"net/url"
	"strings"
)

// Of returns the origin of raw: the lowercase scheme, "://", and the
// lowercase host with any explicit port kept exactly as given. Path, query,
// fragment and credentials are dropped. Default ports are not normalized, so
// "https://h" and "https://h:443" are different origins; a mismatch only
// forces a token to be entered again, which is the safe direction.
//
// It returns "" for an empty, unparsable, scheme-less or host-less URL. ""
// is never a valid origin: callers must treat it as matching nothing.
func Of(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}
