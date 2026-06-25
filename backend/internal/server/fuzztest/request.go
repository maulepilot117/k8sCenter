package fuzztest

import (
	"bytes"
	"net/http"
	"net/http/httptest"
)

// knownPaths is a fixed table of API route prefixes the fuzzer selects from.
// Index is derived from corpus bytes so the selection is deterministic and
// reproducible across runs. All paths are under /api/v1 to hit the auth +
// CSRF + ClusterContext middleware chain.
var knownPaths = []string{
	"/api/v1/auth/me",
	"/api/v1/auth/refresh",
	"/api/v1/settings",
	"/api/v1/users",
	"/api/v1/resources/secrets/default/test",
	"/api/v1/resources/secrets",
	"/api/v1/resources/pods",
	"/api/v1/resources/deployments",
	"/api/v1/resources/namespaces",
	"/api/v1/resources/nodes",
	"/api/v1/cluster/dashboard-summary",
	"/api/v1/monitoring/status",
	"/api/v1/logs/status",
}

// knownMethods is the set of HTTP methods mapped from corpus byte.
var knownMethods = []string{
	http.MethodGet,
	http.MethodPost,
	http.MethodPut,
	http.MethodDelete,
	http.MethodPatch,
}

// BuildRequest deterministically maps a fuzz corpus byte slice onto an
// *http.Request. The mapping is:
//
//   - byte[0] (mod len(knownMethods)) → HTTP method
//   - byte[1] (mod len(knownPaths))   → path (with remaining bytes appended as a suffix)
//   - byte[2] (mod 4) → header toggles:
//     bit 0: include X-Requested-With: XMLHttpRequest
//     bit 1: include X-Cluster-ID: local
//   - byte[3] (mod 3) → Authorization header mode:
//     0 = absent, 1 = "Bearer <token from Token arg>", 2 = garbage
//   - remaining bytes → request body (raw, not necessarily valid JSON)
//
// The returned request is built via httptest.NewRequest so it is safe to
// feed directly to a chi Router via ServeHTTP.
//
// token is the Authorization Bearer value used when the corpus selects mode 1.
// Pass an empty string to suppress even when mode 1 is selected (treated as absent).
//
// BuildRequest never panics regardless of corpus content, including empty or
// 1-byte slices. Missing bytes fall back to index 0.
func BuildRequest(corpus []byte, token string) *http.Request {
	// Helper to safely index corpus with a fallback value.
	byteAt := func(i int) byte {
		if i < len(corpus) {
			return corpus[i]
		}
		return 0
	}

	method := knownMethods[int(byteAt(0))%len(knownMethods)]

	pathIdx := int(byteAt(1)) % len(knownPaths)
	basePath := knownPaths[pathIdx]

	// Append remaining corpus bytes after the first 4 as a path suffix.
	// This exercises path-parameter parsing (e.g., /:namespace/:name) with
	// arbitrary bytes while keeping the route prefix recognizable.
	var pathSuffix string
	if len(corpus) > 4 {
		// URL-encode only the null byte and slash to avoid accidentally
		// hitting entirely different routes; keep other bytes raw so the
		// fuzzer can explore URL-decode edge cases in chi.
		suffix := corpus[4:]
		buf := make([]byte, 0, len(suffix))
		for _, b := range suffix {
			switch b {
			case 0x00:
				buf = append(buf, '%', '0', '0')
			default:
				buf = append(buf, b)
			}
		}
		pathSuffix = string(buf)
	}
	path := basePath + pathSuffix

	// Remaining bytes (after first 4) become the body. We re-use all bytes
	// after the header-control bytes rather than splitting again.
	var body []byte
	if len(corpus) > 4 {
		body = corpus[4:]
	}

	var req *http.Request
	if len(body) > 0 {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}

	// Header toggles from byte[2].
	headerBits := byteAt(2) % 4
	if headerBits&1 != 0 {
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
	}
	if headerBits&2 != 0 {
		req.Header.Set("X-Cluster-ID", "local")
	}

	// Content-Type: set when body is present to exercise JSON decoding paths.
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	// Authorization mode from byte[3].
	authMode := byteAt(3) % 3
	switch authMode {
	case 0:
		// absent — no Authorization header
	case 1:
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	case 2:
		req.Header.Set("Authorization", GarbageToken)
	}

	return req
}

// BuildAuthenticatedRequest constructs a GET request to path with the given
// Bearer token and X-Requested-With header set. Use for the "happy path"
// seed corpus in fuzz targets.
func BuildAuthenticatedRequest(method, path, token string, body []byte) *http.Request {
	var req *http.Request
	if len(body) > 0 {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}
