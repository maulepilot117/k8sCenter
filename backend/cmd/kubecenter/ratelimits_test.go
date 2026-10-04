package main

import (
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/server"
)

// The e2e suite drives all 16 yamlRateLimiter-guarded route groups from a
// single runner IP, serially, inside a few minutes. A production-sized budget
// applied to that traffic returns 429 on a wizard preview, which makes
// WizardReviewStep render its error branch — a branch with no Apply button —
// so the Playwright wait for Apply cannot succeed at any timeout. Retries
// re-hit the exhausted bucket, so all attempts fail together.
//
// Guard the property that actually prevents the flake: dev must be far enough
// above the suite's burst that ordinary variation cannot reach it, while
// production keeps the real protection.
func TestYAMLRateLimit_DevIsRelaxedForE2E(t *testing.T) {
	const (
		prodBudget = 30
		// Measured shape of the suite: 16 guarded route groups, the
		// parameterized wizard test (4 wizards x preview per step
		// transition), eso-templates, yaml-apply, and up to 2 Playwright
		// retries that replay all of it. 30/min is inside that range;
		// anything at or below ~100/min leaves the flake reachable.
		minimumDevBudget = 100
	)

	devBudget, devWindow := yamlRateLimit(true)
	if devWindow != time.Minute {
		t.Fatalf("dev window = %v; want %v", devWindow, time.Minute)
	}
	if devBudget < minimumDevBudget {
		t.Fatalf("dev budget = %d req/%v; want at least %d so the e2e suite cannot exhaust it",
			devBudget, devWindow, minimumDevBudget)
	}

	prodBudgetGot, prodWindow := yamlRateLimit(false)
	if prodWindow != time.Minute {
		t.Fatalf("prod window = %v; want %v", prodWindow, time.Minute)
	}
	if prodBudgetGot != prodBudget {
		t.Fatalf("prod budget = %d; want %d — production protection must not change",
			prodBudgetGot, prodBudget)
	}

	if devBudget <= prodBudgetGot {
		t.Fatalf("dev budget %d must exceed prod budget %d", devBudget, prodBudgetGot)
	}
}

// changesRateLimit is the per-user budget of the /changes limiter. The UI polls
// verification every 5s (12/min per open receipt), so production must leave room
// for several receipts at once, and must stay tighter than the 2 req/s that a
// verification (up to ~100 live GETs each) would be allowed at 120/min. The e2e
// suite is one user from one IP, serial with retries, so dev must be far above
// that burst; same reasoning and guard shape as TestYAMLRateLimit above.
func TestChangesRateLimit_BudgetsAreGuarded(t *testing.T) {
	const (
		pollsPerReceiptPerMin = 12
		minConcurrentReceipts = 4
		maxProdBudget         = 100
		minimumDevBudget      = 300
	)

	prod, prodWindow := changesRateLimit(false)
	if prodWindow != time.Minute {
		t.Fatalf("prod window = %v; want %v", prodWindow, time.Minute)
	}
	if prod < pollsPerReceiptPerMin*minConcurrentReceipts {
		t.Fatalf("prod budget %d/min cannot sustain %d receipts polled every 5s (%d/min)",
			prod, minConcurrentReceipts, pollsPerReceiptPerMin*minConcurrentReceipts)
	}
	if prod > maxProdBudget {
		t.Fatalf("prod budget %d/min exceeds %d: each verification can do ~100 live GETs, keep it bounded", prod, maxProdBudget)
	}

	dev, devWindow := changesRateLimit(true)
	if devWindow != time.Minute {
		t.Fatalf("dev window = %v; want %v", devWindow, time.Minute)
	}
	if dev < minimumDevBudget {
		t.Fatalf("dev budget %d/min; want at least %d so the e2e suite cannot exhaust it", dev, minimumDevBudget)
	}
	if dev <= prod {
		t.Fatalf("dev budget %d must exceed prod budget %d", dev, prod)
	}
	if prod != server.DefaultChangesRateLimit {
		t.Fatalf("prod budget %d != server.DefaultChangesRateLimit %d; the unwired fallback must match", prod, server.DefaultChangesRateLimit)
	}
}
