package externalsecrets

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/singleflight"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"

	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// evidenceResources is the kind allowlist for the evidence endpoints: the
// five ESO plurals, matched exactly, taken from the package GVRs so the set
// of kinds has one source. Only the plural is used — the version and
// namespaced flag come from live discovery, never from those v1-pinned vars.
var evidenceResources = func() map[string]struct{} {
	m := map[string]struct{}{}
	for _, gvr := range []schema.GroupVersionResource{
		ExternalSecretGVR, ClusterExternalSecretGVR, SecretStoreGVR, ClusterSecretStoreGVR, PushSecretGVR,
	} {
		m[gvr.Resource] = struct{}{}
	}
	return m
}()

// Bounds on what the events endpoint reads and returns.
const (
	evidenceEventsListLimit   = 200
	evidenceEventsMaxPages    = 5
	evidenceMessageMaxBytes   = 1024
	evidenceTokenMaxBytes     = 256
	evidenceClusterNamespace  = "_"
	evidenceDiscoveryCacheTTL = staleDuration
	// evidenceDiscoveryWalkTimeout bounds one shared discovery walk, which
	// runs detached from the request that started it.
	evidenceDiscoveryWalkTimeout = 30 * time.Second
)

// evidenceOutcomeOnlyDroppedFields names the event keys an outcome-only
// response leaves out, as evidenceEventDTO JSON keys verbatim.
var evidenceOutcomeOnlyDroppedFields = []string{"message", "messageTruncated"}

var (
	errEvidenceDiscovery = errors.New("eso discovery unavailable")
	errEvidenceNotServed = errors.New("eso kind not served by this cluster")
)

// evidenceResource is one ESO kind as the cluster serves it.
type evidenceResource struct {
	gvr        schema.GroupVersionResource
	namespaced bool
}

// evidenceGVRCache holds the last successful discovery walk of the ESO
// group. The zero value is ready to use. now is a test seam; nil means
// time.Now. group coalesces concurrent refreshes into one walkESOGroup call
// (see resolveESOGVR).
type evidenceGVRCache struct {
	mu         sync.RWMutex
	at         time.Time
	byResource map[string]evidenceResource
	now        func() time.Time
	group      singleflight.Group
}

func (c *evidenceGVRCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

type evidenceEventDTO struct {
	Type             string     `json:"type"`
	Reason           string     `json:"reason"`
	Message          *string    `json:"message,omitempty"`
	MessageTruncated *bool      `json:"messageTruncated,omitempty"`
	Count            int32      `json:"count"`
	FirstTimestamp   *time.Time `json:"firstTimestamp,omitempty"`
	LastTimestamp    *time.Time `json:"lastTimestamp,omitempty"`
	Source           string     `json:"source"`
}

type evidenceEventsResponse struct {
	UID        string             `json:"uid"`
	Projection historyProjection  `json:"projection"`
	Events     []evidenceEventDTO `json:"events"`
	Truncated  bool               `json:"truncated"`
}

// HandleGetEvidenceEvents serves the Kubernetes events recorded about one
// live ESO object, newest first.
//
//	GET /externalsecrets/evidence/{kind}/{namespace}/{name}/events
//
// {kind} is one of the five ESO plurals; {namespace} is "_" for the two
// cluster-scoped kinds, exactly as /yaml/export addresses them. Gate order:
//
//  1. 401 without a user.
//  2. 501 remote_events_unsupported for a non-local cluster: this handler's
//     clients reach only the local cluster.
//  3. 503 eso_not_detected.
//  4. 400 on an invalid name or namespace, 400 unknown_evidence_kind outside
//     the allowlist.
//  5. The kind's version and scope come from live discovery (503
//     discovery_unavailable, 404 evidence_kind_not_served), never from the
//     v1-pinned package GVRs; the homelab serves PushSecret at v1alpha1
//     only. 400 invalid_scope when "_" does not match the kind's scope.
//  6. 403 without `get <kind>` (cluster-wide for a cluster-scoped kind, so a
//     namespaced grant cannot reach it).
//  7. The object is read live through the impersonating client (403/404
//     mapped) and its UID taken from that read, never from the request.
//  8. Projection: messages need `get secrets` in the object's namespace
//     (cluster-wide for a cluster-scoped kind); without it they are omitted.
//  9. Events are listed with an involvedObject.uid field selector, so a
//     deleted-and-recreated object never inherits its predecessor's events,
//     paging up to evidenceEventsMaxPages pages of evidenceEventsListLimit
//     each and filtering on the UID again in-process. The accumulated pages
//     are sorted newest first and cut to evidenceEventsListLimit; truncated
//     is true when a Continue token remains after the page cap, or when the
//     top-N cut dropped events. A forbidden list on any page is 403
//     events_forbidden naming the grant — never an empty 200.
//
// The generic /resources/events path cannot be reused: it ignores the
// involved-object parameters and returns every event in the namespace.
func (h *Handler) HandleGetEvidenceEvents(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	if reqCluster := middleware.ClusterIDFromContext(ctx); !k8s.IsLocalClusterID(reqCluster) && reqCluster != h.historyClusterID() {
		httputil.WriteErrorWithReason(w, http.StatusNotImplemented,
			"ESO evidence is available for the local cluster only",
			"remote_events_unsupported", nil)
		return
	}

	if !h.Discoverer.IsAvailable(ctx) {
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, "ESO not detected", "eso_not_detected", nil)
		return
	}

	// Validated inline rather than with resources.ValidateURLParams, which
	// rejects the "_" cluster-scope namespace (the yaml.HandleExport rule).
	kind := chi.URLParam(r, "kind")
	urlNS := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	// ValidateK8sName accepts "", so emptiness is checked separately.
	if name == "" || !resources.ValidateK8sName(name) {
		httputil.WriteError(w, http.StatusBadRequest, "invalid resource name", "")
		return
	}
	if urlNS != evidenceClusterNamespace && (urlNS == "" || !resources.ValidateK8sName(urlNS)) {
		httputil.WriteError(w, http.StatusBadRequest, "invalid namespace", "")
		return
	}
	if _, ok := evidenceResources[kind]; !ok {
		httputil.WriteErrorWithReason(w, http.StatusBadRequest,
			"kind is not an External Secrets Operator resource", "unknown_evidence_kind", nil)
		return
	}

	res, err := h.resolveESOGVR(ctx, kind)
	switch {
	case errors.Is(err, errEvidenceNotServed):
		httputil.WriteErrorWithReason(w, http.StatusNotFound,
			"this cluster does not serve "+kind, "evidence_kind_not_served", nil)
		return
	case err != nil:
		h.Logger.Error("resolve eso resource for evidence", "kind", kind, "error", err)
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable,
			"ESO API discovery is temporarily unavailable", "discovery_unavailable", nil)
		return
	}
	if res.namespaced == (urlNS == evidenceClusterNamespace) {
		httputil.WriteErrorWithReason(w, http.StatusBadRequest,
			scopeMismatchMessage(kind, res.namespaced), "invalid_scope", nil)
		return
	}
	ns := urlNS
	if !res.namespaced {
		ns = ""
	}

	if !h.canAccess(ctx, user, "get", kind, ns) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, err := h.dynForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("create impersonating dynamic client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	obj, err := dynClient.Resource(res.gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		switch {
		case apierrors.IsForbidden(err):
			httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		case apierrors.IsNotFound(err):
			httputil.WriteError(w, http.StatusNotFound, "resource not found", "")
		default:
			h.Logger.Error("get eso object for evidence", "kind", kind, "namespace", ns, "name", name, "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch resource", "")
		}
		return
	}
	uid := string(obj.GetUID())
	if uid == "" {
		// A field selector on "" would match nothing and answer "no events",
		// which is a lie about an object that exists.
		h.Logger.Error("eso object has no uid", "kind", kind, "namespace", ns, "name", name)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch resource", "")
		return
	}

	level := projectionOutcomeOnly
	if h.canAccessGroup(ctx, user, "get", "", "secrets", ns) {
		level = projectionFull
	}

	kube, err := h.clientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	// The API server returns items in key (name) order, which for
	// client-go-named events (<obj>.<hex UnixNano>) is oldest first — a
	// single page would silently hand back the OLDEST evidenceEventsListLimit
	// events for an object with more than that many. Page up to
	// evidenceEventsMaxPages, accumulate, then sort and cut to newest.
	var items []corev1.Event
	continueToken := ""
	for page := 0; page < evidenceEventsMaxPages; page++ {
		list, err := kube.CoreV1().Events(ns).List(ctx, metav1.ListOptions{
			FieldSelector: fields.OneTermEqualSelector("involvedObject.uid", uid).String(),
			Limit:         evidenceEventsListLimit,
			Continue:      continueToken,
		})
		if err != nil {
			if apierrors.IsForbidden(err) {
				httputil.WriteErrorWithReason(w, http.StatusForbidden,
					"access denied: this object's events require "+eventsGrant(ns),
					"events_forbidden", map[string]any{"requiredGrant": eventsGrant(ns)})
				return
			}
			h.Logger.Error("list eso object events", "kind", kind, "namespace", ns, "name", name, "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, "failed to list events", "")
			return
		}
		items = append(items, list.Items...)
		continueToken = list.Continue
		if continueToken == "" {
			break
		}
	}
	pagesRemain := continueToken != ""

	// The field selector already filtered server-side; this repeats it so the
	// UID boundary does not rest on one API-server feature alone.
	items = slices.DeleteFunc(items, func(ev corev1.Event) bool { return string(ev.InvolvedObject.UID) != uid })
	slices.SortStableFunc(items, compareEventsNewestFirst)

	truncated := pagesRemain
	if len(items) > evidenceEventsListLimit {
		truncated = true
		items = items[:evidenceEventsListLimit]
	}

	resp := evidenceEventsResponse{
		UID:        uid,
		Projection: historyProjection{Level: level, DroppedFields: []string{}},
		Events:     make([]evidenceEventDTO, 0, len(items)),
		Truncated:  truncated,
	}
	if level == projectionOutcomeOnly {
		resp.Projection.DroppedFields = evidenceOutcomeOnlyDroppedFields
	}
	for i := range items {
		resp.Events = append(resp.Events, projectEvent(&items[i], level))
	}

	httputil.WriteJSON(w, http.StatusOK, api.Response{
		Data:     resp,
		Metadata: &api.Metadata{Total: len(resp.Events)},
	})
}

// resolveESOGVR returns the version and scope at which this cluster serves
// an ESO resource. Within the group, the preferred version wins when it
// serves the resource; otherwise the first other version that does. A
// resource can be absent from the preferred version entirely — ESO v2
// serves PushSecret only at v1alpha1 while preferring v1.
//
// It duplicates the shape of yaml.resolveGVR (R-7) rather than exporting it,
// to keep this package off internal/yaml. The walk is cached for
// evidenceDiscoveryCacheTTL, mirroring the Discoverer's own TTL; a failed
// walk is not cached.
//
// A fresh cache is read under an RLock and returned without touching the
// network. On a miss or a stale entry the walk runs through the cache's
// singleflight.Group so concurrent refreshes coalesce into one walkESOGroup
// call — no lock is held across that I/O, mirroring Discoverer.Probe.
//
// The shared walk runs on a context detached from any one caller and bounded
// by evidenceDiscoveryWalkTimeout, so a client that disconnects does not fail
// the others coalesced onto its walk; each caller still stops waiting when
// its own context ends. DoChan runs the walk on its own goroutine, outside
// chi's recovery, hence recoverutil.Safe.
func (h *Handler) resolveESOGVR(ctx context.Context, resource string) (evidenceResource, error) {
	c := &h.evidenceGVRs

	c.mu.RLock()
	if c.byResource != nil && c.clock().Sub(c.at) < evidenceDiscoveryCacheTTL {
		res, ok := c.byResource[resource]
		c.mu.RUnlock()
		if !ok {
			return evidenceResource{}, errEvidenceNotServed
		}
		return res, nil
	}
	c.mu.RUnlock()

	walk := c.group.DoChan("eso-gvr", func() (any, error) {
		walkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), evidenceDiscoveryWalkTimeout)
		defer cancel()
		var byResource map[string]evidenceResource
		err := fmt.Errorf("%w: discovery walk panicked", errEvidenceDiscovery)
		recoverutil.Safe(h.Logger, "eso evidence discovery walk", func() {
			byResource, err = walkESOGroup(walkCtx, discovery.ToDiscoveryInterfaceWithContext(h.Discoverer.discovery()))
		})
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.byResource, c.at = byResource, c.clock()
		c.mu.Unlock()
		return byResource, nil
	})
	var result singleflight.Result
	select {
	case <-ctx.Done():
		return evidenceResource{}, fmt.Errorf("%w: %w", errEvidenceDiscovery, ctx.Err())
	case result = <-walk:
	}
	if result.Err != nil {
		return evidenceResource{}, result.Err
	}
	res, ok := result.Val.(map[string]evidenceResource)[resource]
	if !ok {
		return evidenceResource{}, errEvidenceNotServed
	}
	return res, nil
}

func walkESOGroup(ctx context.Context, disco discovery.DiscoveryInterfaceWithContext) (map[string]evidenceResource, error) {
	groups, err := disco.ServerGroupsWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: server groups: %w", errEvidenceDiscovery, err)
	}
	var group *metav1.APIGroup
	for i := range groups.Groups {
		if groups.Groups[i].Name == GroupName {
			group = &groups.Groups[i]
			break
		}
	}
	byResource := map[string]evidenceResource{}
	if group == nil {
		return byResource, nil
	}

	versions := []string{group.PreferredVersion.Version}
	for _, v := range group.Versions {
		if v.Version != group.PreferredVersion.Version {
			versions = append(versions, v.Version)
		}
	}
	for _, version := range versions {
		list, err := disco.ServerResourcesForGroupVersionWithContext(ctx, GroupName+"/"+version)
		if err != nil {
			// A version that fails to list could be the only one serving a
			// kind; answering "not served" from a partial walk would be wrong.
			return nil, fmt.Errorf("%w: %s/%s: %w", errEvidenceDiscovery, GroupName, version, err)
		}
		for _, r := range list.APIResources {
			if _, allowed := evidenceResources[r.Name]; !allowed {
				continue // subresources such as externalsecrets/status, and non-ESO kinds
			}
			if _, seen := byResource[r.Name]; seen {
				continue
			}
			byResource[r.Name] = evidenceResource{
				gvr:        schema.GroupVersionResource{Group: GroupName, Version: version, Resource: r.Name},
				namespaced: r.Namespaced,
			}
		}
	}
	return byResource, nil
}

func scopeMismatchMessage(kind string, namespaced bool) string {
	if namespaced {
		return kind + " is namespaced; address it by namespace, not " + evidenceClusterNamespace
	}
	return kind + " is cluster-scoped; address it with " + evidenceClusterNamespace + " as the namespace"
}

// eventsGrant names the RBAC grant listing an object's events needs.
func eventsGrant(ns string) string {
	if ns == "" {
		return "cluster-wide `list events`"
	}
	return "`list events` in namespace " + ns
}

// projectEvent renders one event at the given level. Every controller-written
// string is sanitized; the message is additionally withheld below full.
func projectEvent(ev *corev1.Event, lvl projectionLevel) evidenceEventDTO {
	typ, _ := sanitizeControllerText(ev.Type, evidenceTokenMaxBytes)
	reason, _ := sanitizeControllerText(ev.Reason, evidenceTokenMaxBytes)
	source, _ := sanitizeControllerText(cmp.Or(ev.Source.Component, ev.ReportingController), evidenceTokenMaxBytes)
	dto := evidenceEventDTO{
		Type:           typ,
		Reason:         reason,
		Count:          eventCount(ev),
		FirstTimestamp: optionalTime(ev.FirstTimestamp.Time, ev.EventTime.Time),
		LastTimestamp:  optionalTime(eventLastSeen(ev), time.Time{}),
		Source:         source,
	}
	if lvl == projectionFull {
		msg, truncated := sanitizeControllerText(ev.Message, evidenceMessageMaxBytes)
		dto.Message = &msg
		dto.MessageTruncated = &truncated
	}
	return dto
}

// eventLastSeen is when the event last occurred, across the core/v1 and
// events.k8s.io shapes: lastTimestamp, else the series' last observation,
// else the single occurrence time.
func eventLastSeen(ev *corev1.Event) time.Time {
	switch {
	case !ev.LastTimestamp.IsZero():
		return ev.LastTimestamp.Time
	case ev.Series != nil && !ev.Series.LastObservedTime.IsZero():
		return ev.Series.LastObservedTime.Time
	default:
		return ev.EventTime.Time
	}
}

func eventCount(ev *corev1.Event) int32 {
	switch {
	case ev.Count > 0:
		return ev.Count
	case ev.Series != nil && ev.Series.Count > 0:
		return ev.Series.Count
	default:
		return 1
	}
}

// optionalTime returns the first non-zero time, in UTC, or nil when both are
// zero, so an unknown timestamp is absent rather than 0001-01-01.
func optionalTime(primary, fallback time.Time) *time.Time {
	t := primary
	if t.IsZero() {
		t = fallback
	}
	if t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}

// compareEventsNewestFirst orders by last occurrence, newest first; events
// with no time sort last, and the name breaks ties for a stable order.
func compareEventsNewestFirst(a, b corev1.Event) int {
	if c := eventLastSeen(&b).Compare(eventLastSeen(&a)); c != 0 {
		return c
	}
	return strings.Compare(a.Name, b.Name)
}
