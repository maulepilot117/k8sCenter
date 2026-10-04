package main

import (
	"time"

	"github.com/kubecenter/kubecenter/internal/server"
)

// yamlRateLimit returns the per-IP budget for the shared YAML/wizard rate
// limiter: the limiter guarding /yaml/*, /wizards/*, the secret routes, and
// every other route group that wires yamlRL in routes.go.
//
// Production keeps a deliberately tight budget — these endpoints render and
// apply cluster manifests, so they are worth protecting from a single noisy
// source.
//
// Dev is relaxed for the same reason the auth limiter is (see the cfg.Dev
// branch that builds rateLimiter in main). The difference is that the auth
// limiter already had its dev relaxation and this one did not, which is what
// made the e2e suite flaky: every one of the 16 guarded route groups is
// driven from a single runner IP inside a few minutes, so the suite's own
// traffic exhausted a production-sized bucket. The resulting 429 on a wizard
// preview made WizardReviewStep render its error branch — which has no Apply
// button — so the Playwright wait for Apply could not succeed at any timeout,
// and the retries re-hit the still-empty bucket.
//
// Dev is not a security boundary; the webhook limiter already uses the same
// 300/min shape for the same reason.
func yamlRateLimit(dev bool) (int, time.Duration) {
	if dev {
		return 300, time.Minute
	}
	return 30, time.Minute
}

// changesRateLimit returns the budget of the tracked-change receipt limiter
// (/changes/*). The limiter is keyed per authenticated user, not per IP: in the
// default install every request reaches the backend through the frontend BFF,
// which forwards no client address, so a per-IP bucket would be one bucket for
// the whole installation (see middleware.RateLimitByUser).
//
// Why a separate bucket from the shared YAML one: the UI polls
// GET /changes/{id}/verification every 5s per open receipt, and that polling
// must not eat the budget of /yaml/apply and the wizard previews that produce
// the receipts.
//
// Budget arithmetic (per user, per minute). A poll is 12 req/min per open
// receipt. It is not a cheap read: verification loads the receipt, resolves the
// target cluster, and does one impersonated GET per recorded object (up to
// about 100 for a large bundle) plus access checks, then persists the verdict.
// The UI shows one receipt at a time, so the steady load is 12 req/min, and a
// user flipping between receipts or with a second tab stays near 24-36. 60/min
// allows about five receipts polled at once, with headroom for list/detail
// navigation, while capping one user at 1 req/s of verification work (at most
// ~100 GETs each) rather than the 2 req/s a 120/min bucket would allow. The
// shared-identity worst case is therefore bounded per person, not per install.
// POST /changes/ownership is the one other costly route (GitOps ownership for up
// to 50 objects) and is bounded per request by the handler's object cap and 20s
// timeout.
//
// Dev follows the yamlRateLimit rule: the e2e suite is one user from one IP,
// serial with retries, so it gets 10x the production budget.
func changesRateLimit(dev bool) (int, time.Duration) {
	if dev {
		return 600, time.Minute
	}
	return server.DefaultChangesRateLimit, time.Minute
}
