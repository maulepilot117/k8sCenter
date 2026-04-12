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

// WarningThresholdDays is the number of days before expiry at which a
// certificate transitions from Ready to Expiring.
const WarningThresholdDays = 30

// CriticalThresholdDays is the number of days before expiry considered critical.
const CriticalThresholdDays = 7

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
type Certificate struct {
	Name          string            `json:"name"`
	Namespace     string            `json:"namespace"`
	Status        Status            `json:"status"`
	Reason        string            `json:"reason,omitempty"`
	Message       string            `json:"message,omitempty"`
	IssuerRef     IssuerRef         `json:"issuerRef"`
	SecretName    string            `json:"secretName"`
	DNSNames      []string          `json:"dnsNames,omitempty"`
	CommonName    string            `json:"commonName,omitempty"`
	Duration      string            `json:"duration,omitempty"`
	RenewBefore   string            `json:"renewBefore,omitempty"`
	NotBefore     *time.Time        `json:"notBefore,omitempty"`
	NotAfter      *time.Time        `json:"notAfter,omitempty"`
	RenewalTime   *time.Time        `json:"renewalTime,omitempty"`
	DaysRemaining *int              `json:"daysRemaining,omitempty"`
	UID           string            `json:"uid"`
}

// Issuer is the API representation of a cert-manager Issuer or ClusterIssuer.
type Issuer struct {
	Name       string    `json:"name"`
	Namespace  string    `json:"namespace,omitempty"`
	Scope      string    `json:"scope"` // "Namespaced" or "Cluster"
	Type       string    `json:"type"`  // "ACME", "CA", "Vault", "SelfSigned", "Unknown"
	Ready      bool      `json:"ready"`
	Reason     string    `json:"reason,omitempty"`
	Message    string    `json:"message,omitempty"`
	ACMEEmail  string    `json:"acmeEmail,omitempty"`
	ACMEServer string    `json:"acmeServer,omitempty"`
	UID        string    `json:"uid"`
	UpdatedAt  time.Time `json:"updatedAt"`
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
