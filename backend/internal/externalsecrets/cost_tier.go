package externalsecrets

import (
	"strings"
	"time"
)

// ESO Prometheus metric names. Lifted out as constants so the per-store
// metrics handler and any future dashboard wiring share a single source of
// truth. Values match the labels exported by ESO 0.9+
// (`externalsecret_sync_calls_total`, `externalsecret_sync_calls_error`).
const (
	MetricSyncCallsTotal = "externalsecret_sync_calls_total"
	MetricSyncCallsError = "externalsecret_sync_calls_error"
)

// providerRateCard captures the public list price for a paid-tier secret
// store, plus when that price was last hand-revised. The data lives in a Go
// map literal (no rate_cards.json) so keys are compiler-checked and there's
// no JSON-unmarshal surface for an attacker to coerce into bogus rates.
//
// Operations is keyed by ESO call type (`get`, `list`) → USD per million
// requests. Self-hosted providers (Vault, Kubernetes provider) intentionally
// have no entry — EstimateCost returns nil for them and the frontend
// suppresses the cost card.
type providerRateCard struct {
	Operations  map[string]float64 // op → USD per 1M requests
	Currency    string             // ISO 4217
	LastUpdated time.Time
}

// rateCards is the single source of truth for billing-tier pricing. Keys are
// internal "billing provider" identifiers, NOT the raw ESO `provider` key —
// AWS expands into two billing identifiers (Secrets Manager vs Parameter
// Store advanced tier), so resolveBillingProvider performs the disambiguation
// before lookup.
//
// Last verified 2026-04-30 against vendor list pricing pages. All prices are
// in USD per million API requests; rates as of that date and documented as
// "not connected to live billing" everywhere they surface in the UI.
var rateCards = map[string]providerRateCard{
	"aws-secrets-manager": {
		Operations:  map[string]float64{"get": 0.05, "list": 0.05},
		Currency:    "USD",
		LastUpdated: time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
	},
	"aws-parameter-store-advanced": {
		// Standard-tier Parameter Store is free; only advanced parameters
		// charge per request. We can't tell tier from the SecretStore spec
		// alone, so the Phase F card surfaces this as an "advanced tier
		// estimate" caveat in the UI.
		Operations:  map[string]float64{"get": 0.05},
		Currency:    "USD",
		LastUpdated: time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
	},
	"gcp-secret-manager": {
		// $0.03 per 10,000 access operations → $3.00 per 1M.
		Operations:  map[string]float64{"get": 3.00},
		Currency:    "USD",
		LastUpdated: time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
	},
	"azure-key-vault": {
		// $0.03 per 10,000 transactions → $3.00 per 1M (standard tier).
		Operations:  map[string]float64{"get": 3.00, "list": 3.00},
		Currency:    "USD",
		LastUpdated: time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
	},
}

// CostEstimate is the API shape returned alongside per-store rate metrics.
// All fields are pointers/strings so a "no data" or "self-hosted" response
// distinguishes itself from an explicit zero — frontend suppresses the cost
// card on nil but still renders the rate panel.
type CostEstimate struct {
	BillingProvider string    `json:"billingProvider"` // "aws-secrets-manager" etc; empty for self-hosted
	Currency        string    `json:"currency,omitempty"`
	USDPerMillion   float64   `json:"usdPerMillion,omitempty"` // weighted average across the operations we count
	Estimated24h    float64   `json:"estimated24h,omitempty"`  // USD over the 24h window
	LastUpdated     time.Time `json:"lastUpdated,omitzero"`    // rate-card snapshot date
}

// ResolveBillingProvider maps an ESO `provider` key + its spec sub-block to
// the internal billing identifier used by rateCards. Returns "" for
// self-hosted providers (Vault, Kubernetes, Akeyless, etc.) and unknown
// providers — both signal "no cost estimate" to the caller.
//
// AWS disambiguation: the ESO `aws` provider carries a `service` field whose
// value is "SecretsManager" or "ParameterStore". We treat ParameterStore as
// the advanced-tier billing card; standard-tier is free and surfaces as
// "no charge" in the UI via the same caveat caption.
func ResolveBillingProvider(provider string, providerSpec map[string]any) string {
	switch provider {
	case "aws":
		if providerSpec != nil {
			if svc, ok := providerSpec["service"].(string); ok {
				if strings.EqualFold(svc, "ParameterStore") {
					return "aws-parameter-store-advanced"
				}
			}
		}
		return "aws-secrets-manager"
	case "gcpsm":
		return "gcp-secret-manager"
	case "azurekv":
		return "azure-key-vault"
	default:
		return ""
	}
}

// EstimateCost returns the projected USD spend for a request volume over a
// window, given a billing-provider key. Returns nil when the provider has no
// rate card (self-hosted or unknown) — callers MUST treat nil as "suppress
// the cost card" rather than fabricating a zero.
//
// The estimate uses the average list-price across the operations we record.
// ESO doesn't tag get-vs-list at the metric level, so we treat the metric
// as a generic "API call" and pick the worst-case (max) op cost — better
// to over-estimate spend than to under-estimate it on a billing screen.
func EstimateCost(billingProvider string, requestCount float64, window time.Duration) *CostEstimate {
	card, ok := rateCards[billingProvider]
	if !ok {
		return nil
	}
	if requestCount < 0 {
		requestCount = 0
	}

	var maxRate float64
	for _, r := range card.Operations {
		if r > maxRate {
			maxRate = r
		}
	}

	// Project to a 24h window for the dashboard card. Caller passes the
	// observation window (e.g., 24h) and the absolute count over that
	// window; we rescale only if the window differs.
	scaled := requestCount
	if window > 0 && window != 24*time.Hour {
		scaled = requestCount * (float64(24*time.Hour) / float64(window))
	}

	return &CostEstimate{
		BillingProvider: billingProvider,
		Currency:        card.Currency,
		USDPerMillion:   maxRate,
		Estimated24h:    (scaled / 1_000_000) * maxRate,
		LastUpdated:     card.LastUpdated,
	}
}
