package scanning

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// These helpers returned `err == nil && can`, which reads a check that could
// not run as a refusal. Both scanners refusing is what the handler turns into
// a 403, and a namespace the viewer genuinely holds no grant on is
// indistinguishable at the card from one where the review simply broke — so a
// vulnerability card could report nothing to see because its own permission
// check failed. The error is now returned so the handler can answer 500.
func erroringScanHandler() *Handler {
	return &Handler{
		AccessChecker: resources.NewErroringAccessChecker(errors.New("apiserver unavailable")),
		Logger:        slog.Default(),
	}
}

func scanUser() *auth.User {
	return &auth.User{KubernetesUsername: "dev", KubernetesGroups: []string{"devs"}}
}

func TestScannerAccess_ErrorIsNotADenial(t *testing.T) {
	h := erroringScanHandler()

	for _, tc := range []struct {
		name string
		call func() (bool, error)
	}{
		{"trivy", func() (bool, error) { return h.canAccessTrivy(context.Background(), scanUser(), "prod") }},
		{"kubescape", func() (bool, error) { return h.canAccessKubescape(context.Background(), scanUser(), "prod") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			can, err := tc.call()
			if err == nil {
				t.Fatal("a failed access review must surface as an error, not a refusal")
			}
			if can {
				t.Fatal("a failed review must not grant access either")
			}
		})
	}
}

func TestScannerAccess_DenialIsStillADenial(t *testing.T) {
	h := &Handler{AccessChecker: resources.NewAlwaysDenyAccessChecker(), Logger: slog.Default()}

	can, err := h.canAccessTrivy(context.Background(), scanUser(), "prod")
	if err != nil {
		t.Fatalf("a plain denial is an answer, not an error: %v", err)
	}
	if can {
		t.Fatal("denied must be denied")
	}
}
