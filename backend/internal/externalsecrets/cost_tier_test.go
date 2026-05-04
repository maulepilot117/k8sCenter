package externalsecrets

import (
	"testing"
	"time"
)

func TestResolveBillingProvider(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		spec     map[string]any
		want     string
	}{
		{"aws default → secrets-manager", "aws", nil, "aws-secrets-manager"},
		{"aws SecretsManager service explicit", "aws", map[string]any{"service": "SecretsManager"}, "aws-secrets-manager"},
		{"aws ParameterStore service", "aws", map[string]any{"service": "ParameterStore"}, "aws-parameter-store-advanced"},
		{"aws ParameterStore lowercase", "aws", map[string]any{"service": "parameterstore"}, "aws-parameter-store-advanced"},
		{"gcpsm", "gcpsm", nil, "gcp-secret-manager"},
		{"azurekv", "azurekv", nil, "azure-key-vault"},
		{"vault is self-hosted", "vault", nil, ""},
		{"kubernetes provider is self-hosted", "kubernetes", nil, ""},
		{"unknown provider", "made-up", nil, ""},
		{"empty provider", "", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveBillingProvider(tc.provider, tc.spec); got != tc.want {
				t.Fatalf("ResolveBillingProvider(%q, %v) = %q; want %q", tc.provider, tc.spec, got, tc.want)
			}
		})
	}
}

func TestEstimateCost_HappyPath(t *testing.T) {
	got := EstimateCost("aws-secrets-manager", 1_000_000, 24*time.Hour)
	if got == nil {
		t.Fatal("expected estimate, got nil")
	}
	if got.BillingProvider != "aws-secrets-manager" {
		t.Errorf("billing provider = %q", got.BillingProvider)
	}
	if got.Currency != "USD" {
		t.Errorf("currency = %q", got.Currency)
	}
	// 1M requests at $0.05/M = $0.05.
	if got.Estimated24h < 0.04 || got.Estimated24h > 0.06 {
		t.Errorf("estimated24h = %v; want ~0.05", got.Estimated24h)
	}
	if got.LastUpdated.IsZero() {
		t.Error("LastUpdated must be populated from rate card")
	}
}

func TestEstimateCost_SelfHostedReturnsNil(t *testing.T) {
	if got := EstimateCost("", 1000, 24*time.Hour); got != nil {
		t.Errorf("expected nil for self-hosted; got %+v", got)
	}
	if got := EstimateCost("vault", 1000, 24*time.Hour); got != nil {
		t.Errorf("expected nil for vault; got %+v", got)
	}
}

func TestEstimateCost_RescaleWindow(t *testing.T) {
	// 100k over 1h projects to 2.4M over 24h → at $3/M for GCP = $7.20.
	got := EstimateCost("gcp-secret-manager", 100_000, time.Hour)
	if got == nil {
		t.Fatal("expected estimate")
	}
	if got.Estimated24h < 7.0 || got.Estimated24h > 7.4 {
		t.Errorf("estimated24h = %v; want ~7.20", got.Estimated24h)
	}
}

func TestEstimateCost_NegativeCountClampedToZero(t *testing.T) {
	got := EstimateCost("aws-secrets-manager", -500, 24*time.Hour)
	if got == nil {
		t.Fatal("expected estimate")
	}
	if got.Estimated24h != 0 {
		t.Errorf("estimated24h = %v; want 0 for negative input", got.Estimated24h)
	}
}

func TestRateCards_AllPopulatedFields(t *testing.T) {
	for key, card := range rateCards {
		if card.Currency == "" {
			t.Errorf("%s: currency empty", key)
		}
		if card.LastUpdated.IsZero() {
			t.Errorf("%s: LastUpdated zero", key)
		}
		if len(card.Operations) == 0 {
			t.Errorf("%s: no operations", key)
		}
		for op, rate := range card.Operations {
			if rate <= 0 {
				t.Errorf("%s/%s: rate = %v; must be positive", key, op, rate)
			}
		}
	}
}
