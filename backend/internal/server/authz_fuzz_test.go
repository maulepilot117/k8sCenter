package server

// FuzzAuthzEnforcement exercises three auth/authz invariants using the
// existing testServer / doRequest helpers:
//
//  1. Unauthenticated requests to protected routes must never return 2xx.
//  2. State-changing methods (POST/PUT/PATCH/DELETE) with a valid token but
//     without X-Requested-With must be rejected with 403 (CSRF).
//  3. Client-supplied Impersonate-User/Impersonate-Group headers on an
//     unauthenticated request must still be rejected with 401.
//  4. No panic on any fuzz-generated request (crash-safety).
//
// CSRF status confirmed from middleware/auth.go CSRF(): returns 403 for
// POST/PUT/PATCH/DELETE missing X-Requested-With.
// Exempt methods: GET, HEAD, OPTIONS, (CONNECT, TRACE).

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// protectedPaths is the set of GET-readable routes in the auth-only testServer
// that are reachable without k8s dependencies. /api/v1/auth/me requires only a
// valid JWT and is the primary GET oracle target. /api/v1/cluster/info is also
// safe.
var fuzzProtectedPaths = []string{
	"/api/v1/auth/me",
	"/api/v1/cluster/info",
}

// csrfTargetPath is a genuinely state-changing protected route the auth-only
// testServer can reach without k8s. PUT /api/v1/settings/ maps to
// handleUpdateAppSettings (routes.go:244) — admin-only, no k8s deps. Unlike
// /auth/me (GET-only → 405 on PUT before CSRF fires), this route ACCEPTS PUT,
// so a PUT here actually reaches the CSRF middleware and exercises the real
// 403 path. State-changing fuzz methods are routed here so oracle-2 isn't
// hollow.
const csrfTargetPath = "/api/v1/settings/"

// stateChangingMethods are the HTTP methods that trigger CSRF enforcement.
var stateChangingMethods = []string{
	http.MethodPost,
	http.MethodPut,
	http.MethodPatch,
	http.MethodDelete,
}

// readMethods are exempt from CSRF enforcement.
var readMethods = []string{
	http.MethodGet,
	http.MethodHead,
}

// allMethods is the combined slice used by the fuzz loop to map methodIdx to an
// HTTP method.  Declared once at package level to avoid a per-iteration alloc.
var allMethods = append(stateChangingMethods, readMethods...)

// sharedSrv, validToken, and initOnce are package-level so the single server
// instance is reused across all fuzz iterations.
var (
	sharedSrv  *Server
	validToken string
	initOnce   sync.Once
)

// FuzzAuthzEnforcement is the fuzz entry point. The corpus encodes:
//   - pathIdx:       index into fuzzProtectedPaths (clamped)
//   - methodIdx:     index into a combined state-changing + read methods slice (clamped)
//   - authMode:      0=no token, 1=valid token, 2=garbage token, 3=impersonation headers (no token)
//   - csrfPresent:   whether X-Requested-With: XMLHttpRequest is included
//   - body:          arbitrary request body
func FuzzAuthzEnforcement(f *testing.F) {
	// Method index map: 0=POST 1=PUT 2=PATCH 3=DELETE 4=GET 5=HEAD.
	//
	// Seed: unauth GET → expect 401 (oracle 1)
	f.Add(0, 4, uint8(0), true, "")
	// Seed (ORACLE-1 AUTH-ISOLATION): unauth GET to /cluster/info (pathIdx 1).
	// handleClusterInfo has NO handler-level auth guard (unlike handleAuthMe) — it
	// derefs s.K8sClient immediately, so removing the Auth middleware would let
	// this no-token request reach the handler and 5xx (nil-deref in the auth-only
	// server); the 2xx-or-5xx rejection oracle catches that. This isolates the
	// Auth middleware instead of relying on a redundant handler guard.
	f.Add(1, 4, uint8(0), true, "")
	// Seed: mode-1 GET — exercises the pinning branch that rewrites a valid-token
	// non-state-changing request into the CSRF-rejection envelope (forced PUT,
	// csrf absent) → 403. No request in this fuzzer returns 2xx by design: every
	// path is engineered to be rejected in middleware (handlers nil-deref).
	f.Add(0, 4, uint8(1), true, "")
	// Seed (ORACLE-2 PRIMARY): authed PUT to /settings/ WITHOUT X-Requested-With
	// → MUST be 403 CSRF. methodIdx=1 (PUT), csrfPresent=false match what the
	// mode-1 override enforces; the settings route accepts PUT so the request
	// reaches the CSRF middleware (not a 405).
	f.Add(0, 1, uint8(1), false, `{}`)
	// Seed: authed POST without CSRF → state-changing, routed to /settings/.
	f.Add(0, 0, uint8(1), false, `{}`)
	// Seed: authed DELETE without CSRF → state-changing, routed to /settings/.
	f.Add(0, 3, uint8(1), false, "")
	// Seed: impersonation on unauth GET → expect 401 (oracle 3)
	f.Add(0, 4, uint8(3), true, "")
	// Seed: garbage token → expect 401
	f.Add(0, 4, uint8(2), true, "")
	// Seed: unauth PUT to /settings/ with CSRF → expect 401 (auth before CSRF)
	f.Add(0, 1, uint8(0), true, `{"x":"y"}`)
	// Seed: impersonation on unauth DELETE without CSRF → 401 (auth first, not 403)
	f.Add(0, 3, uint8(3), false, "")
	// Seed: mode-1 valid-token on the second path — also pinned to the
	// CSRF-rejection envelope (method forced to PUT, csrf absent) → 403.
	// methodIdx=1 (PUT) and csrfPresent=false match what the override enforces.
	f.Add(1, 1, uint8(1), false, "")

	// Build server and mint a valid token ONCE outside the fuzz loop.
	// testServer accepts *testing.T; use f.Fuzz's inner t for that.
	// We build here with a throwaway *testing.T via a sub-test so we can
	// reuse the server across iterations (the server is stateless for auth).

	// Use a sub-test to initialise so testServer's t.Helper cleanup runs correctly.
	f.Fuzz(func(t *testing.T,
		pathIdx int,
		methodIdx int,
		authMode uint8,
		csrfPresent bool,
		body string,
	) {
		// Lazy-init the shared server the first time we enter f.Fuzz.
		// sync.Once makes this safe for parallel fuzz workers.
		initOnce.Do(func() {
			sharedSrv = testServer(t)
			// loginAdmin creates the admin user and returns its access token —
			// the valid token oracle 2 needs for the CSRF-rejection path.
			tok, _ := loginAdmin(t, sharedSrv)
			validToken = tok
		})

		mode := authMode % 4

		// --- Build method ---
		method := allMethods[clamp(methodIdx, 0, len(allMethods)-1)]
		isStateChanging := isStateChangingMethod(method)

		// CRITICAL — keep every fuzzed request inside the middleware layer.
		//
		// The auth-only testServer has nil Informers / RBACChecker / k8s
		// clients, so handleAuthMe, handleClusterInfo, and
		// handleUpdateAppSettings all nil-deref the moment a request actually
		// REACHES them. chi's Recoverer would mask that as a 500, but a
		// recovered panic still undermines oracle-4 (crash-safety) and proves
		// nothing about authz. The task's premise is that protected requests
		// are rejected BY THE MIDDLEWARE before the handler runs — so we must
		// never construct a request that passes all of Auth+CSRF and lands on
		// a nil-deref handler.
		//
		// We enforce that by construction: a VALID token (mode 1) is only ever
		// used to drive a CSRF-rejected request — forced state-changing method
		// + forced absent X-Requested-With. That request is rejected at the
		// CSRF middleware (403) and never reaches the handler. All other modes
		// (no token / garbage / impersonation-only) are rejected at the Auth
		// middleware (401), also before the handler. Result: zero handler
		// entries, so any panic the fuzzer surfaces is a real middleware bug.
		csrf := csrfPresent
		if mode == 1 {
			// Valid token: pin to the CSRF-rejection envelope.
			if !isStateChanging {
				method = http.MethodPut // PUT is accepted by /settings/
				isStateChanging = true
			}
			csrf = false // guarantee CSRF rejects before the handler
		}

		// --- Build path ---
		// State-changing methods are routed to a route that ACCEPTS them
		// (PUT /api/v1/settings/) so the request reaches the CSRF middleware
		// instead of being short-circuited with 405 on a GET-only route. Read
		// methods exercise the GET-readable protected paths (rejected at Auth).
		var path string
		if isStateChanging {
			path = csrfTargetPath
		} else {
			path = fuzzProtectedPaths[clamp(pathIdx, 0, len(fuzzProtectedPaths)-1)]
		}

		// --- Build headers ---
		headers := map[string]string{}

		switch mode {
		case 0: // no token
			// headers stays empty for Authorization
		case 1: // valid token
			headers["Authorization"] = "Bearer " + validToken
		case 2: // garbage token
			headers["Authorization"] = "Bearer garbage.token.value"
		case 3: // impersonation headers, no real token
			headers["Impersonate-User"] = "system:admin"
			headers["Impersonate-Group"] = "system:masters"
			// Do NOT add a valid Authorization header.
		}

		if csrf {
			headers["X-Requested-With"] = "XMLHttpRequest"
		}

		// --- Execute ---
		w := doRequest(t, sharedSrv, method, path, body, headers)
		status := w.Code

		// ---------------------------------------------------------------
		// Oracle 1: No-token requests must be rejected by the Auth middleware
		// before any handler runs.  A 5xx means a handler was reached (auth
		// bypassed → nil-deref recover), which is a real authz bug.
		// Acceptable statuses: 401, 403, 405 (legitimate method-not-allowed on
		// a real route).  2xx or 5xx both indicate auth bypass.
		// mode 0 = no token, mode 3 = impersonation-only (no real token).
		// ---------------------------------------------------------------
		if mode == 0 || mode == 3 {
			if (status >= 200 && status < 300) || (status >= 500) {
				t.Errorf(
					"ORACLE-1 VIOLATED: no-token request returned %d (want 401/403/405)\n"+
						"  path=%s method=%s mode=%d csrf=%v body=%q\n"+
						"  response=%s",
					status, path, method, mode, csrf, body,
					w.Body.String(),
				)
			}
		}

		// ---------------------------------------------------------------
		// Oracle 1b: Garbage/expired token must also be rejected by Auth before
		// any handler runs.  Same rule: 2xx or 5xx indicate bypass.
		// Acceptable: 401, 403, 405.
		// ---------------------------------------------------------------
		if mode == 2 {
			if (status >= 200 && status < 300) || (status >= 500) {
				t.Errorf(
					"ORACLE-1b VIOLATED: garbage-token request returned %d (want 401/403/405)\n"+
						"  path=%s method=%s body=%q\n"+
						"  response=%s",
					status, path, method, body,
					w.Body.String(),
				)
			}
		}

		// ---------------------------------------------------------------
		// Oracle 2: State-changing request WITH a valid admin token but
		// WITHOUT X-Requested-With must be rejected with 403 (CSRF), never 2xx.
		//
		// mode 1 is pinned above to {state-changing method, csrf=false} and
		// state-changing methods route to PUT /settings/ which ACCEPTS PUT, so
		// the request reaches the CSRF middleware and we assert the EXACT 403
		// confirmed in middleware/auth.go. (If the fuzzer mutated the method to
		// POST/PATCH/DELETE the settings route 405s before CSRF — still non-2xx,
		// so we assert the exact 403 only for the PUT the route accepts.)
		// ---------------------------------------------------------------
		if mode == 1 && isStateChanging && !csrf {
			if status >= 200 && status < 300 {
				t.Errorf(
					"ORACLE-2 VIOLATED: CSRF-less authed state-changing request returned 2xx\n"+
						"  path=%s method=%s body=%q status=%d body=%s",
					path, method, body, status, w.Body.String(),
				)
			}
			if method == http.MethodPut && status != http.StatusForbidden {
				t.Errorf(
					"ORACLE-2 VIOLATED: CSRF-less authed PUT to %s expected 403, got %d\n"+
						"  body=%s",
					path, status, w.Body.String(),
				)
			}
		}

		// ---------------------------------------------------------------
		// Oracle 3 (explicit): An unauthenticated request carrying only
		// Impersonate-User/Impersonate-Group headers (no real token) must be
		// rejected by the Auth middleware.  A 2xx or 5xx proves the headers
		// bypassed authentication — that is the authz bug this oracle catches.
		// Acceptable: 401, 403, 405.
		// ---------------------------------------------------------------
		if mode == 3 {
			if (status >= 200 && status < 300) || (status >= 500) {
				t.Errorf(
					"ORACLE-3 VIOLATED: impersonation-without-token returned %d (want 401/403/405)\n"+
						"  path=%s method=%s status=%d response=%s",
					status, path, method, status, w.Body.String(),
				)
			}
		}

		// ---------------------------------------------------------------
		// Oracle 4 (crash-safety): reaching here without panic satisfies it.
		// ---------------------------------------------------------------
		_ = fmt.Sprintf("status=%d", status) // ensure status is read
	})
}

// clamp returns v clamped to [lo, hi].
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// isStateChangingMethod returns true for methods that trigger CSRF enforcement.
func isStateChangingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}
