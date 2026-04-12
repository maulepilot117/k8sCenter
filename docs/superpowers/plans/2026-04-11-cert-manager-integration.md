# Cert-Manager Integration Implementation Plan (Phase 11A)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the cert-manager observatory and lifecycle actions — Certificates/Issuers/ClusterIssuers list + detail, force-renew and re-issue actions, and a background expiry poller that emits threshold-crossing events through the existing Notification Center.

**Architecture:** New `backend/internal/certmanager/` package follows the established CRD-discovery pattern (Velero/Policy/GitOps). Dynamic client for CRDs. User impersonation on handler calls, service account on the background poller. Singleflight + 30s cache on list endpoints. Per-user RBAC filtering via `AccessChecker.CanAccessGroupResource`. Threshold poller maintains an in-memory dedupe map (`<uid>:<threshold>`) and emits `certificate.expiring`/`expired`/`failed` notifications. Frontend: 5 routes, 5 islands, new "Certificates" tab in Security SubNav.

**Tech Stack:** Go 1.26, chi, client-go dynamic client, singleflight; Deno 2.x + Fresh 2.x, Preact islands, Tailwind v4 with CSS custom property tokens; PostgreSQL not touched.

**Spec:** `docs/superpowers/specs/2026-04-11-cert-manager-integration-design.md`

---

## File Structure

### Backend — create

- `backend/internal/certmanager/types.go` — GVR constants, normalized types (`Certificate`, `Issuer`, `ClusterIssuer`, `CertificateRequest`, `Order`, `Challenge`), `CertManagerStatus`, `StatusEnum` helper, phase helpers
- `backend/internal/certmanager/discovery.go` — `Discoverer` struct, `Probe`/`Status`/`IsAvailable`, 5 min stale TTL (matches Velero)
- `backend/internal/certmanager/discovery_test.go` — discovery tests (fake discovery client)
- `backend/internal/certmanager/normalize.go` — `normalizeCertificate`, `normalizeIssuer`, `normalizeCertRequest`, `normalizeOrder`, `normalizeChallenge`, `computeStatus` (conditions → enum)
- `backend/internal/certmanager/normalize_test.go` — table-driven normalization tests
- `backend/internal/certmanager/handler.go` — `Handler` struct, `HandleStatus`, `HandleListCertificates`, `HandleGetCertificate`, `HandleListIssuers`, `HandleListClusterIssuers`, `HandleListExpiring`, singleflight cache
- `backend/internal/certmanager/actions.go` — `HandleRenew`, `HandleReissue`
- `backend/internal/certmanager/actions_test.go` — fake dynamic client tests
- `backend/internal/certmanager/poller.go` — `Poller` struct, tick loop, threshold crossing math, dedupe map lifecycle
- `backend/internal/certmanager/poller_test.go` — table-driven crossing + dedupe lifecycle tests
- `backend/internal/certmanager/rbac.go` — `filterCertificates`, `filterIssuers`, `filterClusterIssuers` helpers using `CanAccessGroupResource`

### Backend — modify

- `backend/internal/notifications/types.go` — add `SourceCertManager Source = "certmanager"`
- `backend/internal/server/server.go` (or wherever the Server struct lives) — add `CertManagerHandler *certmanager.Handler` and `CertManagerPoller *certmanager.Poller` fields
- `backend/internal/server/routes.go` — `registerCertManagerRoutes` helper, wired inside the existing `if s.VeleroHandler != nil {...}` block pattern
- `backend/cmd/kubecenter/main.go` — construct Discoverer/Handler/Poller, start poller goroutine alongside ClusterProber

### Frontend — create

- `frontend/lib/certmanager-types.ts` — TS interfaces mirroring backend JSON
- `frontend/components/ui/CertificateBadges.tsx` — `StatusBadge`, `IssuerTypeBadge`, `ExpiryBadge`
- `frontend/routes/security/certificates/index.tsx` — redirect to `./certificates`
- `frontend/routes/security/certificates/certificates.tsx` — list page shell
- `frontend/routes/security/certificates/certificates/[namespace]/[name].tsx` — detail page shell
- `frontend/routes/security/certificates/issuers.tsx` — issuers page shell
- `frontend/routes/security/certificates/expiring.tsx` — expiry dashboard page shell
- `frontend/islands/CertificatesList.tsx`
- `frontend/islands/CertificateDetail.tsx`
- `frontend/islands/IssuersList.tsx`
- `frontend/islands/ExpiryDashboard.tsx`
- `frontend/islands/CertificateStatusBanner.tsx`

### Frontend — modify

- `frontend/lib/api.ts` — add cert-manager API helpers (or ensure generic fetch works; likely no-op)
- `frontend/components/nav/SubNav.tsx` (or Security SubNav file) — add Certificates tab
- `frontend/islands/CommandPalette.tsx` — add "Certificates" and "Expiring certificates" quick actions

### E2E tests — create

- `e2e/tests/certificates.spec.ts` — one happy-path test, skips if status unavailable

### Docs

- `CLAUDE.md` — add Phase 11A entry under "Build Progress"; check off roadmap item #7; add Phase 11B to future features

---

## Task 1: Add SourceCertManager constant

**Files:**
- Modify: `backend/internal/notifications/types.go`

- [ ] **Step 1: Re-read the file**

Run: Read tool on `backend/internal/notifications/types.go` lines 1-20.

- [ ] **Step 2: Add the constant**

Edit the const block that contains the other `Source*` values to append:

```go
	SourceVelero      Source = "velero"
	SourceCertManager Source = "certmanager"
)
```

- [ ] **Step 3: Verify compile**

Run: `cd backend && go build ./internal/notifications/...`
Expected: no output, exit 0.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/notifications/types.go
git commit -m "feat(notifications): add SourceCertManager constant"
```

---

## Task 2: certmanager package — types and GVRs

**Files:**
- Create: `backend/internal/certmanager/types.go`

- [ ] **Step 1: Create the file**

```go
// Package certmanager provides cert-manager integration for k8sCenter:
// Certificate/Issuer inventory, lifecycle actions (renew, reissue), and
// expiry notifications via the Notification Center.
package certmanager

import (
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// cert-manager.io/v1 GVRs
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

// acme.cert-manager.io/v1 GVRs
var (
	OrderGVR = schema.GroupVersionResource{
		Group: "acme.cert-manager.io", Version: "v1", Resource: "orders",
	}
	ChallengeGVR = schema.GroupVersionResource{
		Group: "acme.cert-manager.io", Version: "v1", Resource: "challenges",
	}
)

// Status is the flattened health enum we expose to the UI.
type Status string

const (
	StatusReady    Status = "Ready"
	StatusIssuing  Status = "Issuing"
	StatusFailed   Status = "Failed"
	StatusExpiring Status = "Expiring"
	StatusExpired  Status = "Expired"
	StatusUnknown  Status = "Unknown"
)

// Expiry thresholds for the poller + UI banding, in days.
const (
	WarningThresholdDays  = 30
	CriticalThresholdDays = 7
)

// CertManagerStatus is returned by GET /certificates/status.
type CertManagerStatus struct {
	Detected    bool      `json:"detected"`
	Namespace   string    `json:"namespace,omitempty"`
	Version     string    `json:"version,omitempty"`
	LastChecked time.Time `json:"lastChecked"`
}

// Certificate is the normalized API response for a cert-manager Certificate.
type Certificate struct {
	Name          string            `json:"name"`
	Namespace     string            `json:"namespace"`
	Status        Status            `json:"status"`
	Reason        string            `json:"reason,omitempty"`
	Message       string            `json:"message,omitempty"`
	IssuerRef     IssuerRef         `json:"issuerRef"`
	SecretName    string            `json:"secretName"`
	DNSNames      []string          `json:"dnsNames,omitempty"`
	IPAddresses   []string          `json:"ipAddresses,omitempty"`
	URIs          []string          `json:"uris,omitempty"`
	CommonName    string            `json:"commonName,omitempty"`
	Duration      string            `json:"duration,omitempty"`
	RenewBefore   string            `json:"renewBefore,omitempty"`
	NotBefore     *time.Time        `json:"notBefore,omitempty"`
	NotAfter      *time.Time        `json:"notAfter,omitempty"`
	RenewalTime   *time.Time        `json:"renewalTime,omitempty"`
	DaysRemaining *int              `json:"daysRemaining,omitempty"`
	UID           string            `json:"uid"`
	Labels        map[string]string `json:"labels,omitempty"`
}

// IssuerRef matches cert-manager's issuerRef shape.
type IssuerRef struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Group string `json:"group,omitempty"`
}

// Issuer is the normalized response for both Issuer and ClusterIssuer.
// Scope distinguishes them in unified views.
type Issuer struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace,omitempty"` // empty for ClusterIssuer
	Scope     string    `json:"scope"`               // "Namespaced" | "Cluster"
	Type      string    `json:"type"`                // "ACME" | "CA" | "Vault" | "SelfSigned" | "Unknown"
	Ready     bool      `json:"ready"`
	Reason    string    `json:"reason,omitempty"`
	Message   string    `json:"message,omitempty"`
	ACMEEmail string    `json:"acmeEmail,omitempty"`
	ACMEServer string   `json:"acmeServer,omitempty"`
	UID       string    `json:"uid"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// CertificateRequest is a normalized CR for the detail timeline.
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

// Order is a normalized ACME Order for the detail timeline.
type Order struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	State     string    `json:"state"`
	Reason    string    `json:"reason,omitempty"`
	URL       string    `json:"url,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UID       string    `json:"uid"`
	CRName    string    `json:"crName,omitempty"` // owning CertificateRequest
}

// Challenge is a normalized ACME Challenge for the detail timeline.
type Challenge struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	Type      string    `json:"type"` // "HTTP-01" | "DNS-01"
	State     string    `json:"state"`
	Reason    string    `json:"reason,omitempty"`
	DNSName   string    `json:"dnsName,omitempty"`
	Token     string    `json:"token,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UID       string    `json:"uid"`
	OrderName string    `json:"orderName,omitempty"`
}

// CertificateDetail is what GET /certificates/certificates/{ns}/{name} returns.
type CertificateDetail struct {
	Certificate         Certificate          `json:"certificate"`
	CertificateRequests []CertificateRequest `json:"certificateRequests,omitempty"`
	Orders              []Order              `json:"orders,omitempty"`
	Challenges          []Challenge          `json:"challenges,omitempty"`
}

// ExpiringCertificate is the flat view used by /certificates/expiring.
type ExpiringCertificate struct {
	Namespace     string    `json:"namespace"`
	Name          string    `json:"name"`
	UID           string    `json:"uid"`
	IssuerName    string    `json:"issuerName"`
	SecretName    string    `json:"secretName"`
	NotAfter      time.Time `json:"notAfter"`
	DaysRemaining int       `json:"daysRemaining"`
	Severity      string    `json:"severity"` // "warning" | "critical" | "expired"
}
```

- [ ] **Step 2: Verify compile**

Run: `cd backend && go build ./internal/certmanager/...`
Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/certmanager/types.go
git commit -m "feat(certmanager): normalized types and GVR constants"
```

---

## Task 3: Discovery — failing test first

**Files:**
- Create: `backend/internal/certmanager/discovery_test.go`

- [ ] **Step 1: Read the Velero discovery test for reference**

Run: Read tool on `backend/internal/velero/discovery_test.go` (if it exists). If not, look at `backend/internal/policy/discovery.go` for the pattern.

- [ ] **Step 2: Write the test**

```go
package certmanager

import (
	"context"
	"log/slog"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakedisco "k8s.io/client-go/discovery/fake"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	fakekube "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fakeClientFactory is a minimal stand-in for k8s.ClientFactory exposing only
// the methods Discoverer uses. If ClientFactory is an interface, reuse it.
// Otherwise, expose a test helper hook in discovery.go.

func TestDiscoverer_ProbeDetected(t *testing.T) {
	// Set up fake discovery returning cert-manager.io/v1 with Certificate
	disco := &fakedisco.FakeDiscovery{Fake: &clienttesting.Fake{
		Resources: []*metav1.APIResourceList{
			{
				GroupVersion: "cert-manager.io/v1",
				APIResources: []metav1.APIResource{
					{Name: "certificates", Kind: "Certificate", Namespaced: true},
					{Name: "issuers", Kind: "Issuer", Namespaced: true},
				},
			},
		},
	}}
	dynClient := fakedynamic.NewSimpleDynamicClient(runtime.NewScheme())
	kube := fakekube.NewSimpleClientset()

	d := NewDiscovererWithClients(disco, dynClient, kube, slog.Default())
	st := d.Probe(context.Background())
	if !st.Detected {
		t.Fatalf("expected Detected=true, got %+v", st)
	}
}

func TestDiscoverer_ProbeNotDetected(t *testing.T) {
	disco := &fakedisco.FakeDiscovery{Fake: &clienttesting.Fake{}}
	dynClient := fakedynamic.NewSimpleDynamicClient(runtime.NewScheme())
	kube := fakekube.NewSimpleClientset()

	d := NewDiscovererWithClients(disco, dynClient, kube, slog.Default())
	st := d.Probe(context.Background())
	if st.Detected {
		t.Fatalf("expected Detected=false, got %+v", st)
	}
}

var _ = schema.GroupVersionResource{}
```

Note: this test assumes a `NewDiscovererWithClients` test constructor — we'll add one in the implementation step.

- [ ] **Step 3: Run test to confirm it fails**

Run: `cd backend && go test ./internal/certmanager/ -run TestDiscoverer -v`
Expected: compile error (`NewDiscovererWithClients` undefined) — this is the expected failure.

---

## Task 4: Discovery — minimal implementation

**Files:**
- Create: `backend/internal/certmanager/discovery.go`

- [ ] **Step 1: Read Velero discovery.go for the exact pattern**

Run: Read tool on `backend/internal/velero/discovery.go` lines 1-146.

- [ ] **Step 2: Write discovery.go**

```go
package certmanager

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

const (
	staleDuration          = 5 * time.Minute
	certManagerNamespace   = "cert-manager"
	certManagerGroupV1     = "cert-manager.io/v1"
	certManagerDeployLabel = "app.kubernetes.io/name=cert-manager"
)

// Discoverer probes for cert-manager CRDs + deployment and caches status.
type Discoverer struct {
	disco  discovery.DiscoveryInterface
	dyn    dynamic.Interface
	kube   kubernetes.Interface
	logger *slog.Logger

	mu     sync.RWMutex
	status CertManagerStatus
}

// NewDiscoverer constructs a Discoverer from a ClientFactory.
func NewDiscoverer(cf *k8s.ClientFactory, logger *slog.Logger) *Discoverer {
	return NewDiscovererWithClients(
		cf.DiscoveryClient(),
		cf.BaseDynamicClient(),
		cf.BaseClientset(),
		logger,
	)
}

// NewDiscovererWithClients is a test-friendly constructor.
func NewDiscovererWithClients(
	disco discovery.DiscoveryInterface,
	dyn dynamic.Interface,
	kube kubernetes.Interface,
	logger *slog.Logger,
) *Discoverer {
	return &Discoverer{
		disco:  disco,
		dyn:    dyn,
		kube:   kube,
		logger: logger,
		status: CertManagerStatus{LastChecked: time.Now().UTC()},
	}
}

// Status returns cached status, re-probing if stale.
func (d *Discoverer) Status(ctx context.Context) CertManagerStatus {
	d.mu.RLock()
	if time.Since(d.status.LastChecked) < staleDuration && d.status.Detected {
		st := d.status
		d.mu.RUnlock()
		return st
	}
	d.mu.RUnlock()
	return d.Probe(ctx)
}

// Probe checks for cert-manager.io/v1 Certificate CRD.
func (d *Discoverer) Probe(ctx context.Context) CertManagerStatus {
	d.mu.Lock()
	defer d.mu.Unlock()

	st := CertManagerStatus{LastChecked: time.Now().UTC()}

	res, err := d.disco.ServerResourcesForGroupVersion(certManagerGroupV1)
	if err != nil || res == nil {
		d.logger.Debug("cert-manager CRDs not found", "error", err)
		d.status = st
		return st
	}

	for _, r := range res.APIResources {
		if r.Kind == "Certificate" {
			st.Detected = true
			break
		}
	}
	if !st.Detected {
		d.status = st
		return st
	}

	// Probe deployment for version info.
	deps, err := d.kube.AppsV1().Deployments(certManagerNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: certManagerDeployLabel,
	})
	if err == nil && len(deps.Items) > 0 {
		st.Namespace = certManagerNamespace
		dep := deps.Items[0]
		if v, ok := dep.Labels["app.kubernetes.io/version"]; ok {
			st.Version = v
		} else if len(dep.Spec.Template.Spec.Containers) > 0 {
			img := dep.Spec.Template.Spec.Containers[0].Image
			if i := strings.LastIndex(img, ":"); i >= 0 && i < len(img)-1 {
				st.Version = img[i+1:]
			}
		}
	}

	d.status = st
	d.logger.Info("cert-manager discovery",
		"detected", st.Detected,
		"namespace", st.Namespace,
		"version", st.Version,
	)
	return st
}

// IsAvailable returns true if cert-manager was detected.
func (d *Discoverer) IsAvailable(ctx context.Context) bool {
	return d.Status(ctx).Detected
}
```

- [ ] **Step 3: Run tests**

Run: `cd backend && go test ./internal/certmanager/ -run TestDiscoverer -v`
Expected: both tests PASS.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/certmanager/discovery.go backend/internal/certmanager/discovery_test.go
git commit -m "feat(certmanager): CRD discovery with deployment probe"
```

---

## Task 5: Normalization — failing tests first

**Files:**
- Create: `backend/internal/certmanager/normalize_test.go`

- [ ] **Step 1: Write table-driven tests**

```go
package certmanager

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestComputeStatus(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		ready    string // "True", "False", "Unknown", or ""
		reason   string
		notAfter *time.Time
		want     Status
	}{
		{"ready valid", "True", "Ready", ptrTime(now.Add(60 * 24 * time.Hour)), StatusReady},
		{"ready expiring warn", "True", "Ready", ptrTime(now.Add(20 * 24 * time.Hour)), StatusExpiring},
		{"ready expiring crit", "True", "Ready", ptrTime(now.Add(3 * 24 * time.Hour)), StatusExpiring},
		{"expired", "True", "Ready", ptrTime(now.Add(-1 * time.Hour)), StatusExpired},
		{"issuing", "False", "Issuing", nil, StatusIssuing},
		{"failed", "False", "Failed", nil, StatusFailed},
		{"unknown", "Unknown", "", nil, StatusUnknown},
		{"missing ready", "", "", nil, StatusUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeStatus(tc.ready, tc.reason, tc.notAfter)
			if got != tc.want {
				t.Fatalf("computeStatus(%q,%q,%v)=%q, want %q", tc.ready, tc.reason, tc.notAfter, got, tc.want)
			}
		})
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestNormalizeCertificate(t *testing.T) {
	notAfter := time.Now().Add(45 * 24 * time.Hour)
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata": map[string]any{
			"name":      "example-tls",
			"namespace": "default",
			"uid":       "uid-123",
		},
		"spec": map[string]any{
			"secretName": "example-tls",
			"dnsNames":   []any{"example.com", "www.example.com"},
			"issuerRef": map[string]any{
				"name":  "letsencrypt",
				"kind":  "ClusterIssuer",
				"group": "cert-manager.io",
			},
		},
		"status": map[string]any{
			"notAfter": notAfter.UTC().Format(time.RFC3339),
			"conditions": []any{
				map[string]any{
					"type":   "Ready",
					"status": "True",
					"reason": "Ready",
				},
			},
		},
	}}
	c, err := normalizeCertificate(u)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Name != "example-tls" || c.Namespace != "default" {
		t.Fatalf("wrong name/ns: %+v", c)
	}
	if c.Status != StatusReady {
		t.Fatalf("expected Ready, got %s", c.Status)
	}
	if len(c.DNSNames) != 2 {
		t.Fatalf("expected 2 DNS names, got %d", len(c.DNSNames))
	}
	if c.IssuerRef.Name != "letsencrypt" {
		t.Fatalf("wrong issuer: %+v", c.IssuerRef)
	}
	if c.DaysRemaining == nil || *c.DaysRemaining < 40 || *c.DaysRemaining > 45 {
		t.Fatalf("unexpected daysRemaining: %v", c.DaysRemaining)
	}
}

var _ = metav1.ListOptions{}
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `cd backend && go test ./internal/certmanager/ -run "TestComputeStatus|TestNormalizeCertificate" -v`
Expected: compile error (`computeStatus` and `normalizeCertificate` undefined).

---

## Task 6: Normalization — implementation

**Files:**
- Create: `backend/internal/certmanager/normalize.go`

- [ ] **Step 1: Write normalize.go**

```go
package certmanager

import (
	"fmt"
	"math"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// computeStatus flattens cert-manager's Ready condition + reason + NotAfter
// into our Status enum. Expiring is bucketed against WarningThresholdDays.
func computeStatus(readyStatus, reason string, notAfter *time.Time) Status {
	if notAfter != nil && notAfter.Before(time.Now()) {
		return StatusExpired
	}
	switch readyStatus {
	case "True":
		if notAfter != nil {
			d := int(math.Floor(time.Until(*notAfter).Hours() / 24))
			if d <= WarningThresholdDays {
				return StatusExpiring
			}
		}
		return StatusReady
	case "False":
		if reason == "Issuing" || reason == "InProgress" {
			return StatusIssuing
		}
		return StatusFailed
	default:
		return StatusUnknown
	}
}

func normalizeCertificate(u *unstructured.Unstructured) (Certificate, error) {
	c := Certificate{
		Name:      u.GetName(),
		Namespace: u.GetNamespace(),
		UID:       string(u.GetUID()),
		Labels:    u.GetLabels(),
	}

	secretName, _, _ := unstructured.NestedString(u.Object, "spec", "secretName")
	c.SecretName = secretName

	dnsNames, _, _ := unstructured.NestedStringSlice(u.Object, "spec", "dnsNames")
	c.DNSNames = dnsNames
	ipAddrs, _, _ := unstructured.NestedStringSlice(u.Object, "spec", "ipAddresses")
	c.IPAddresses = ipAddrs
	uris, _, _ := unstructured.NestedStringSlice(u.Object, "spec", "uris")
	c.URIs = uris
	cn, _, _ := unstructured.NestedString(u.Object, "spec", "commonName")
	c.CommonName = cn
	dur, _, _ := unstructured.NestedString(u.Object, "spec", "duration")
	c.Duration = dur
	rb, _, _ := unstructured.NestedString(u.Object, "spec", "renewBefore")
	c.RenewBefore = rb

	if issuer, ok, _ := unstructured.NestedMap(u.Object, "spec", "issuerRef"); ok {
		c.IssuerRef = IssuerRef{
			Name:  stringFrom(issuer, "name"),
			Kind:  stringFrom(issuer, "kind"),
			Group: stringFrom(issuer, "group"),
		}
	}

	notBefore := parseTimeField(u.Object, "status", "notBefore")
	notAfter := parseTimeField(u.Object, "status", "notAfter")
	renewal := parseTimeField(u.Object, "status", "renewalTime")
	c.NotBefore = notBefore
	c.NotAfter = notAfter
	c.RenewalTime = renewal

	if notAfter != nil {
		d := int(math.Floor(time.Until(*notAfter).Hours() / 24))
		c.DaysRemaining = &d
	}

	readyStatus, reason, message := readReadyCondition(u.Object)
	c.Reason = reason
	c.Message = message
	c.Status = computeStatus(readyStatus, reason, notAfter)

	return c, nil
}

func normalizeIssuer(u *unstructured.Unstructured, scope string) Issuer {
	iss := Issuer{
		Name:      u.GetName(),
		Namespace: u.GetNamespace(),
		Scope:     scope,
		Type:      detectIssuerType(u.Object),
		UID:       string(u.GetUID()),
		UpdatedAt: u.GetCreationTimestamp().Time,
	}

	readyStatus, reason, message := readReadyCondition(u.Object)
	iss.Ready = readyStatus == "True"
	iss.Reason = reason
	iss.Message = message

	if email, ok, _ := unstructured.NestedString(u.Object, "spec", "acme", "email"); ok {
		iss.ACMEEmail = email
	}
	if srv, ok, _ := unstructured.NestedString(u.Object, "spec", "acme", "server"); ok {
		iss.ACMEServer = srv
	}
	return iss
}

func detectIssuerType(obj map[string]any) string {
	if _, ok, _ := unstructured.NestedMap(obj, "spec", "acme"); ok {
		return "ACME"
	}
	if _, ok, _ := unstructured.NestedMap(obj, "spec", "ca"); ok {
		return "CA"
	}
	if _, ok, _ := unstructured.NestedMap(obj, "spec", "vault"); ok {
		return "Vault"
	}
	if _, ok, _ := unstructured.NestedMap(obj, "spec", "selfSigned"); ok {
		return "SelfSigned"
	}
	return "Unknown"
}

func normalizeCertRequest(u *unstructured.Unstructured) CertificateRequest {
	cr := CertificateRequest{
		Name:      u.GetName(),
		Namespace: u.GetNamespace(),
		UID:       string(u.GetUID()),
		CreatedAt: u.GetCreationTimestamp().Time,
	}
	if issuer, ok, _ := unstructured.NestedMap(u.Object, "spec", "issuerRef"); ok {
		cr.IssuerRef = IssuerRef{
			Name:  stringFrom(issuer, "name"),
			Kind:  stringFrom(issuer, "kind"),
			Group: stringFrom(issuer, "group"),
		}
	}
	readyStatus, reason, message := readReadyCondition(u.Object)
	cr.Reason = reason
	cr.Message = message
	cr.Status = computeStatus(readyStatus, reason, nil)
	return cr
}

func normalizeOrder(u *unstructured.Unstructured) Order {
	o := Order{
		Name:      u.GetName(),
		Namespace: u.GetNamespace(),
		UID:       string(u.GetUID()),
		CreatedAt: u.GetCreationTimestamp().Time,
	}
	o.State, _, _ = unstructured.NestedString(u.Object, "status", "state")
	o.Reason, _, _ = unstructured.NestedString(u.Object, "status", "reason")
	o.URL, _, _ = unstructured.NestedString(u.Object, "status", "url")
	for _, owner := range u.GetOwnerReferences() {
		if owner.Kind == "CertificateRequest" {
			o.CRName = owner.Name
			break
		}
	}
	return o
}

func normalizeChallenge(u *unstructured.Unstructured) Challenge {
	ch := Challenge{
		Name:      u.GetName(),
		Namespace: u.GetNamespace(),
		UID:       string(u.GetUID()),
		CreatedAt: u.GetCreationTimestamp().Time,
	}
	ch.Type, _, _ = unstructured.NestedString(u.Object, "spec", "type")
	ch.DNSName, _, _ = unstructured.NestedString(u.Object, "spec", "dnsName")
	ch.Token, _, _ = unstructured.NestedString(u.Object, "spec", "token")
	ch.State, _, _ = unstructured.NestedString(u.Object, "status", "state")
	ch.Reason, _, _ = unstructured.NestedString(u.Object, "status", "reason")
	for _, owner := range u.GetOwnerReferences() {
		if owner.Kind == "Order" {
			ch.OrderName = owner.Name
			break
		}
	}
	return ch
}

// readReadyCondition returns (status, reason, message) of the "Ready" condition.
func readReadyCondition(obj map[string]any) (string, string, string) {
	conds, found, err := unstructured.NestedSlice(obj, "status", "conditions")
	if err != nil || !found {
		return "", "", ""
	}
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if stringFrom(m, "type") == "Ready" {
			return stringFrom(m, "status"), stringFrom(m, "reason"), stringFrom(m, "message")
		}
	}
	return "", "", ""
}

func stringFrom(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func parseTimeField(obj map[string]any, fields ...string) *time.Time {
	s, found, err := unstructured.NestedString(obj, fields...)
	if err != nil || !found || s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

var _ = fmt.Sprintf // keep fmt import for future use
```

- [ ] **Step 2: Run tests**

Run: `cd backend && go test ./internal/certmanager/ -run "TestComputeStatus|TestNormalizeCertificate" -v`
Expected: both PASS.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/certmanager/normalize.go backend/internal/certmanager/normalize_test.go
git commit -m "feat(certmanager): unstructured normalization + status flattening"
```

---

## Task 7: RBAC helper

**Files:**
- Create: `backend/internal/certmanager/rbac.go`

- [ ] **Step 1: Read existing RBAC helper signature**

Run: Read tool on `backend/internal/k8s/resources/access.go` lines 125-175 to see `CanAccessGroupResource` exactly.

- [ ] **Step 2: Write rbac.go**

```go
package certmanager

import (
	"context"
	"log/slog"

	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

const certManagerAPIGroup = "cert-manager.io"
const acmeAPIGroup = "acme.cert-manager.io"

// filterCertificatesByRBAC keeps only certificates the user can "get" in their namespace.
func filterCertificatesByRBAC(
	ctx context.Context,
	ac *resources.AccessChecker,
	user string,
	groups []string,
	items []Certificate,
	logger *slog.Logger,
) []Certificate {
	nsAllow := map[string]bool{}
	out := make([]Certificate, 0, len(items))
	for _, c := range items {
		allowed, ok := nsAllow[c.Namespace]
		if !ok {
			ok2, err := ac.CanAccessGroupResource(ctx, user, groups, "get", certManagerAPIGroup, "certificates", c.Namespace)
			if err != nil {
				logger.Debug("rbac check failed", "namespace", c.Namespace, "error", err)
				ok2 = false
			}
			nsAllow[c.Namespace] = ok2
			allowed = ok2
		}
		if allowed {
			out = append(out, c)
		}
	}
	return out
}

// filterIssuersByRBAC keeps only namespaced Issuers the user can "get".
func filterIssuersByRBAC(
	ctx context.Context,
	ac *resources.AccessChecker,
	user string,
	groups []string,
	items []Issuer,
	logger *slog.Logger,
) []Issuer {
	nsAllow := map[string]bool{}
	out := make([]Issuer, 0, len(items))
	for _, i := range items {
		allowed, ok := nsAllow[i.Namespace]
		if !ok {
			ok2, err := ac.CanAccessGroupResource(ctx, user, groups, "get", certManagerAPIGroup, "issuers", i.Namespace)
			if err != nil {
				logger.Debug("rbac check failed", "namespace", i.Namespace, "error", err)
				ok2 = false
			}
			nsAllow[i.Namespace] = ok2
			allowed = ok2
		}
		if allowed {
			out = append(out, i)
		}
	}
	return out
}

// canListClusterIssuers checks if user has cluster-scoped list permission.
func canListClusterIssuers(ctx context.Context, ac *resources.AccessChecker, user string, groups []string) bool {
	ok, err := ac.CanAccessGroupResource(ctx, user, groups, "list", certManagerAPIGroup, "clusterissuers", "")
	if err != nil {
		return false
	}
	return ok
}
```

- [ ] **Step 2: Verify compile**

Run: `cd backend && go build ./internal/certmanager/...`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/certmanager/rbac.go
git commit -m "feat(certmanager): RBAC filter helpers"
```

---

## Task 8: Handler — status, list, detail (read endpoints)

**Files:**
- Create: `backend/internal/certmanager/handler.go`

- [ ] **Step 1: Read Velero handler for structure reference**

Run: Read tool on `backend/internal/velero/handler.go` lines 1-120 (already read above — reference this for struct shape).

- [ ] **Step 2: Write handler.go**

```go
package certmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/singleflight"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/notifications"
)

const cacheTTL = 30 * time.Second

// Handler serves cert-manager HTTP endpoints.
type Handler struct {
	K8sClient     *k8s.ClientFactory
	Discoverer    *Discoverer
	AccessChecker *resources.AccessChecker
	AuditLogger   audit.Logger
	NotifService  *notifications.NotificationService
	Logger        *slog.Logger

	fetchGroup singleflight.Group
	cacheMu    sync.RWMutex
	cache      *cachedData
}

type cachedData struct {
	certificates   []Certificate
	issuers        []Issuer
	clusterIssuers []Issuer
	fetchedAt      time.Time
}

func NewHandler(
	cf *k8s.ClientFactory,
	disc *Discoverer,
	ac *resources.AccessChecker,
	audit audit.Logger,
	notif *notifications.NotificationService,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		K8sClient:     cf,
		Discoverer:    disc,
		AccessChecker: ac,
		AuditLogger:   audit,
		NotifService:  notif,
		Logger:        logger,
	}
}

// HandleStatus returns cert-manager availability.
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	st := h.Discoverer.Status(r.Context())
	httputil.JSON(w, http.StatusOK, map[string]any{"data": st})
}

// HandleListCertificates returns all Certificates (RBAC-filtered, cached).
func (h *Handler) HandleListCertificates(w http.ResponseWriter, r *http.Request) {
	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.Error(w, http.StatusServiceUnavailable, "cert-manager not installed")
		return
	}
	data, err := h.getCached(r.Context())
	if err != nil {
		h.Logger.Error("list certificates failed", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to list certificates")
		return
	}
	user, groups := auth.UserAndGroups(r.Context())
	filtered := filterCertificatesByRBAC(r.Context(), h.AccessChecker, user, groups, data.certificates, h.Logger)
	ns := r.URL.Query().Get("namespace")
	if ns != "" {
		out := filtered[:0]
		for _, c := range filtered {
			if c.Namespace == ns {
				out = append(out, c)
			}
		}
		filtered = out
	}
	httputil.JSON(w, http.StatusOK, map[string]any{
		"data":     filtered,
		"metadata": map[string]any{"total": len(filtered)},
	})
}

// HandleListIssuers returns all namespaced Issuers (RBAC-filtered, cached).
func (h *Handler) HandleListIssuers(w http.ResponseWriter, r *http.Request) {
	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.Error(w, http.StatusServiceUnavailable, "cert-manager not installed")
		return
	}
	data, err := h.getCached(r.Context())
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, "failed to list issuers")
		return
	}
	user, groups := auth.UserAndGroups(r.Context())
	filtered := filterIssuersByRBAC(r.Context(), h.AccessChecker, user, groups, data.issuers, h.Logger)
	httputil.JSON(w, http.StatusOK, map[string]any{
		"data":     filtered,
		"metadata": map[string]any{"total": len(filtered)},
	})
}

// HandleListClusterIssuers returns all ClusterIssuers if user has list permission.
func (h *Handler) HandleListClusterIssuers(w http.ResponseWriter, r *http.Request) {
	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.Error(w, http.StatusServiceUnavailable, "cert-manager not installed")
		return
	}
	user, groups := auth.UserAndGroups(r.Context())
	if !canListClusterIssuers(r.Context(), h.AccessChecker, user, groups) {
		httputil.JSON(w, http.StatusOK, map[string]any{
			"data":     []Issuer{},
			"metadata": map[string]any{"total": 0},
		})
		return
	}
	data, err := h.getCached(r.Context())
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, "failed to list clusterissuers")
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]any{
		"data":     data.clusterIssuers,
		"metadata": map[string]any{"total": len(data.clusterIssuers)},
	})
}

// HandleGetCertificate returns a single Certificate with nested CRs/Orders/Challenges.
// Uses user impersonation — no cache.
func (h *Handler) HandleGetCertificate(w http.ResponseWriter, r *http.Request) {
	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.Error(w, http.StatusServiceUnavailable, "cert-manager not installed")
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	user, groups := auth.UserAndGroups(r.Context())
	dyn, err := h.K8sClient.ImpersonatedDynamic(user, groups)
	if err != nil {
		httputil.Error(w, http.StatusForbidden, "impersonation failed")
		return
	}

	certU, err := dyn.Resource(CertificateGVR).Namespace(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		httputil.Error(w, http.StatusNotFound, "certificate not found")
		return
	}
	cert, _ := normalizeCertificate(certU)
	detail := CertificateDetail{Certificate: cert}

	// Nested CRs owned by this Cert (label: cert-manager.io/certificate-name=<name>)
	crSel := fmt.Sprintf("cert-manager.io/certificate-name=%s", name)
	crList, err := dyn.Resource(CertificateRequestGVR).Namespace(ns).List(r.Context(), metav1.ListOptions{LabelSelector: crSel})
	if err == nil {
		for i := range crList.Items {
			detail.CertificateRequests = append(detail.CertificateRequests, normalizeCertRequest(&crList.Items[i]))
		}
	}

	// Orders in namespace owned by any of these CRs
	crUIDs := map[string]string{}
	for _, cr := range crList.Items {
		crUIDs[string(cr.GetUID())] = cr.GetName()
	}
	orderList, err := dyn.Resource(OrderGVR).Namespace(ns).List(r.Context(), metav1.ListOptions{})
	orderUIDs := map[string]string{}
	if err == nil {
		for i := range orderList.Items {
			o := &orderList.Items[i]
			for _, owner := range o.GetOwnerReferences() {
				if _, ok := crUIDs[string(owner.UID)]; ok {
					detail.Orders = append(detail.Orders, normalizeOrder(o))
					orderUIDs[string(o.GetUID())] = o.GetName()
					break
				}
			}
		}
	}
	// Challenges owned by any of these Orders
	chList, err := dyn.Resource(ChallengeGVR).Namespace(ns).List(r.Context(), metav1.ListOptions{})
	if err == nil {
		for i := range chList.Items {
			c := &chList.Items[i]
			for _, owner := range c.GetOwnerReferences() {
				if _, ok := orderUIDs[string(owner.UID)]; ok {
					detail.Challenges = append(detail.Challenges, normalizeChallenge(c))
					break
				}
			}
		}
	}

	httputil.JSON(w, http.StatusOK, map[string]any{"data": detail})
}

// HandleListExpiring returns flat list of expiring/expired certs.
func (h *Handler) HandleListExpiring(w http.ResponseWriter, r *http.Request) {
	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.Error(w, http.StatusServiceUnavailable, "cert-manager not installed")
		return
	}
	data, err := h.getCached(r.Context())
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, "failed to list certificates")
		return
	}
	user, groups := auth.UserAndGroups(r.Context())
	filtered := filterCertificatesByRBAC(r.Context(), h.AccessChecker, user, groups, data.certificates, h.Logger)

	out := make([]ExpiringCertificate, 0)
	for _, c := range filtered {
		if c.NotAfter == nil {
			continue
		}
		d := 0
		if c.DaysRemaining != nil {
			d = *c.DaysRemaining
		}
		var sev string
		switch {
		case d < 0:
			sev = "expired"
		case d <= CriticalThresholdDays:
			sev = "critical"
		case d <= WarningThresholdDays:
			sev = "warning"
		default:
			continue
		}
		out = append(out, ExpiringCertificate{
			Namespace:     c.Namespace,
			Name:          c.Name,
			UID:           c.UID,
			IssuerName:    c.IssuerRef.Name,
			SecretName:    c.SecretName,
			NotAfter:      *c.NotAfter,
			DaysRemaining: d,
			Severity:      sev,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DaysRemaining < out[j].DaysRemaining })
	httputil.JSON(w, http.StatusOK, map[string]any{
		"data":     out,
		"metadata": map[string]any{"total": len(out)},
	})
}

// getCached returns cache, refreshing via singleflight if stale.
func (h *Handler) getCached(ctx context.Context) (*cachedData, error) {
	h.cacheMu.RLock()
	if h.cache != nil && time.Since(h.cache.fetchedAt) < cacheTTL {
		d := h.cache
		h.cacheMu.RUnlock()
		return d, nil
	}
	h.cacheMu.RUnlock()

	v, err, _ := h.fetchGroup.Do("all", func() (any, error) {
		return h.fetchAll(ctx)
	})
	if err != nil {
		return nil, err
	}
	d := v.(*cachedData)
	h.cacheMu.Lock()
	h.cache = d
	h.cacheMu.Unlock()
	return d, nil
}

// fetchAll uses the base (service account) dynamic client — RBAC filtering is done per-request in the handler.
func (h *Handler) fetchAll(ctx context.Context) (*cachedData, error) {
	dyn := h.K8sClient.BaseDynamicClient()
	data := &cachedData{fetchedAt: time.Now()}

	data.certificates = listAndNormalize[Certificate](ctx, dyn, CertificateGVR, "",
		func(u *unstructured.Unstructured) Certificate { c, _ := normalizeCertificate(u); return c })
	data.issuers = listAndNormalize[Issuer](ctx, dyn, IssuerGVR, "",
		func(u *unstructured.Unstructured) Issuer { return normalizeIssuer(u, "Namespaced") })
	data.clusterIssuers = listAndNormalize[Issuer](ctx, dyn, ClusterIssuerGVR, "",
		func(u *unstructured.Unstructured) Issuer { return normalizeIssuer(u, "Cluster") })
	return data, nil
}

func listAndNormalize[T any](
	ctx context.Context,
	dyn dynamic.Interface,
	gvr interface{ Resource() string }, // placeholder — see below
	ns string,
	fn func(*unstructured.Unstructured) T,
) []T {
	// This generic helper is replaced in the implementation step because GVR doesn't have Resource().
	// See revised version below.
	return nil
}

// UserAndGroups is a local hook — some codebases may have this in auth. Use the real one.
var _ = auth.UserAndGroups

// cache invalidation (called from actions and InformerManager if wired)
func (h *Handler) InvalidateCache() {
	h.cacheMu.Lock()
	h.cache = nil
	h.cacheMu.Unlock()
}

// decodeJSON is a small helper used elsewhere.
var _ = json.Marshal
```

**Note:** the generic `listAndNormalize` sketch above won't compile. Replace `fetchAll` with three explicit calls. Rewrite block to use:

```go
func (h *Handler) fetchAll(ctx context.Context) (*cachedData, error) {
	dyn := h.K8sClient.BaseDynamicClient()
	data := &cachedData{fetchedAt: time.Now()}

	if certs, err := dyn.Resource(CertificateGVR).Namespace("").List(ctx, metav1.ListOptions{}); err == nil {
		for i := range certs.Items {
			c, _ := normalizeCertificate(&certs.Items[i])
			data.certificates = append(data.certificates, c)
		}
	} else {
		return nil, fmt.Errorf("list certificates: %w", err)
	}
	if iss, err := dyn.Resource(IssuerGVR).Namespace("").List(ctx, metav1.ListOptions{}); err == nil {
		for i := range iss.Items {
			data.issuers = append(data.issuers, normalizeIssuer(&iss.Items[i], "Namespaced"))
		}
	}
	if ciss, err := dyn.Resource(ClusterIssuerGVR).List(ctx, metav1.ListOptions{}); err == nil {
		for i := range ciss.Items {
			data.clusterIssuers = append(data.clusterIssuers, normalizeIssuer(&ciss.Items[i], "Cluster"))
		}
	}
	return data, nil
}
```

Delete the broken `listAndNormalize` generic. Delete unused imports.

- [ ] **Step 3: Verify compile**

Run: `cd backend && go build ./internal/certmanager/...`
Expected: no output. Fix any unused-import errors the compiler flags.

- [ ] **Step 4: Check auth helpers actually exist**

Run: `grep -rn "func UserAndGroups\|func UsernameFrom\|func Username\b" backend/internal/auth/ | head -5`
Expected: find the actual helper name. If it's `UsernameFromContext` + `GroupsFromContext` instead of `UserAndGroups`, update handler.go accordingly. Also check how Velero handler reads user — mirror that.

- [ ] **Step 5: Check K8sClient impersonation method name**

Run: `grep -n "func.*Impersonated" backend/internal/k8s/*.go`
Expected: find the real method. Adjust `ImpersonatedDynamic(user, groups)` call to whatever signature exists.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/certmanager/handler.go
git commit -m "feat(certmanager): read handlers with singleflight cache + RBAC filter"
```

---

## Task 9: Handler action endpoints — renew, reissue

**Files:**
- Create: `backend/internal/certmanager/actions.go`

- [ ] **Step 1: Write actions.go**

```go
package certmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
)

// HandleRenew triggers a Certificate renewal by patching the status subresource
// to add an Issuing=True condition — the same mechanism cmctl renew uses.
func (h *Handler) HandleRenew(w http.ResponseWriter, r *http.Request) {
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	user, groups := auth.UserAndGroups(r.Context())
	dyn, err := h.K8sClient.ImpersonatedDynamic(user, groups)
	if err != nil {
		httputil.Error(w, http.StatusForbidden, "impersonation failed")
		return
	}

	now := metav1.NewTime(time.Now().UTC())
	patch := map[string]any{
		"status": map[string]any{
			"conditions": []any{
				map[string]any{
					"type":               "Issuing",
					"status":             "True",
					"reason":             "ManuallyTriggered",
					"message":            "Renewal triggered via k8sCenter",
					"lastTransitionTime": now.Format(time.RFC3339),
				},
			},
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, "marshal patch")
		return
	}

	_, err = dyn.Resource(CertificateGVR).Namespace(ns).Patch(
		r.Context(),
		name,
		types.MergePatchType,
		patchBytes,
		metav1.PatchOptions{FieldManager: "kubecenter"},
		"status",
	)
	if err != nil {
		h.Logger.Error("renew patch failed", "ns", ns, "name", name, "error", err)
		httputil.Error(w, http.StatusInternalServerError, fmt.Sprintf("renew failed: %v", err))
		return
	}

	h.AuditLogger.Log(r.Context(), audit.Event{
		User:     user,
		Action:   "renew",
		Resource: "cert-manager.io/certificates",
		Name:     name,
		Ns:       ns,
	})
	h.InvalidateCache()
	httputil.JSON(w, http.StatusAccepted, map[string]any{"data": map[string]string{"status": "renewing"}})
}

// HandleReissue deletes the Secret backing a Certificate, forcing full re-issuance.
func (h *Handler) HandleReissue(w http.ResponseWriter, r *http.Request) {
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	user, groups := auth.UserAndGroups(r.Context())
	dyn, err := h.K8sClient.ImpersonatedDynamic(user, groups)
	if err != nil {
		httputil.Error(w, http.StatusForbidden, "impersonation failed")
		return
	}

	certU, err := dyn.Resource(CertificateGVR).Namespace(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		httputil.Error(w, http.StatusNotFound, "certificate not found")
		return
	}
	secretName, _, _ := unstructured.NestedString(certU.Object, "spec", "secretName")
	if secretName == "" {
		httputil.Error(w, http.StatusBadRequest, "certificate has no secretName")
		return
	}

	// Use impersonated typed client to delete the Secret
	typed, err := h.K8sClient.ImpersonatedTyped(user, groups)
	if err != nil {
		httputil.Error(w, http.StatusForbidden, "impersonation failed")
		return
	}
	err = typed.CoreV1().Secrets(ns).Delete(r.Context(), secretName, metav1.DeleteOptions{})
	if err != nil {
		h.Logger.Error("reissue secret delete failed", "ns", ns, "secret", secretName, "error", err)
		httputil.Error(w, http.StatusInternalServerError, fmt.Sprintf("reissue failed: %v", err))
		return
	}

	h.AuditLogger.Log(r.Context(), audit.Event{
		User:     user,
		Action:   "reissue",
		Resource: "cert-manager.io/certificates",
		Name:     name,
		Ns:       ns,
	})
	h.InvalidateCache()
	httputil.JSON(w, http.StatusAccepted, map[string]any{"data": map[string]string{"status": "reissuing"}})
}

// Force context unused warning silenced
var _ = context.Background
```

- [ ] **Step 2: Reconcile audit.Event field names**

Run: `grep -n "type Event\b" backend/internal/audit/*.go`
Expected: find the real Event struct. Update field names in `actions.go` to match (could be `Verb` instead of `Action`, etc.).

- [ ] **Step 3: Reconcile ImpersonatedTyped method name**

Run: `grep -n "func.*Impersonated" backend/internal/k8s/*.go`
Update call to match actual method signature.

- [ ] **Step 4: Verify compile**

Run: `cd backend && go build ./internal/certmanager/...`
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/certmanager/actions.go
git commit -m "feat(certmanager): renew and reissue action handlers"
```

---

## Task 10: Poller — failing tests first

**Files:**
- Create: `backend/internal/certmanager/poller_test.go`

- [ ] **Step 1: Write tests**

```go
package certmanager

import (
	"testing"
	"time"
)

func TestThresholdBucket(t *testing.T) {
	cases := []struct {
		days int
		want threshold
	}{
		{60, thresholdNone},
		{30, thresholdWarning},
		{29, thresholdWarning},
		{8, thresholdWarning},
		{7, thresholdCritical},
		{0, thresholdCritical},
		{-1, thresholdExpired},
	}
	for _, tc := range cases {
		if got := thresholdBucket(tc.days); got != tc.want {
			t.Fatalf("thresholdBucket(%d)=%v want %v", tc.days, got, tc.want)
		}
	}
}

func TestDedupeEmitOncePerCrossing(t *testing.T) {
	p := newPollerForTest()
	now := time.Now()
	c := Certificate{
		UID:      "uid-1",
		Name:     "a",
		Namespace: "ns",
		NotAfter: ptrTime(now.Add(25 * 24 * time.Hour)), // warning
	}
	c.DaysRemaining = intPtr(25)

	emitted := p.check(c)
	if len(emitted) != 1 {
		t.Fatalf("first crossing: expected 1 emit, got %d", len(emitted))
	}
	if emitted[0].Severity != "warning" {
		t.Fatalf("expected warning, got %s", emitted[0].Severity)
	}

	// Same cert, same bucket → no emit
	emitted = p.check(c)
	if len(emitted) != 0 {
		t.Fatalf("second tick same bucket: expected 0 emits, got %d", len(emitted))
	}

	// Cross into critical
	c.NotAfter = ptrTime(now.Add(5 * 24 * time.Hour))
	c.DaysRemaining = intPtr(5)
	emitted = p.check(c)
	if len(emitted) != 1 || emitted[0].Severity != "critical" {
		t.Fatalf("cross into critical: expected 1 critical emit, got %+v", emitted)
	}

	// Renewal: notAfter advances back past warning threshold
	c.NotAfter = ptrTime(now.Add(60 * 24 * time.Hour))
	c.DaysRemaining = intPtr(60)
	emitted = p.check(c)
	if len(emitted) != 0 {
		t.Fatalf("renewal: expected 0 emits, got %d", len(emitted))
	}

	// Now re-degrade to warning → should re-emit because dedupe entry was cleared
	c.NotAfter = ptrTime(now.Add(20 * 24 * time.Hour))
	c.DaysRemaining = intPtr(20)
	emitted = p.check(c)
	if len(emitted) != 1 {
		t.Fatalf("re-degrade: expected 1 emit, got %d", len(emitted))
	}
}

func intPtr(i int) *int { return &i }
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/certmanager/ -run "TestThresholdBucket|TestDedupeEmitOncePerCrossing" -v`
Expected: compile errors (`thresholdBucket`, `newPollerForTest`, `check` undefined).

---

## Task 11: Poller — implementation

**Files:**
- Create: `backend/internal/certmanager/poller.go`

- [ ] **Step 1: Write poller.go**

```go
package certmanager

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/notifications"
)

const (
	pollInterval = 60 * time.Second
)

type threshold int

const (
	thresholdNone threshold = iota
	thresholdWarning
	thresholdCritical
	thresholdExpired
)

func (t threshold) String() string {
	switch t {
	case thresholdWarning:
		return "warning"
	case thresholdCritical:
		return "critical"
	case thresholdExpired:
		return "expired"
	}
	return "none"
}

// thresholdBucket maps daysRemaining to a threshold bucket.
func thresholdBucket(days int) threshold {
	switch {
	case days < 0:
		return thresholdExpired
	case days <= CriticalThresholdDays:
		return thresholdCritical
	case days <= WarningThresholdDays:
		return thresholdWarning
	default:
		return thresholdNone
	}
}

// emitRecord is what the poller publishes.
type emitRecord struct {
	Certificate Certificate
	Severity    string
	Threshold   threshold
}

// Poller runs a background loop emitting expiry notifications.
type Poller struct {
	k8s          *k8s.ClientFactory
	disc         *Discoverer
	notifService *notifications.NotificationService
	logger       *slog.Logger

	mu     sync.Mutex
	dedupe map[string]threshold // key: "<uid>:<threshold>", value: bucket that was emitted
}

// NewPoller constructs a Poller.
func NewPoller(cf *k8s.ClientFactory, disc *Discoverer, ns *notifications.NotificationService, logger *slog.Logger) *Poller {
	return &Poller{
		k8s:          cf,
		disc:         disc,
		notifService: ns,
		logger:       logger,
		dedupe:       map[string]threshold{},
	}
}

// newPollerForTest is used only by tests.
func newPollerForTest() *Poller {
	return &Poller{
		logger: slog.Default(),
		dedupe: map[string]threshold{},
	}
}

// Start runs the polling loop until ctx is cancelled.
// Local cluster only in Phase 11A.
func (p *Poller) Start(ctx context.Context) {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	// Fire immediately on start.
	p.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.tick(ctx)
		}
	}
}

func (p *Poller) tick(ctx context.Context) {
	if !p.disc.IsAvailable(ctx) {
		return
	}
	dyn := p.k8s.BaseDynamicClient()
	list, err := dyn.Resource(CertificateGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		p.logger.Debug("poller list failed", "error", err)
		return
	}
	for i := range list.Items {
		c, err := normalizeCertificate(&list.Items[i])
		if err != nil {
			continue
		}
		for _, rec := range p.check(c) {
			p.emit(ctx, rec)
		}
	}
}

// check returns the records to emit for a single cert, updating dedupe state.
func (p *Poller) check(c Certificate) []emitRecord {
	p.mu.Lock()
	defer p.mu.Unlock()

	if c.NotAfter == nil || c.DaysRemaining == nil {
		return nil
	}
	bucket := thresholdBucket(*c.DaysRemaining)
	key := c.UID
	prev := p.dedupe[key]

	if bucket == thresholdNone {
		// Recovered: clear any prior state.
		if prev != thresholdNone {
			delete(p.dedupe, key)
		}
		return nil
	}
	if prev == bucket {
		return nil
	}
	p.dedupe[key] = bucket
	return []emitRecord{{
		Certificate: c,
		Severity:    bucket.String(),
		Threshold:   bucket,
	}}
}

func (p *Poller) emit(ctx context.Context, rec emitRecord) {
	if p.notifService == nil {
		return
	}
	var sev notifications.Severity
	switch rec.Threshold {
	case thresholdCritical, thresholdExpired:
		sev = notifications.SeverityCritical
	default:
		sev = notifications.SeverityWarning
	}

	kind := "certificate.expiring"
	if rec.Threshold == thresholdExpired {
		kind = "certificate.expired"
	}

	title := fmt.Sprintf("Certificate %s/%s %s", rec.Certificate.Namespace, rec.Certificate.Name, rec.Severity)
	msg := fmt.Sprintf("Certificate %s/%s expires in %d days (issuer: %s)",
		rec.Certificate.Namespace, rec.Certificate.Name, *rec.Certificate.DaysRemaining, rec.Certificate.IssuerRef.Name)

	p.notifService.Emit(ctx, notifications.Notification{
		Source:       notifications.SourceCertManager,
		Severity:     sev,
		Title:        title,
		Message:      msg,
		ResourceKind: kind,
		ResourceNS:   rec.Certificate.Namespace,
		ResourceName: rec.Certificate.Name,
		CreatedAt:    time.Now().UTC(),
	})
}
```

- [ ] **Step 2: Run tests**

Run: `cd backend && go test ./internal/certmanager/ -run "TestThresholdBucket|TestDedupeEmitOncePerCrossing" -v`
Expected: both PASS.

- [ ] **Step 3: Run full package tests**

Run: `cd backend && go test ./internal/certmanager/... -v`
Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/certmanager/poller.go backend/internal/certmanager/poller_test.go
git commit -m "feat(certmanager): expiry poller with dedupe + Notification Center emit"
```

---

## Task 12: Wire routes + main

**Files:**
- Modify: `backend/internal/server/server.go` (or wherever `Server` struct is defined)
- Modify: `backend/internal/server/routes.go`
- Modify: `backend/cmd/kubecenter/main.go`

- [ ] **Step 1: Find where VeleroHandler is declared on Server struct**

Run: `grep -n "VeleroHandler\|VeleroDiscoverer" backend/internal/server/*.go`
Expected: finds field declaration in server.go (or similar).

- [ ] **Step 2: Add CertManagerHandler field**

Edit the Server struct to append next to VeleroHandler:

```go
	VeleroHandler     *velero.Handler
	CertManagerHandler *certmanager.Handler
```

Add the import if needed:

```go
	"github.com/kubecenter/kubecenter/internal/certmanager"
```

- [ ] **Step 3: Add registerCertManagerRoutes**

In `backend/internal/server/routes.go`, find the `registerVeleroRoutes` function and add a sibling function right after it:

```go
func (s *Server) registerCertManagerRoutes(ar chi.Router) {
	h := s.CertManagerHandler
	ar.Route("/certificates", func(cr chi.Router) {
		yamlRL := s.YAMLRateLimiter
		if yamlRL == nil {
			yamlRL = s.RateLimiter
		}
		// Read
		cr.Get("/status", h.HandleStatus)
		cr.Get("/certificates", h.HandleListCertificates)
		cr.With(resources.ValidateURLParams).Get("/certificates/{namespace}/{name}", h.HandleGetCertificate)
		cr.Get("/issuers", h.HandleListIssuers)
		cr.Get("/clusterissuers", h.HandleListClusterIssuers)
		cr.Get("/expiring", h.HandleListExpiring)

		// Write (rate-limited, param-validated)
		cr.With(middleware.RateLimit(yamlRL), resources.ValidateURLParams).
			Post("/certificates/{namespace}/{name}/renew", h.HandleRenew)
		cr.With(middleware.RateLimit(yamlRL), resources.ValidateURLParams).
			Post("/certificates/{namespace}/{name}/reissue", h.HandleReissue)
	})
}
```

- [ ] **Step 4: Wire the call site**

In `routes.go`, find the existing `if s.VeleroHandler != nil { s.registerVeleroRoutes(ar) }` block and add below it:

```go
			if s.CertManagerHandler != nil {
				s.registerCertManagerRoutes(ar)
			}
```

- [ ] **Step 5: Construct discoverer/handler/poller in main.go**

Find the Velero construction block in `backend/cmd/kubecenter/main.go` and add right after it:

```go
	cmDiscoverer := certmanager.NewDiscoverer(k8sClient, logger)
	cmHandler := certmanager.NewHandler(k8sClient, cmDiscoverer, accessChecker, auditLogger, notifService, logger)
	cmPoller := certmanager.NewPoller(k8sClient, cmDiscoverer, notifService, logger)
	go cmPoller.Start(ctx)
	srv.CertManagerHandler = cmHandler
```

(Adjust variable names — e.g. `srv`, `k8sClient`, `notifService`, `accessChecker`, `auditLogger` — to match the ones actually used in main.go.)

Add import:

```go
	"github.com/kubecenter/kubecenter/internal/certmanager"
```

- [ ] **Step 6: Build the whole backend**

Run: `cd backend && go build ./...`
Expected: no output, exit 0. Fix any compile errors one by one.

- [ ] **Step 7: Run full backend tests**

Run: `cd backend && go test ./...`
Expected: all tests pass.

- [ ] **Step 8: Run go vet**

Run: `cd backend && go vet ./...`
Expected: no output.

- [ ] **Step 9: Commit**

```bash
git add backend/internal/server/ backend/cmd/kubecenter/main.go
git commit -m "feat(certmanager): wire handler, poller, and routes into server"
```

---

## Task 13: Smoke test against running backend

**Files:** none

- [ ] **Step 1: Start the backend locally**

```bash
make dev-db
KUBECENTER_DEV=true KUBECENTER_AUTH_JWTSECRET="dev-secret-thirty-two-bytes-xx!" make dev-backend
```

- [ ] **Step 2: Login and hit status endpoint**

```bash
TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -H "X-Requested-With: XMLHttpRequest" \
  -d '{"username":"admin","password":"admin123"}' | jq -r .data.accessToken)
curl -s http://localhost:8080/api/v1/certificates/status \
  -H "Authorization: Bearer $TOKEN" | jq
```

Expected: JSON with `{detected: true|false, lastChecked: "..."}`. On a dev kubeconfig with no cert-manager, `detected: false` is fine.

- [ ] **Step 3: Verify list endpoint returns 200**

```bash
curl -s -w "\n%{http_code}\n" http://localhost:8080/api/v1/certificates/certificates \
  -H "Authorization: Bearer $TOKEN"
```

Expected: 200 if cert-manager is installed (list), 503 if not installed. Either is acceptable.

- [ ] **Step 4: Commit** (nothing to commit; verification only)

---

## Task 14: Frontend — TypeScript types

**Files:**
- Create: `frontend/lib/certmanager-types.ts`

- [ ] **Step 1: Write the file**

```typescript
export type CertStatus = "Ready" | "Issuing" | "Failed" | "Expiring" | "Expired" | "Unknown";

export interface IssuerRef {
  name: string;
  kind: string;
  group?: string;
}

export interface Certificate {
  name: string;
  namespace: string;
  status: CertStatus;
  reason?: string;
  message?: string;
  issuerRef: IssuerRef;
  secretName: string;
  dnsNames?: string[];
  ipAddresses?: string[];
  uris?: string[];
  commonName?: string;
  duration?: string;
  renewBefore?: string;
  notBefore?: string;
  notAfter?: string;
  renewalTime?: string;
  daysRemaining?: number;
  uid: string;
  labels?: Record<string, string>;
}

export interface Issuer {
  name: string;
  namespace?: string;
  scope: "Namespaced" | "Cluster";
  type: "ACME" | "CA" | "Vault" | "SelfSigned" | "Unknown";
  ready: boolean;
  reason?: string;
  message?: string;
  acmeEmail?: string;
  acmeServer?: string;
  uid: string;
  updatedAt: string;
}

export interface CertificateRequest {
  name: string;
  namespace: string;
  status: CertStatus;
  reason?: string;
  message?: string;
  issuerRef: IssuerRef;
  createdAt: string;
  finishedAt?: string;
  uid: string;
}

export interface Order {
  name: string;
  namespace: string;
  state: string;
  reason?: string;
  url?: string;
  createdAt: string;
  uid: string;
  crName?: string;
}

export interface Challenge {
  name: string;
  namespace: string;
  type: string;
  state: string;
  reason?: string;
  dnsName?: string;
  token?: string;
  createdAt: string;
  uid: string;
  orderName?: string;
}

export interface CertificateDetail {
  certificate: Certificate;
  certificateRequests?: CertificateRequest[];
  orders?: Order[];
  challenges?: Challenge[];
}

export interface ExpiringCertificate {
  namespace: string;
  name: string;
  uid: string;
  issuerName: string;
  secretName: string;
  notAfter: string;
  daysRemaining: number;
  severity: "warning" | "critical" | "expired";
}

export interface CertManagerStatus {
  detected: boolean;
  namespace?: string;
  version?: string;
  lastChecked: string;
}
```

- [ ] **Step 2: Type-check**

Run: `cd frontend && deno check lib/certmanager-types.ts`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/lib/certmanager-types.ts
git commit -m "feat(frontend): cert-manager TypeScript types"
```

---

## Task 15: Frontend — Badges component

**Files:**
- Create: `frontend/components/ui/CertificateBadges.tsx`

- [ ] **Step 1: Read an existing badges file for the token pattern**

Run: Read tool on `frontend/components/ui/PolicyBadges.tsx` (if it exists). Note use of `var(--success)`, `var(--warning)`, `var(--danger)`, `var(--accent)` for theming.

- [ ] **Step 2: Write CertificateBadges.tsx**

```tsx
import type { CertStatus, Issuer } from "../../lib/certmanager-types.ts";

interface StatusBadgeProps {
  status: CertStatus;
}

export function StatusBadge({ status }: StatusBadgeProps) {
  const styles: Record<CertStatus, { bg: string; label: string }> = {
    Ready:    { bg: "var(--success)",  label: "Ready" },
    Issuing:  { bg: "var(--accent)",   label: "Issuing" },
    Failed:   { bg: "var(--danger)",   label: "Failed" },
    Expiring: { bg: "var(--warning)",  label: "Expiring" },
    Expired:  { bg: "var(--danger)",   label: "Expired" },
    Unknown:  { bg: "var(--muted)",    label: "Unknown" },
  };
  const s = styles[status];
  return (
    <span
      class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium"
      style={{ backgroundColor: s.bg, color: "white" }}
    >
      {s.label}
    </span>
  );
}

export function IssuerTypeBadge({ type }: { type: Issuer["type"] }) {
  const color = type === "ACME" ? "var(--accent)"
    : type === "CA" ? "var(--success)"
    : type === "Vault" ? "var(--warning)"
    : "var(--muted)";
  return (
    <span class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium"
      style={{ backgroundColor: color, color: "white" }}>
      {type}
    </span>
  );
}

interface ExpiryBadgeProps {
  daysRemaining?: number;
}

export function ExpiryBadge({ daysRemaining }: ExpiryBadgeProps) {
  if (daysRemaining === undefined) {
    return <span class="text-xs" style={{ color: "var(--muted)" }}>—</span>;
  }
  if (daysRemaining < 0) {
    return (
      <span class="text-xs font-semibold" style={{ color: "var(--danger)" }}>
        Expired
      </span>
    );
  }
  if (daysRemaining <= 7) {
    return (
      <span class="text-xs font-semibold" style={{ color: "var(--danger)" }}>
        {daysRemaining}d left
      </span>
    );
  }
  if (daysRemaining <= 30) {
    return (
      <span class="text-xs font-semibold" style={{ color: "var(--warning)" }}>
        {daysRemaining}d left
      </span>
    );
  }
  return <span class="text-xs" style={{ color: "var(--muted)" }}>{daysRemaining}d</span>;
}
```

- [ ] **Step 3: Type-check and format**

Run: `cd frontend && deno check components/ui/CertificateBadges.tsx && deno fmt components/ui/CertificateBadges.tsx`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add frontend/components/ui/CertificateBadges.tsx
git commit -m "feat(frontend): cert-manager status badges"
```

---

## Task 16: Frontend — CertificatesList island

**Files:**
- Create: `frontend/islands/CertificatesList.tsx`

- [ ] **Step 1: Read an existing list island for the pattern**

Run: Read tool on `frontend/islands/PolicyDashboard.tsx` or similar to see how lists call `api` and render tables.

- [ ] **Step 2: Write the island**

```tsx
import { useEffect, useState } from "preact/hooks";
import { api } from "../lib/api.ts";
import type { Certificate } from "../lib/certmanager-types.ts";
import { ExpiryBadge, StatusBadge } from "../components/ui/CertificateBadges.tsx";

interface Props {
  namespace?: string;
}

interface ListResponse {
  data: Certificate[];
  metadata?: { total: number };
}

export default function CertificatesList({ namespace }: Props) {
  const [certs, setCerts] = useState<Certificate[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState("");

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    const qs = namespace ? `?namespace=${encodeURIComponent(namespace)}` : "";
    api.get<ListResponse>(`/certificates/certificates${qs}`)
      .then((r) => {
        if (!cancelled) {
          setCerts(r.data ?? []);
          setError(null);
        }
      })
      .catch((e) => !cancelled && setError(String(e)))
      .finally(() => !cancelled && setLoading(false));
    return () => { cancelled = true; };
  }, [namespace]);

  const filtered = certs.filter((c) =>
    !filter || c.name.includes(filter) || c.namespace.includes(filter) || c.issuerRef.name.includes(filter)
  );

  if (loading) return <div class="p-4 text-sm" style={{ color: "var(--muted)" }}>Loading…</div>;
  if (error) return <div class="p-4 text-sm" style={{ color: "var(--danger)" }}>Error: {error}</div>;

  return (
    <div class="space-y-3">
      <input
        type="text"
        placeholder="Filter by name, namespace, or issuer…"
        value={filter}
        onInput={(e) => setFilter((e.target as HTMLInputElement).value)}
        class="w-full px-3 py-2 rounded text-sm"
        style={{ backgroundColor: "var(--surface-2)", color: "var(--text)" }}
      />
      <div class="overflow-x-auto">
        <table class="min-w-full text-sm">
          <thead>
            <tr style={{ color: "var(--muted)" }}>
              <th class="text-left py-2 px-2">Name</th>
              <th class="text-left py-2 px-2">Namespace</th>
              <th class="text-left py-2 px-2">Status</th>
              <th class="text-left py-2 px-2">Issuer</th>
              <th class="text-left py-2 px-2">DNS Names</th>
              <th class="text-left py-2 px-2">Expires</th>
            </tr>
          </thead>
          <tbody>
            {filtered.map((c) => (
              <tr key={c.uid} class="border-t" style={{ borderColor: "var(--border)" }}>
                <td class="py-2 px-2">
                  <a href={`/security/certificates/certificates/${c.namespace}/${c.name}`} style={{ color: "var(--accent)" }}>
                    {c.name}
                  </a>
                </td>
                <td class="py-2 px-2">{c.namespace}</td>
                <td class="py-2 px-2"><StatusBadge status={c.status} /></td>
                <td class="py-2 px-2">{c.issuerRef.name} <span style={{ color: "var(--muted)" }}>({c.issuerRef.kind})</span></td>
                <td class="py-2 px-2 truncate max-w-xs">{(c.dnsNames ?? []).join(", ")}</td>
                <td class="py-2 px-2"><ExpiryBadge daysRemaining={c.daysRemaining} /></td>
              </tr>
            ))}
          </tbody>
        </table>
        {filtered.length === 0 && (
          <div class="p-6 text-center text-sm" style={{ color: "var(--muted)" }}>No certificates found.</div>
        )}
      </div>
    </div>
  );
}
```

- [ ] **Step 3: Type-check and format**

Run: `cd frontend && deno check islands/CertificatesList.tsx && deno fmt islands/CertificatesList.tsx`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add frontend/islands/CertificatesList.tsx
git commit -m "feat(frontend): CertificatesList island"
```

---

## Task 17: Frontend — IssuersList island

**Files:**
- Create: `frontend/islands/IssuersList.tsx`

- [ ] **Step 1: Write the island**

```tsx
import { useEffect, useState } from "preact/hooks";
import { api } from "../lib/api.ts";
import type { Issuer } from "../lib/certmanager-types.ts";
import { IssuerTypeBadge } from "../components/ui/CertificateBadges.tsx";

interface Resp { data: Issuer[]; }

export default function IssuersList() {
  const [items, setItems] = useState<Issuer[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    async function load() {
      setLoading(true);
      try {
        const [ns, cl] = await Promise.all([
          api.get<Resp>("/certificates/issuers"),
          api.get<Resp>("/certificates/clusterissuers"),
        ]);
        if (!cancelled) {
          setItems([...(ns.data ?? []), ...(cl.data ?? [])]);
          setError(null);
        }
      } catch (e) {
        if (!cancelled) setError(String(e));
      } finally {
        if (!cancelled) setLoading(false);
      }
    }
    load();
    return () => { cancelled = true; };
  }, []);

  if (loading) return <div class="p-4 text-sm" style={{ color: "var(--muted)" }}>Loading…</div>;
  if (error) return <div class="p-4 text-sm" style={{ color: "var(--danger)" }}>Error: {error}</div>;

  return (
    <div class="overflow-x-auto">
      <table class="min-w-full text-sm">
        <thead>
          <tr style={{ color: "var(--muted)" }}>
            <th class="text-left py-2 px-2">Name</th>
            <th class="text-left py-2 px-2">Scope</th>
            <th class="text-left py-2 px-2">Namespace</th>
            <th class="text-left py-2 px-2">Type</th>
            <th class="text-left py-2 px-2">Ready</th>
            <th class="text-left py-2 px-2">Details</th>
          </tr>
        </thead>
        <tbody>
          {items.map((i) => (
            <tr key={i.uid} class="border-t" style={{ borderColor: "var(--border)" }}>
              <td class="py-2 px-2">{i.name}</td>
              <td class="py-2 px-2">{i.scope}</td>
              <td class="py-2 px-2">{i.namespace ?? "—"}</td>
              <td class="py-2 px-2"><IssuerTypeBadge type={i.type} /></td>
              <td class="py-2 px-2" style={{ color: i.ready ? "var(--success)" : "var(--danger)" }}>
                {i.ready ? "Yes" : "No"}
              </td>
              <td class="py-2 px-2 text-xs" style={{ color: "var(--muted)" }}>
                {i.acmeServer ?? i.reason ?? ""}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
```

- [ ] **Step 2: Type-check and format**

Run: `cd frontend && deno check islands/IssuersList.tsx && deno fmt islands/IssuersList.tsx`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/islands/IssuersList.tsx
git commit -m "feat(frontend): IssuersList island"
```

---

## Task 18: Frontend — ExpiryDashboard island

**Files:**
- Create: `frontend/islands/ExpiryDashboard.tsx`

- [ ] **Step 1: Write the island**

```tsx
import { useEffect, useState } from "preact/hooks";
import { api } from "../lib/api.ts";
import type { ExpiringCertificate } from "../lib/certmanager-types.ts";

interface Resp { data: ExpiringCertificate[]; }

export default function ExpiryDashboard() {
  const [items, setItems] = useState<ExpiringCertificate[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    api.get<Resp>("/certificates/expiring")
      .then((r) => !cancelled && setItems(r.data ?? []))
      .catch((e) => !cancelled && setError(String(e)))
      .finally(() => !cancelled && setLoading(false));
    return () => { cancelled = true; };
  }, []);

  const expired = items.filter((i) => i.severity === "expired").length;
  const critical = items.filter((i) => i.severity === "critical").length;
  const warning = items.filter((i) => i.severity === "warning").length;

  if (loading) return <div class="p-4 text-sm" style={{ color: "var(--muted)" }}>Loading…</div>;
  if (error) return <div class="p-4 text-sm" style={{ color: "var(--danger)" }}>Error: {error}</div>;

  return (
    <div class="space-y-4">
      <div class="grid grid-cols-3 gap-3">
        <Tile label="Expired" count={expired} color="var(--danger)" />
        <Tile label="< 7 days" count={critical} color="var(--danger)" />
        <Tile label="< 30 days" count={warning} color="var(--warning)" />
      </div>
      <div class="overflow-x-auto">
        <table class="min-w-full text-sm">
          <thead>
            <tr style={{ color: "var(--muted)" }}>
              <th class="text-left py-2 px-2">Namespace</th>
              <th class="text-left py-2 px-2">Name</th>
              <th class="text-left py-2 px-2">Issuer</th>
              <th class="text-left py-2 px-2">Days Remaining</th>
              <th class="text-left py-2 px-2">Expires</th>
            </tr>
          </thead>
          <tbody>
            {items.map((i) => (
              <tr key={i.uid} class="border-t" style={{ borderColor: "var(--border)" }}>
                <td class="py-2 px-2">{i.namespace}</td>
                <td class="py-2 px-2">
                  <a href={`/security/certificates/certificates/${i.namespace}/${i.name}`} style={{ color: "var(--accent)" }}>
                    {i.name}
                  </a>
                </td>
                <td class="py-2 px-2">{i.issuerName}</td>
                <td class="py-2 px-2" style={{ color: severityColor(i.severity) }}>{i.daysRemaining}</td>
                <td class="py-2 px-2 text-xs" style={{ color: "var(--muted)" }}>{new Date(i.notAfter).toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {items.length === 0 && (
          <div class="p-6 text-center text-sm" style={{ color: "var(--muted)" }}>No certificates nearing expiry.</div>
        )}
      </div>
    </div>
  );
}

function Tile({ label, count, color }: { label: string; count: number; color: string }) {
  return (
    <div class="p-4 rounded" style={{ backgroundColor: "var(--surface-2)" }}>
      <div class="text-xs" style={{ color: "var(--muted)" }}>{label}</div>
      <div class="text-3xl font-bold" style={{ color }}>{count}</div>
    </div>
  );
}

function severityColor(s: ExpiringCertificate["severity"]): string {
  if (s === "expired" || s === "critical") return "var(--danger)";
  return "var(--warning)";
}
```

- [ ] **Step 2: Type-check and format**

Run: `cd frontend && deno check islands/ExpiryDashboard.tsx && deno fmt islands/ExpiryDashboard.tsx`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/islands/ExpiryDashboard.tsx
git commit -m "feat(frontend): ExpiryDashboard island"
```

---

## Task 19: Frontend — CertificateDetail island

**Files:**
- Create: `frontend/islands/CertificateDetail.tsx`

- [ ] **Step 1: Write the island**

```tsx
import { useEffect, useState } from "preact/hooks";
import { api } from "../lib/api.ts";
import type { CertificateDetail as Detail } from "../lib/certmanager-types.ts";
import { ExpiryBadge, StatusBadge } from "../components/ui/CertificateBadges.tsx";

interface Props {
  namespace: string;
  name: string;
}

interface Resp { data: Detail; }

export default function CertificateDetail({ namespace, name }: Props) {
  const [detail, setDetail] = useState<Detail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [actionMsg, setActionMsg] = useState<string | null>(null);
  const [confirmReissue, setConfirmReissue] = useState(false);

  const load = () => {
    setLoading(true);
    api.get<Resp>(`/certificates/certificates/${namespace}/${name}`)
      .then((r) => { setDetail(r.data); setError(null); })
      .catch((e) => setError(String(e)))
      .finally(() => setLoading(false));
  };

  useEffect(load, [namespace, name]);

  async function doRenew() {
    setActionMsg(null);
    try {
      await api.post(`/certificates/certificates/${namespace}/${name}/renew`, {});
      setActionMsg("Renewal triggered.");
      setTimeout(load, 1500);
    } catch (e) {
      setActionMsg(`Renew failed: ${String(e)}`);
    }
  }

  async function doReissue() {
    setActionMsg(null);
    setConfirmReissue(false);
    try {
      await api.post(`/certificates/certificates/${namespace}/${name}/reissue`, {});
      setActionMsg("Re-issue triggered.");
      setTimeout(load, 1500);
    } catch (e) {
      setActionMsg(`Re-issue failed: ${String(e)}`);
    }
  }

  if (loading) return <div class="p-4 text-sm" style={{ color: "var(--muted)" }}>Loading…</div>;
  if (error) return <div class="p-4 text-sm" style={{ color: "var(--danger)" }}>Error: {error}</div>;
  if (!detail) return null;

  const c = detail.certificate;
  return (
    <div class="space-y-4">
      <div class="flex items-center gap-3">
        <h1 class="text-xl font-semibold">{c.name}</h1>
        <StatusBadge status={c.status} />
        <ExpiryBadge daysRemaining={c.daysRemaining} />
      </div>
      <div class="flex gap-2">
        <button
          class="px-3 py-1.5 rounded text-sm"
          style={{ backgroundColor: "var(--accent)", color: "white" }}
          onClick={doRenew}
        >
          Renew
        </button>
        <button
          class="px-3 py-1.5 rounded text-sm"
          style={{ backgroundColor: "var(--danger)", color: "white" }}
          onClick={() => setConfirmReissue(true)}
        >
          Re-issue
        </button>
        {actionMsg && <span class="text-sm self-center" style={{ color: "var(--muted)" }}>{actionMsg}</span>}
      </div>
      {confirmReissue && (
        <div class="p-3 rounded" style={{ backgroundColor: "var(--surface-2)" }}>
          <div class="text-sm mb-2">
            Re-issue will delete the Secret <code>{c.secretName}</code> in <code>{c.namespace}</code> and force a fresh issuance.
            Applications using this Secret will briefly lose TLS until cert-manager completes re-issue. Continue?
          </div>
          <div class="flex gap-2">
            <button class="px-3 py-1 rounded text-xs" style={{ backgroundColor: "var(--danger)", color: "white" }} onClick={doReissue}>
              Yes, re-issue
            </button>
            <button class="px-3 py-1 rounded text-xs" style={{ backgroundColor: "var(--surface)", color: "var(--text)" }} onClick={() => setConfirmReissue(false)}>
              Cancel
            </button>
          </div>
        </div>
      )}

      <section>
        <h2 class="text-sm font-semibold mb-2" style={{ color: "var(--muted)" }}>Details</h2>
        <dl class="grid grid-cols-2 gap-x-6 gap-y-1 text-sm">
          <dt style={{ color: "var(--muted)" }}>Namespace</dt><dd>{c.namespace}</dd>
          <dt style={{ color: "var(--muted)" }}>Issuer</dt><dd>{c.issuerRef.kind}/{c.issuerRef.name}</dd>
          <dt style={{ color: "var(--muted)" }}>Secret</dt>
          <dd>
            <a href={`/workloads/secrets/${c.namespace}/${c.secretName}`} style={{ color: "var(--accent)" }}>
              {c.secretName}
            </a>
          </dd>
          <dt style={{ color: "var(--muted)" }}>DNS Names</dt><dd>{(c.dnsNames ?? []).join(", ") || "—"}</dd>
          <dt style={{ color: "var(--muted)" }}>Common Name</dt><dd>{c.commonName ?? "—"}</dd>
          <dt style={{ color: "var(--muted)" }}>Not Before</dt><dd>{c.notBefore ?? "—"}</dd>
          <dt style={{ color: "var(--muted)" }}>Not After</dt><dd>{c.notAfter ?? "—"}</dd>
          <dt style={{ color: "var(--muted)" }}>Renewal Time</dt><dd>{c.renewalTime ?? "—"}</dd>
        </dl>
      </section>

      {(detail.certificateRequests?.length ?? 0) > 0 && (
        <section>
          <h2 class="text-sm font-semibold mb-2" style={{ color: "var(--muted)" }}>Certificate Requests</h2>
          <table class="min-w-full text-xs">
            <thead>
              <tr style={{ color: "var(--muted)" }}>
                <th class="text-left py-1 px-2">Name</th>
                <th class="text-left py-1 px-2">Status</th>
                <th class="text-left py-1 px-2">Reason</th>
                <th class="text-left py-1 px-2">Created</th>
              </tr>
            </thead>
            <tbody>
              {detail.certificateRequests!.map((cr) => (
                <tr key={cr.uid} class="border-t" style={{ borderColor: "var(--border)" }}>
                  <td class="py-1 px-2">{cr.name}</td>
                  <td class="py-1 px-2"><StatusBadge status={cr.status} /></td>
                  <td class="py-1 px-2">{cr.reason ?? "—"}</td>
                  <td class="py-1 px-2">{new Date(cr.createdAt).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}

      {(detail.orders?.length ?? 0) > 0 && (
        <section>
          <h2 class="text-sm font-semibold mb-2" style={{ color: "var(--muted)" }}>ACME Orders</h2>
          <table class="min-w-full text-xs">
            <thead>
              <tr style={{ color: "var(--muted)" }}>
                <th class="text-left py-1 px-2">Name</th>
                <th class="text-left py-1 px-2">State</th>
                <th class="text-left py-1 px-2">Reason</th>
              </tr>
            </thead>
            <tbody>
              {detail.orders!.map((o) => (
                <tr key={o.uid} class="border-t" style={{ borderColor: "var(--border)" }}>
                  <td class="py-1 px-2">{o.name}</td>
                  <td class="py-1 px-2">{o.state}</td>
                  <td class="py-1 px-2">{o.reason ?? "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}

      {(detail.challenges?.length ?? 0) > 0 && (
        <section>
          <h2 class="text-sm font-semibold mb-2" style={{ color: "var(--muted)" }}>ACME Challenges</h2>
          <table class="min-w-full text-xs">
            <thead>
              <tr style={{ color: "var(--muted)" }}>
                <th class="text-left py-1 px-2">Name</th>
                <th class="text-left py-1 px-2">Type</th>
                <th class="text-left py-1 px-2">DNS</th>
                <th class="text-left py-1 px-2">State</th>
                <th class="text-left py-1 px-2">Reason</th>
              </tr>
            </thead>
            <tbody>
              {detail.challenges!.map((ch) => (
                <tr key={ch.uid} class="border-t" style={{ borderColor: "var(--border)" }}>
                  <td class="py-1 px-2">{ch.name}</td>
                  <td class="py-1 px-2">{ch.type}</td>
                  <td class="py-1 px-2">{ch.dnsName ?? "—"}</td>
                  <td class="py-1 px-2">{ch.state}</td>
                  <td class="py-1 px-2">{ch.reason ?? "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}
    </div>
  );
}
```

- [ ] **Step 2: Type-check and format**

Run: `cd frontend && deno check islands/CertificateDetail.tsx && deno fmt islands/CertificateDetail.tsx`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/islands/CertificateDetail.tsx
git commit -m "feat(frontend): CertificateDetail island with renew/reissue actions"
```

---

## Task 20: Frontend — CertificateStatusBanner island

**Files:**
- Create: `frontend/islands/CertificateStatusBanner.tsx`

- [ ] **Step 1: Write the island**

```tsx
import { useEffect, useState } from "preact/hooks";
import { api } from "../lib/api.ts";
import type { ExpiringCertificate } from "../lib/certmanager-types.ts";

interface Resp { data: ExpiringCertificate[]; }

export default function CertificateStatusBanner() {
  const [critical, setCritical] = useState(0);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    api.get<Resp>("/certificates/expiring")
      .then((r) => {
        const items = r.data ?? [];
        setCritical(items.filter((i) => i.severity === "critical" || i.severity === "expired").length);
      })
      .catch(() => {/* ignore — cert-manager may not be installed */})
      .finally(() => setLoaded(true));
  }, []);

  if (!loaded || critical === 0) return null;
  return (
    <a
      href="/security/certificates/expiring"
      class="block px-3 py-2 rounded text-sm mb-3"
      style={{ backgroundColor: "var(--danger)", color: "white" }}
    >
      <strong>{critical}</strong> certificate{critical === 1 ? "" : "s"} need attention — expiring within 7 days or expired.
    </a>
  );
}
```

- [ ] **Step 2: Type-check and format**

Run: `cd frontend && deno check islands/CertificateStatusBanner.tsx && deno fmt islands/CertificateStatusBanner.tsx`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/islands/CertificateStatusBanner.tsx
git commit -m "feat(frontend): CertificateStatusBanner island"
```

---

## Task 21: Frontend — routes

**Files:**
- Create: `frontend/routes/security/certificates/index.tsx`
- Create: `frontend/routes/security/certificates/certificates.tsx`
- Create: `frontend/routes/security/certificates/certificates/[namespace]/[name].tsx`
- Create: `frontend/routes/security/certificates/issuers.tsx`
- Create: `frontend/routes/security/certificates/expiring.tsx`

- [ ] **Step 1: Read an existing security route for the layout pattern**

Run: Read tool on `frontend/routes/security/policies.tsx` to see the shell structure.

- [ ] **Step 2: Write index.tsx (redirect)**

```tsx
import { Handlers } from "$fresh/server.ts";

export const handler: Handlers = {
  GET() {
    return new Response(null, { status: 302, headers: { Location: "/security/certificates/certificates" } });
  },
};
```

- [ ] **Step 3: Write certificates.tsx**

```tsx
import CertificatesList from "../../../islands/CertificatesList.tsx";

export default function CertificatesPage() {
  return (
    <div class="p-4">
      <h1 class="text-2xl font-semibold mb-4">Certificates</h1>
      <CertificatesList />
    </div>
  );
}
```

- [ ] **Step 4: Write issuers.tsx**

```tsx
import IssuersList from "../../../islands/IssuersList.tsx";

export default function IssuersPage() {
  return (
    <div class="p-4">
      <h1 class="text-2xl font-semibold mb-4">Issuers</h1>
      <IssuersList />
    </div>
  );
}
```

- [ ] **Step 5: Write expiring.tsx**

```tsx
import ExpiryDashboard from "../../../islands/ExpiryDashboard.tsx";

export default function ExpiringPage() {
  return (
    <div class="p-4">
      <h1 class="text-2xl font-semibold mb-4">Expiring Certificates</h1>
      <ExpiryDashboard />
    </div>
  );
}
```

- [ ] **Step 6: Write detail route `certificates/[namespace]/[name].tsx`**

```tsx
import { PageProps } from "$fresh/server.ts";
import CertificateDetail from "../../../../../islands/CertificateDetail.tsx";

export default function CertificateDetailPage(props: PageProps) {
  const ns = props.params.namespace;
  const name = props.params.name;
  return (
    <div class="p-4">
      <CertificateDetail namespace={ns} name={name} />
    </div>
  );
}
```

Note: count the `../` segments carefully against actual path depth. If Fresh's routing puts this at `/security/certificates/certificates/[namespace]/[name]`, the island import path is 5 levels up.

- [ ] **Step 7: Type-check and format all new routes**

Run: `cd frontend && deno check routes/security/certificates/**/*.tsx && deno fmt routes/security/certificates/`
Expected: no errors.

- [ ] **Step 8: Commit**

```bash
git add frontend/routes/security/certificates/
git commit -m "feat(frontend): cert-manager routes"
```

---

## Task 22: Frontend — SubNav + CommandPalette wiring

**Files:**
- Modify: Security SubNav config (whichever file defines it)
- Modify: `frontend/islands/CommandPalette.tsx`

- [ ] **Step 1: Find Security SubNav config**

Run: `grep -rn "vulnerabilities\|/security/policies\|SubNav" frontend/components/nav/ frontend/islands/ | head -20`
Expected: find the file that defines the Security section tabs (probably a config object).

- [ ] **Step 2: Add Certificates tab**

Add a tab entry that points to `/security/certificates/certificates` with label "Certificates". Follow the exact same shape as the existing "Policies", "Violations", "Vulnerabilities" entries.

- [ ] **Step 3: Add command palette entries**

In `CommandPalette.tsx`, find the existing quick-action list (grep for "Policies" or "Violations") and add:

```tsx
{ label: "Certificates", href: "/security/certificates/certificates", section: "Security" },
{ label: "Expiring certificates", href: "/security/certificates/expiring", section: "Security" },
```

(Match the existing object shape.)

- [ ] **Step 4: Type-check and format**

Run: `cd frontend && deno check islands/CommandPalette.tsx && deno fmt islands/CommandPalette.tsx components/nav/`
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add frontend/islands/CommandPalette.tsx frontend/components/nav/
git commit -m "feat(frontend): Certificates SubNav tab and command palette entries"
```

---

## Task 23: Full frontend verification

**Files:** none

- [ ] **Step 1: deno fmt check**

Run: `cd frontend && deno fmt --check`
Expected: no output, exit 0.

- [ ] **Step 2: deno lint**

Run: `cd frontend && deno lint`
Expected: no errors.

- [ ] **Step 3: deno check full project**

Run: `cd frontend && deno task check` (or whatever the project uses for full type-check; fall back to `deno check main.ts` or `deno check dev.ts`)
Expected: no errors.

- [ ] **Step 4: Build**

Run: `cd frontend && deno task build`
Expected: successful build.

- [ ] **Step 5: Manual smoke**

Run `make dev` in one terminal, open `http://localhost:5173/security/certificates/certificates` in browser. Verify the page renders, the table shows (empty list or real certs), navigation tab appears, and no console errors.

- [ ] **Step 6: Commit** (nothing to commit; verification only)

---

## Task 24: E2E test

**Files:**
- Create: `e2e/tests/certificates.spec.ts`

- [ ] **Step 1: Read an existing e2e spec for the skip pattern**

Run: Read tool on `e2e/tests/policies.spec.ts` (or whichever spec uses a skip-on-unavailable pattern).

- [ ] **Step 2: Write the spec**

```typescript
import { expect, test } from "@playwright/test";

test.describe("cert-manager", () => {
  test("list page loads and opens a detail panel", async ({ page, request }) => {
    await page.goto("/security/certificates/certificates");
    await page.waitForLoadState("networkidle");

    // Skip if cert-manager not installed
    const statusResp = await request.get("/api/v1/certificates/status");
    const statusJson = await statusResp.json();
    test.skip(!statusJson.data?.detected, "cert-manager not installed on this cluster");

    await expect(page.locator("h1")).toHaveText("Certificates");

    // Table should render (may be empty)
    const rows = page.locator("tbody tr");
    const count = await rows.count();
    if (count > 0) {
      const firstLink = rows.first().locator("a").first();
      await firstLink.click();
      await expect(page.locator("h1")).toBeVisible();
    }
  });
});
```

- [ ] **Step 3: Run locally against dev**

Run: `cd e2e && npx playwright test tests/certificates.spec.ts`
Expected: test passes or skips (skip is fine if cert-manager absent).

- [ ] **Step 4: Commit**

```bash
git add e2e/tests/certificates.spec.ts
git commit -m "test(e2e): cert-manager list page happy path"
```

---

## Task 25: Docs update

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Re-read CLAUDE.md Build Progress section**

Run: Read tool on `CLAUDE.md` offset around the "Build Progress" section (roughly lines 190-280).

- [ ] **Step 2: Add Phase 11A entry under Build Progress**

Find the last entry (Post-Phase Enhancements). Append after it:

```markdown
- **Phase 11A (Cert-Manager Observatory):** COMPLETE
  - New `internal/certmanager/` package: CRD discovery, normalized types, dynamic client reads, singleflight + 30s cache, RBAC filtering via `CanAccessGroupResource`
  - 8 HTTP endpoints: `GET /certificates/{status,certificates,certificates/{ns}/{name},issuers,clusterissuers,expiring}`, `POST /certificates/certificates/{ns}/{name}/{renew,reissue}`
  - Background expiry poller (60s tick, local cluster only) emits `certificate.expiring`/`expired`/`failed` events to Notification Center with `(uid, threshold)` dedupe
  - 5 frontend islands: CertificatesList, CertificateDetail (with Renew/Re-issue actions), IssuersList, ExpiryDashboard, CertificateStatusBanner
  - 5 routes under `/security/certificates/*` with SubNav tab and command palette quick actions
  - Theme-compliant: CSS custom properties for all colors
```

- [ ] **Step 3: Check off roadmap item #7**

Find the "Future Features (Roadmap)" section. Change:

```markdown
- [ ] **7. Cert-Manager integration** — certificate inventory, expiry warnings, issuers management
```

to:

```markdown
- [x] **7. Cert-Manager integration** — certificate inventory, expiry warnings, issuers management (Phase 11A)
```

- [ ] **Step 4: Add Phase 11B placeholder**

Below the checked-off #7, add a new entry:

```markdown
- [ ] **7b. Cert-Manager wizards (Phase 11B)** — Certificate/Issuer/ClusterIssuer creation wizards (ACME HTTP01/DNS01, CA, Vault, SelfSigned), force-rotate action, configurable per-cert expiry thresholds
```

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: Phase 11A cert-manager observatory"
```

---

## Task 26: Full verification run

**Files:** none

- [ ] **Step 1: Go backend full test + vet + build**

Run these in parallel:
```bash
cd backend && go vet ./... && go test ./... && go build ./...
```
Expected: all pass.

- [ ] **Step 2: Frontend full check**

Run:
```bash
cd frontend && deno fmt --check && deno lint && deno task check && deno task build
```
Expected: all pass.

- [ ] **Step 3: Helm lint** (sanity — no chart changes expected)

Run: `make helm-lint`
Expected: pass.

- [ ] **Step 4: Commit** — nothing to commit; verification only.

---

## Task 27: Open PR

**Files:** none

- [ ] **Step 1: Push branch**

Run: `git push -u origin feat/cert-manager-design`

- [ ] **Step 2: Run /compounding-engineering:workflows:review before opening PR**

Per CLAUDE.md: "Before any merge: Run /compounding-engineering:workflows:review first. No exceptions."

Run the review workflow. Fix any issues it flags before opening the PR.

- [ ] **Step 3: Open PR**

```bash
gh pr create --title "feat: cert-manager observatory + lifecycle (Phase 11A)" --body "$(cat <<'EOF'
## Summary
- Adds `internal/certmanager/` package with CRD discovery, normalized Certificate/Issuer types, singleflight-cached read handlers, and user-impersonated detail endpoint nesting CertificateRequest/Order/Challenge
- Adds renew (status-subresource Issuing condition patch) and reissue (Secret delete) actions
- Adds background expiry poller that emits `certificate.expiring`/`expired`/`failed` events to Notification Center with per-(uid, threshold) dedupe
- Adds 5 frontend islands + 5 routes under `/security/certificates/*` with SubNav tab and command palette quick actions
- Roadmap item #7 complete; Phase 11B (creation wizards) queued

## Test plan
- [ ] `cd backend && go vet ./... && go test ./... && go build ./...` clean
- [ ] `cd frontend && deno fmt --check && deno lint && deno task check && deno task build` clean
- [ ] Homelab smoke: list loads, detail opens, renew round-trips, notification appears in feed when a test cert is near expiry
- [ ] Playwright: `e2e/tests/certificates.spec.ts` passes or skips cleanly

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 4: Watch CI**

Run: `gh run list --limit 1` and `gh run view` to confirm CI passes. Fix any failures before requesting merge.

---

## Self-Review Checklist

Completed during plan writing:

1. **Spec coverage:**
   - Backend package layout (discovery, types, normalize, handler, actions, poller, notifier, rbac) → Tasks 2-11
   - 8 HTTP endpoints → Task 8 (reads) + Task 9 (writes) + Task 12 (routes)
   - Notification source integration → Task 1 (const) + Task 11 (poller emit)
   - Frontend: 5 routes + 5 islands + shared types + badges → Tasks 14-21
   - SubNav + command palette → Task 22
   - Tests: discovery, normalization, poller/dedupe → Tasks 3, 5, 10
   - E2E Playwright → Task 24
   - Docs → Task 25
   - Phase 11B deferred explicitly in docs → Task 25 Step 4

2. **Placeholder scan:** No TBD/TODO. The `listAndNormalize` generic sketch in Task 8 Step 2 is explicitly flagged as broken with a corrected replacement immediately below it.

3. **Type consistency:** `Certificate.DaysRemaining` is `*int` in Go, `number | undefined` in TS — consistent. `Status` enum values match between Go and TS. `threshold` enum is internal to poller and not exposed.

4. **Known reconciliation points (called out in tasks):**
   - `auth.UserAndGroups` — Task 8 Step 4 verifies the real helper name
   - `K8sClient.ImpersonatedDynamic` / `ImpersonatedTyped` — Task 8 Step 5 + Task 9 Step 3
   - `audit.Event` field names — Task 9 Step 2
   - SubNav config file path — Task 22 Step 1 greps for it
   - Route import depth — Task 21 Step 6 note
