package certmanager

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

const (
	cacheTTL        = 30 * time.Second
	maxRenewRetries = 1
	issuingCondType = "Issuing"
	issuingReason   = "ManuallyTriggered"
	issuingMessage  = "Certificate re-issuance manually triggered"
)

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
	cacheGen   uint64
}

type cachedData struct {
	certificates   []Certificate
	issuers        []Issuer
	clusterIssuers []Issuer
	fetchedAt      time.Time
}

// NewHandler creates a new cert-manager handler.
func NewHandler(
	k8sClient *k8s.ClientFactory,
	discoverer *Discoverer,
	accessChecker *resources.AccessChecker,
	auditLogger audit.Logger,
	notifService *notifications.NotificationService,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		K8sClient:     k8sClient,
		Discoverer:    discoverer,
		AccessChecker: accessChecker,
		AuditLogger:   auditLogger,
		NotifService:  notifService,
		Logger:        logger,
	}
}

// InvalidateCache clears the cached data and emits a notification.
func (h *Handler) InvalidateCache() {
	h.cacheMu.Lock()
	h.cacheGen++
	h.cache = nil
	h.cacheMu.Unlock()

	if h.NotifService != nil {
		go h.NotifService.Emit(context.Background(), notifications.Notification{
			Source:   notifications.SourceCertManager,
			Severity: notifications.SeverityInfo,
			Title:    "cert-manager data updated",
			Message:  "Certificate or issuer data has changed",
		})
	}
}

// getImpersonatingClient creates a dynamic client impersonating the user and handles errors.
func (h *Handler) getImpersonatingClient(w http.ResponseWriter, user *auth.User) (dynamic.Interface, bool) {
	client, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return nil, false
	}
	return client, true
}

// canAccess checks if the user can access a cert-manager resource.
func (h *Handler) canAccess(ctx context.Context, user *auth.User, verb, resource, namespace string) bool {
	can, err := h.AccessChecker.CanAccessGroupResource(
		ctx,
		user.KubernetesUsername,
		user.KubernetesGroups,
		verb,
		"cert-manager.io",
		resource,
		namespace,
	)
	return err == nil && can
}

// auditLog writes an audit entry for a cert-manager action.
func (h *Handler) auditLog(r *http.Request, user *auth.User, action audit.Action, kind, ns, name string, result audit.Result) {
	if h.AuditLogger == nil {
		return
	}
	_ = h.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp:         time.Now(),
		ClusterID:         middleware.ClusterIDFromContext(r.Context()),
		User:              user.Username,
		SourceIP:          r.RemoteAddr,
		Action:            action,
		ResourceKind:      kind,
		ResourceNamespace: ns,
		ResourceName:      name,
		Result:            result,
	})
}

// namespacedResource is implemented by types that carry a Kubernetes namespace.
type namespacedResource interface {
	getNamespace() string
}

func (c Certificate) getNamespace() string { return c.Namespace }
func (i Issuer) getNamespace() string      { return i.Namespace }

// filterByRBAC returns only items the user can access in their respective namespaces.
func filterByRBAC[T namespacedResource](ctx context.Context, h *Handler, user *auth.User, resource string, items []T) []T {
	nsAllow := map[string]bool{}
	out := make([]T, 0, len(items))
	for _, item := range items {
		ns := item.getNamespace()
		allowed, ok := nsAllow[ns]
		if !ok {
			allowed = h.canAccess(ctx, user, "get", resource, ns)
			nsAllow[ns] = allowed
		}
		if allowed {
			out = append(out, item)
		}
	}
	return out
}

func (h *Handler) getCached(ctx context.Context) (*cachedData, error) {
	h.cacheMu.RLock()
	if h.cache != nil && time.Since(h.cache.fetchedAt) < cacheTTL {
		data := h.cache
		h.cacheMu.RUnlock()
		return data, nil
	}
	gen := h.cacheGen
	h.cacheMu.RUnlock()

	result, err, _ := h.fetchGroup.Do("all", func() (any, error) {
		return h.fetchAll(ctx, gen)
	})
	if err != nil {
		return nil, err
	}
	return result.(*cachedData), nil
}

func (h *Handler) fetchAll(ctx context.Context, gen uint64) (*cachedData, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	dynClient := h.K8sClient.BaseDynamicClient()

	var (
		certificates   []Certificate
		issuers        []Issuer
		clusterIssuers []Issuer
	)

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		list, err := dynClient.Resource(CertificateGVR).Namespace("").List(gctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("list certificates: %w", err)
		}
		certificates = make([]Certificate, 0, len(list.Items))
		for i := range list.Items {
			c, err := normalizeCertificate(&list.Items[i])
			if err != nil {
				continue
			}
			certificates = append(certificates, c)
		}
		return nil
	})

	g.Go(func() error {
		list, err := dynClient.Resource(IssuerGVR).Namespace("").List(gctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("list issuers: %w", err)
		}
		issuers = make([]Issuer, 0, len(list.Items))
		for i := range list.Items {
			issuers = append(issuers, normalizeIssuer(&list.Items[i], "Namespaced"))
		}
		return nil
	})

	g.Go(func() error {
		list, err := dynClient.Resource(ClusterIssuerGVR).Namespace("").List(gctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("list clusterissuers: %w", err)
		}
		clusterIssuers = make([]Issuer, 0, len(list.Items))
		for i := range list.Items {
			clusterIssuers = append(clusterIssuers, normalizeIssuer(&list.Items[i], "Cluster"))
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	data := &cachedData{
		certificates:   certificates,
		issuers:        issuers,
		clusterIssuers: clusterIssuers,
		fetchedAt:      time.Now(),
	}

	h.cacheMu.Lock()
	if h.cacheGen == gen {
		h.cache = data
	}
	h.cacheMu.Unlock()

	return data, nil
}

// CachedCertificates returns the cached certificate list (for use by the Poller).
func (h *Handler) CachedCertificates(ctx context.Context) ([]Certificate, error) {
	data, err := h.getCached(ctx)
	if err != nil {
		return nil, err
	}
	return data.certificates, nil
}

// HandleStatus returns the cert-manager detection status.
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	_, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	status := h.Discoverer.Status(r.Context())
	httputil.WriteData(w, status)
}

// HandleListCertificates returns all cert-manager certificates, optionally filtered by namespace.
func (h *Handler) HandleListCertificates(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteData(w, []Certificate{})
		return
	}

	data, err := h.getCached(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch certificates", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch certificates", "")
		return
	}

	filtered := filterByRBAC(r.Context(), h, user, "certificates", data.certificates)

	// Optional namespace filter
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		nsFiltered := make([]Certificate, 0, len(filtered))
		for _, c := range filtered {
			if c.Namespace == ns {
				nsFiltered = append(nsFiltered, c)
			}
		}
		filtered = nsFiltered
	}

	httputil.WriteData(w, filtered)
}

// HandleGetCertificate returns a single certificate with its sub-resources.
func (h *Handler) HandleGetCertificate(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.canAccess(r.Context(), user, "get", "certificates", ns) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, ok := h.getImpersonatingClient(w, user)
	if !ok {
		return
	}

	ctx := r.Context()

	// Fetch the certificate
	certObj, err := dynClient.Resource(CertificateGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("failed to get certificate", "namespace", ns, "name", name, "error", err)
		httputil.WriteError(w, http.StatusNotFound, "certificate not found", "")
		return
	}

	cert, err := normalizeCertificate(certObj)
	if err != nil {
		h.Logger.Error("failed to normalize certificate", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to parse certificate", "")
		return
	}

	// Fetch CertificateRequests for this certificate
	crSel := labels.Set{"cert-manager.io/certificate-name": name}.String()

	// Fetch CRs, Orders, Challenges in parallel
	g, gCtx := errgroup.WithContext(ctx)

	var crList *unstructured.UnstructuredList
	g.Go(func() error {
		var fetchErr error
		crList, fetchErr = dynClient.Resource(CertificateRequestGVR).Namespace(ns).List(gCtx, metav1.ListOptions{
			LabelSelector: crSel,
		})
		return fetchErr
	})

	var orderList *unstructured.UnstructuredList
	g.Go(func() error {
		var fetchErr error
		orderList, fetchErr = dynClient.Resource(OrderGVR).Namespace(ns).List(gCtx, metav1.ListOptions{})
		return fetchErr
	})

	var challengeList *unstructured.UnstructuredList
	g.Go(func() error {
		var fetchErr error
		challengeList, fetchErr = dynClient.Resource(ChallengeGVR).Namespace(ns).List(gCtx, metav1.ListOptions{})
		return fetchErr
	})

	if err := g.Wait(); err != nil {
		h.Logger.Debug("sub-resource fetch partial failure", "error", err)
	}

	// Process CertificateRequests
	var certRequests []CertificateRequest
	crUIDs := make(map[string]bool)
	if crList != nil {
		certRequests = make([]CertificateRequest, 0, len(crList.Items))
		for i := range crList.Items {
			cr := normalizeCertRequest(&crList.Items[i])
			certRequests = append(certRequests, cr)
			crUIDs[cr.UID] = true
		}
	} else {
		certRequests = []CertificateRequest{}
	}

	// Filter Orders owned by the CertificateRequests
	var orders []Order
	orderUIDs := make(map[string]bool)
	if orderList != nil {
		orders = make([]Order, 0)
		for i := range orderList.Items {
			owners := orderList.Items[i].GetOwnerReferences()
			for _, ref := range owners {
				if crUIDs[string(ref.UID)] {
					o := normalizeOrder(&orderList.Items[i])
					orders = append(orders, o)
					orderUIDs[o.UID] = true
					break
				}
			}
		}
	} else {
		orders = []Order{}
	}

	// Filter Challenges owned by the Orders
	var challenges []Challenge
	if challengeList != nil {
		challenges = make([]Challenge, 0)
		for i := range challengeList.Items {
			owners := challengeList.Items[i].GetOwnerReferences()
			for _, ref := range owners {
				if orderUIDs[string(ref.UID)] {
					challenges = append(challenges, normalizeChallenge(&challengeList.Items[i]))
					break
				}
			}
		}
	} else {
		challenges = []Challenge{}
	}

	detail := CertificateDetail{
		Certificate:         cert,
		CertificateRequests: certRequests,
		Orders:              orders,
		Challenges:          challenges,
	}

	httputil.WriteData(w, detail)
}

// HandleListIssuers returns all namespaced issuers.
func (h *Handler) HandleListIssuers(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteData(w, []Issuer{})
		return
	}

	data, err := h.getCached(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch issuers", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch issuers", "")
		return
	}

	filtered := filterByRBAC(r.Context(), h, user, "issuers", data.issuers)
	httputil.WriteData(w, filtered)
}

// HandleListClusterIssuers returns all cluster-scoped issuers.
func (h *Handler) HandleListClusterIssuers(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteData(w, []Issuer{})
		return
	}

	// Cluster-scoped RBAC check
	if !h.canAccess(r.Context(), user, "get", "clusterissuers", "") {
		httputil.WriteData(w, []Issuer{})
		return
	}

	data, err := h.getCached(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch cluster issuers", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch cluster issuers", "")
		return
	}

	httputil.WriteData(w, data.clusterIssuers)
}

// HandleListExpiring returns certificates expiring within the warning threshold,
// sorted by days remaining ascending.
func (h *Handler) HandleListExpiring(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteData(w, []ExpiringCertificate{})
		return
	}

	data, err := h.getCached(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch certificates", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch certificates", "")
		return
	}

	certs := filterByRBAC(r.Context(), h, user, "certificates", data.certificates)

	expiring := make([]ExpiringCertificate, 0)
	for _, c := range certs {
		if c.DaysRemaining == nil || *c.DaysRemaining > WarningThresholdDays {
			continue
		}
		severity := "warning"
		if *c.DaysRemaining <= CriticalThresholdDays {
			severity = "critical"
		}
		var notAfter time.Time
		if c.NotAfter != nil {
			notAfter = *c.NotAfter
		}
		expiring = append(expiring, ExpiringCertificate{
			Namespace:     c.Namespace,
			Name:          c.Name,
			UID:           c.UID,
			IssuerName:    c.IssuerRef.Name,
			SecretName:    c.SecretName,
			NotAfter:      notAfter,
			DaysRemaining: *c.DaysRemaining,
			Severity:      severity,
		})
	}

	sort.Slice(expiring, func(i, j int) bool {
		return expiring[i].DaysRemaining < expiring[j].DaysRemaining
	})

	httputil.WriteData(w, expiring)
}

// HandleRenew triggers certificate renewal by setting the Issuing condition on the status subresource.
func (h *Handler) HandleRenew(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	ctx := r.Context()

	// RBAC pre-check
	if !h.canAccess(ctx, user, "patch", "certificates", ns) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, ok := h.getImpersonatingClient(w, user)
	if !ok {
		return
	}

	var lastErr error
	for attempt := 0; attempt <= maxRenewRetries; attempt++ {
		lastErr = h.doRenew(ctx, dynClient, ns, name)
		if lastErr == nil {
			break
		}
		h.Logger.Warn("renew attempt failed", "attempt", attempt, "error", lastErr)
	}

	if lastErr != nil {
		h.Logger.Error("failed to renew certificate", "namespace", ns, "name", name, "error", lastErr)
		h.auditLog(r, user, audit.ActionCertRenew, "Certificate", ns, name, audit.ResultFailure)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to renew certificate", "")
		return
	}

	h.auditLog(r, user, audit.ActionCertRenew, "Certificate", ns, name, audit.ResultSuccess)
	h.InvalidateCache()

	w.WriteHeader(http.StatusAccepted)
	httputil.WriteData(w, map[string]string{"status": "renewing"})
}

// doRenew performs one attempt at setting the Issuing=True condition on the certificate status.
func (h *Handler) doRenew(ctx context.Context, dynClient dynamic.Interface, ns, name string) error {
	cert, err := dynClient.Resource(CertificateGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get certificate: %w", err)
	}

	generation := cert.GetGeneration()
	now := time.Now().UTC().Format(time.RFC3339)

	// Read existing status.conditions
	statusMap, _ := cert.Object["status"].(map[string]any)
	if statusMap == nil {
		statusMap = map[string]any{}
		cert.Object["status"] = statusMap
	}

	conditions, _ := statusMap["conditions"].([]any)

	// Upsert Issuing condition
	found := false
	for i, c := range conditions {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := cm["type"].(string); t == issuingCondType {
			found = true
			existingStatus, _ := cm["status"].(string)
			if existingStatus != "True" {
				cm["status"] = "True"
				cm["lastTransitionTime"] = now
			}
			// Always update reason, message, and observedGeneration
			cm["reason"] = issuingReason
			cm["message"] = issuingMessage
			cm["observedGeneration"] = generation
			conditions[i] = cm
			break
		}
	}

	if !found {
		conditions = append(conditions, map[string]any{
			"type":               issuingCondType,
			"status":             "True",
			"reason":             issuingReason,
			"message":            issuingMessage,
			"lastTransitionTime": now,
			"observedGeneration": generation,
		})
	}

	statusMap["conditions"] = conditions

	_, err = dynClient.Resource(CertificateGVR).Namespace(ns).UpdateStatus(ctx, cert, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	return nil
}

// HandleReissue forces certificate re-issuance by deleting the backing Secret.
func (h *Handler) HandleReissue(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	ctx := r.Context()

	// RBAC pre-check — need delete on secrets
	if !h.canAccess(ctx, user, "delete", "secrets", ns) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, ok := h.getImpersonatingClient(w, user)
	if !ok {
		return
	}

	// GET the Certificate
	certObj, err := dynClient.Resource(CertificateGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("failed to get certificate", "namespace", ns, "name", name, "error", err)
		httputil.WriteError(w, http.StatusNotFound, "certificate not found", "")
		return
	}

	// Extract spec.secretName
	secretName, _, _ := unstructured.NestedString(certObj.Object, "spec", "secretName")
	if secretName == "" {
		httputil.WriteError(w, http.StatusBadRequest, "certificate has no secretName", "")
		return
	}

	certUID := string(certObj.GetUID())

	// Get the typed clientset for secret operations
	cs, err := h.K8sClient.ClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create typed client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	// GET the Secret
	secret, err := cs.CoreV1().Secrets(ns).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("failed to get secret", "namespace", ns, "name", secretName, "error", err)
		httputil.WriteError(w, http.StatusNotFound, "backing secret not found", "")
		return
	}

	// Validate ownership: check Secret's ownerReferences for the Certificate's UID
	owned := false
	for _, ref := range secret.OwnerReferences {
		if string(ref.UID) == certUID {
			owned = true
			break
		}
	}
	if !owned {
		httputil.WriteError(w, http.StatusBadRequest, "secret not owned by this certificate", "")
		return
	}

	// Delete the Secret
	if err := cs.CoreV1().Secrets(ns).Delete(ctx, secretName, metav1.DeleteOptions{}); err != nil {
		h.Logger.Error("failed to delete secret", "namespace", ns, "name", secretName, "error", err)
		h.auditLog(r, user, audit.ActionCertReissue, "Certificate", ns, name, audit.ResultFailure)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to delete backing secret", "")
		return
	}

	h.auditLog(r, user, audit.ActionCertReissue, "Certificate", ns, name, audit.ResultSuccess)
	h.InvalidateCache()

	w.WriteHeader(http.StatusAccepted)
	httputil.WriteData(w, map[string]string{"status": "reissuing"})
}
