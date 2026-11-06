package externalsecrets

import (
	"crypto/sha3"
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// esoDataHashAnnotation is the annotation ESO writes on every Secret it
// manages: a hash of the Secret's data as ESO wrote it
// (esv1.AnnotationDataHash in external-secrets).
const esoDataHashAnnotation = "reconcile.external-secrets.io/data-hash"

// esoDataHash computes ESO's data hash for a Secret's data. It mirrors
// esutils.ObjectHash in external-secrets (checked against v2.4.1): SHA3-224
// of fmt's %+v rendering, which prints a map with its keys sorted. Pinned by
// TestESODataHash_MatchesESO.
func esoDataHash(data map[string][]byte) string {
	return fmt.Sprintf("%x", sha3.Sum224([]byte(fmt.Sprintf("%+v", data))))
}

// computeDriftStatus reports whether a Secret ESO synced has been changed
// since. ESO records a hash of the data it wrote in esoDataHashAnnotation and
// treats a Secret whose data no longer matches it as invalid (isSecretValid in
// its controller); this is the same comparison.
//
// It deliberately ignores the ExternalSecret's status.syncedResourceVersion:
// that is "<generation>-<hash of the ExternalSecret's metadata>", a version of
// the ExternalSecret, and never equals the Secret's resourceVersion, which
// itself moves on every write. Comparing the two read every Secret as drifted.
//
// A Secret without the annotation (not written by ESO, or the annotation was
// removed) is Unknown with DriftReasonNoDataHash rather than a guess.
func computeDriftStatus(secret *corev1.Secret) (DriftStatus, string) {
	want, ok := secret.Annotations[esoDataHashAnnotation]
	if !ok || want == "" {
		return DriftUnknown, DriftReasonNoDataHash
	}
	if esoDataHash(secret.Data) == want {
		return DriftInSync, ""
	}
	return DriftDrifted, ""
}
