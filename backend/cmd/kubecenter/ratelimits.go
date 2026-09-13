package main

import "time"

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

// authRateLimit returns the per-IP budget for the shared auth limiter that
// guards login, refresh and setup.
//
// Production keeps 5/min. That number is a security control — it is what makes
// credential stuffing expensive — and nothing here relaxes it.
//
// Dev was already relaxed to 60/min, which was sized for a human clicking
// through a login or two. It is too tight for the e2e suite, because refresh
// shares this bucket: the browser holds its access token only in memory, so
// every full page load starts with no token and spends one /auth/refresh
// re-establishing it from the httpOnly cookie. A spec file that walks 25
// resource tables therefore spends 25 of the 60 on page loads alone, before
// any test does an actual login — and when the bucket empties, the refresh
// fails, api.ts treats that as a dead session and redirects to /login, so the
// spec fails somewhere unrelated to what it was testing.
//
// 300/min matches the shape the YAML/wizard and webhook limiters already use
// in dev, and for the same reason: one runner IP drives the entire suite
// within a few minutes, which is nothing like the traffic the production
// budget is defending against.
func authRateLimit(dev bool) (int, time.Duration) {
	if dev {
		return 300, time.Minute
	}
	return 5, time.Minute
}
