// Package certmanager provides cert-manager integration for k8sCenter.
package certmanager

import (
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GVR constants for cert-manager.io/v1 resources.
var (
	CertificateGVR = schema.GroupVersionResource{
		Group: "cert-manager.io", Version: "v1", Resource: "certificates",
	}
	IssuerGVR = schema.GroupVersionResource{
		Group: "cert-manager.io", Version: "v1", Resource: "issuers",
	}
	ClusterIssuerGVR = schema.GroupVersionResource{
		Group: "cert-manager.io", Version: "v1", Resource: "clusterissuers",
	}
	CertificateRequestGVR = schema.GroupVersionResource{
		Group: "cert-manager.io", Version: "v1", Resource: "certificaterequests",
	}
)

// GVR constants for acme.cert-manager.io/v1 resources.
var (
	OrderGVR = schema.GroupVersionResource{
		Group: "acme.cert-manager.io", Version: "v1", Resource: "orders",
	}
	ChallengeGVR = schema.GroupVersionResource{
		Group: "acme.cert-manager.io", Version: "v1", Resource: "challenges",
	}
)

// Status represents the computed lifecycle state of a Certificate.
type Status string

const (
	StatusReady    Status = "Ready"
	StatusIssuing  Status = "Issuing"
	StatusFailed   Status = "Failed"
	StatusExpiring Status = "Expiring"
	StatusExpired  Status = "Expired"
	StatusUnknown  Status = "Unknown"
)

// WarningThresholdDays is the package-default warning threshold in days.
// Per-cert / per-issuer overrides can be set via annotation; see
// AnnotationWarnThreshold.
const WarningThresholdDays = 30

// CriticalThresholdDays is the package-default critical threshold in days.
// Per-cert / per-issuer overrides can be set via annotation; see
// AnnotationCriticalThreshold.
const CriticalThresholdDays = 7

// Annotation keys for per-Certificate / per-Issuer / per-ClusterIssuer
// expiry-threshold overrides. The same key strings apply on all three
// resource kinds so operators don't have to memorize different names per
// kind. Values must be positive integers (days); invalid values are
// logged and silently fall through to the next layer of the resolution
// chain.
const (
	AnnotationWarnThreshold     = "kubecenter.io/cert-warn-threshold-days"
	AnnotationCriticalThreshold = "kubecenter.io/cert-critical-threshold-days"
)

// ThresholdSource enumerates which layer of the resolution chain
// supplied a certificate's effective thresholds. Used by the UI to
// explain "Warns at: 60d (from Issuer X)".
type ThresholdSource string

const (
	ThresholdSourceDefault       ThresholdSource = "default"
	ThresholdSourceCertificate   ThresholdSource = "certificate"
	ThresholdSourceIssuer        ThresholdSource = "issuer"
	ThresholdSourceClusterIssuer ThresholdSource = "clusterissuer"
)

// CertManagerStatus is returned by GET /certmanager/status.
type CertManagerStatus struct {
	Detected    bool      `json:"detected"`
	Namespace   string    `json:"namespace,omitempty"`
	Version     string    `json:"version,omitempty"`
	LastChecked time.Time `json:"lastChecked"`
}

// IssuerRef identifies the Issuer or ClusterIssuer that signs a certificate.
type IssuerRef struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Group string `json:"group"`
}

// Certificate is the API representation of a cert-manager Certificate resource.
//
// WarningThresholdDays / CriticalThresholdDays / ThresholdSource carry the
// resolved per-cert thresholds. After normalizeCertificate runs, the
// fields hold parsed cert-level annotation values (or zero if absent or
// invalid). After ApplyThresholds runs, they hold the final resolved
// values that drive Status, the /expiring filter, and the poller bucket.
// Status itself is empty until ApplyThresholds runs — earlier callers
// must not rely on it.
type Certificate struct {
	Name                  string     `json:"name"`
	Namespace             string     `json:"namespace"`
	Status                Status     `json:"status"`
	Reason                string     `json:"reason,omitempty"`
	Message               string     `json:"message,omitempty"`
	IssuerRef             IssuerRef  `json:"issuerRef"`
	SecretName            string     `json:"secretName"`
	DNSNames              []string   `json:"dnsNames,omitempty"`
	CommonName            string     `json:"commonName,omitempty"`
	Duration              string     `json:"duration,omitempty"`
	RenewBefore           string     `json:"renewBefore,omitempty"`
	NotBefore             *time.Time `json:"notBefore,omitempty"`
	NotAfter              *time.Time `json:"notAfter,omitempty"`
	RenewalTime           *time.Time `json:"renewalTime,omitempty"`
	DaysRemaining         *int       `json:"daysRemaining,omitempty"`
	WarningThresholdDays  int        `json:"warningThresholdDays,omitempty"`
	CriticalThresholdDays int        `json:"criticalThresholdDays,omitempty"`
	ThresholdSource       ThresholdSource `json:"thresholdSource,omitempty"`
	UID                   string     `json:"uid"`
}

// Issuer is the API representation of a cert-manager Issuer or ClusterIssuer.
//
// WarningThresholdDays / CriticalThresholdDays carry parsed annotation
// values when present. nil pointers mean "not set on this issuer";
// resolution falls through to the next layer.
type Issuer struct {
	Name                  string    `json:"name"`
	Namespace             string    `json:"namespace,omitempty"`
	Scope                 string    `json:"scope"` // "Namespaced" or "Cluster"
	Type                  string    `json:"type"`  // "ACME", "CA", "Vault", "SelfSigned", "Unknown"
	Ready                 bool      `json:"ready"`
	Reason                string    `json:"reason,omitempty"`
	Message               string    `json:"message,omitempty"`
	ACMEEmail             string    `json:"acmeEmail,omitempty"`
	ACMEServer            string    `json:"acmeServer,omitempty"`
	WarningThresholdDays  *int      `json:"warningThresholdDays,omitempty"`
	CriticalThresholdDays *int      `json:"criticalThresholdDays,omitempty"`
	UID                   string    `json:"uid"`
	UpdatedAt             time.Time `json:"updatedAt"`
}

// CertificateRequest is the API representation of a cert-manager CertificateRequest.
type CertificateRequest struct {
	Name       string     `json:"name"`
	Namespace  string     `json:"namespace"`
	Status     Status     `json:"status"`
	Reason     string     `json:"reason,omitempty"`
	Message    string     `json:"message,omitempty"`
	IssuerRef  IssuerRef  `json:"issuerRef"`
	CreatedAt  time.Time  `json:"createdAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	UID        string     `json:"uid"`
}

// Order is the API representation of an ACME Order resource.
type Order struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	State     string    `json:"state"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UID       string    `json:"uid"`
	CRName    string    `json:"crName,omitempty"` // owning Certificate name
}

// Challenge is the API representation of an ACME Challenge resource.
type Challenge struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	Type      string    `json:"type"`
	State     string    `json:"state"`
	Reason    string    `json:"reason,omitempty"`
	DNSName   string    `json:"dnsName"`
	CreatedAt time.Time `json:"createdAt"`
	UID       string    `json:"uid"`
	OrderName string    `json:"orderName,omitempty"` // owning Order name
}

// CertificateDetail aggregates a Certificate with its related sub-resources.
type CertificateDetail struct {
	Certificate         Certificate          `json:"certificate"`
	CertificateRequests []CertificateRequest `json:"certificateRequests"`
	Orders              []Order              `json:"orders"`
	Challenges          []Challenge          `json:"challenges"`
}

// ExpiringCertificate is a summary entry used for expiry notifications.
type ExpiringCertificate struct {
	Namespace     string    `json:"namespace"`
	Name          string    `json:"name"`
	UID           string    `json:"uid"`
	IssuerName    string    `json:"issuerName"`
	SecretName    string    `json:"secretName"`
	NotAfter      time.Time `json:"notAfter"`
	DaysRemaining int       `json:"daysRemaining"`
	Severity      string    `json:"severity"` // "warning" or "critical"
}
