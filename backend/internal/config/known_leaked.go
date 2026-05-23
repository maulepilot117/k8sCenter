package config

// KnownLeakedSecrets are values committed to the repo's homelab values file
// prior to the 2026-05-22 security audit (finding P0-1). The startup guard
// (in cmd/kubecenter/main.go) refuses to boot in production with any of
// these. Keep this list in sync with helm/kubecenter/templates/_validate.tpl.
var KnownLeakedSecrets = []string{
	"homelab-jwt-secret-for-k8scenter-minimum-32-bytes",
	"homelab-setup-token",
	"k8sC3nterDB2026",
}

// IsKnownLeakedSecret reports whether s matches any of the known-leaked
// homelab values. Constant-time comparison is not required here — leaked
// strings are public, so the check itself is not a side-channel target.
func IsKnownLeakedSecret(s string) bool {
	for _, leaked := range KnownLeakedSecrets {
		if s == leaked {
			return true
		}
	}
	return false
}
