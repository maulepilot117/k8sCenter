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
