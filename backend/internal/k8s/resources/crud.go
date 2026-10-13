package resources

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	// remoteListTimeout bounds one whole paged list on a remote cluster,
	// including client resolution. Up to k8s.RemoteListMaxPages round trips share
	// it, so a slow or unreachable API server fails the request in bounded
	// time instead of holding it open.
	remoteListTimeout = 10 * time.Second
	// remoteGetTimeout bounds a single-object read on a remote cluster,
	// including client resolution.
	remoteGetTimeout = 10 * time.Second
)

// ---------------------------------------------------------------------------
// Generic CRUD handlers — dispatch to the adapter looked up from {kind}.
// ---------------------------------------------------------------------------

// HandleListResource handles GET /api/v1/resources/{kind}[/{namespace}]
//
// The local cluster is answered from the informer cache. A remote cluster has
// no informers, so its list is read directly from that cluster's API server,
// as the user, through ClusterRouter; a failure there is reported and never
// answered from the local cluster.
func (h *Handler) HandleListResource(w http.ResponseWriter, r *http.Request) {
	adapter, ok := resolveAdapter(w, r)
	if !ok {
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	params := parseListParams(r)
	ns := params.Namespace
	if adapter.ClusterScoped() {
		ns = ""
	}

	if !h.checkAccess(w, r, user, "list", adapter.APIResource(), ns) {
		return
	}

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	// An adapter whose list reads query parameters of its own serves the
	// whole request, on either cluster.
	if rl, ok := adapter.(requestLister); ok {
		rl.listForRequest(h, w, r, user, ns, sel, params)
		return
	}

	if clusterID := middleware.ClusterIDFromContext(r.Context()); !k8s.IsLocalClusterID(clusterID) {
		items, truncated, ok := h.listRemote(w, r, user, clusterID, adapter, adapter.DisplayName(), ns,
			metav1.ListOptions{LabelSelector: sel.String()})
		if !ok {
			return
		}
		page, token := paginateAny(items, params.Limit, params.Continue)
		writeListPage(w, page, len(items), token, truncated)
		return
	}

	items, err := adapter.ListFromCache(h.Informers, ns, sel)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list "+adapter.DisplayName(), err.Error())
		return
	}

	page, token := paginateAny(items, params.Limit, params.Continue)
	writeList(w, page, len(items), token)
}

// HandleGetResource handles GET /api/v1/resources/{kind}/{namespace}/{name}
// For cluster-scoped resources: GET /api/v1/resources/{kind}/{name}
//
// Like the list, the local cluster reads the informer cache and a remote
// cluster is read directly, as the user, with no local fallback.
func (h *Handler) HandleGetResource(w http.ResponseWriter, r *http.Request) {
	adapter, ok := resolveAdapter(w, r)
	if !ok {
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	ns, name := extractNsName(r, adapter)

	if !h.checkAccess(w, r, user, "get", adapter.APIResource(), ns) {
		return
	}

	if clusterID := middleware.ClusterIDFromContext(r.Context()); !k8s.IsLocalClusterID(clusterID) {
		h.getRemote(w, r, user, clusterID, adapter, ns, name)
		return
	}

	item, err := adapter.GetFromCache(h.Informers, ns, name)
	if err != nil {
		mapK8sError(w, err, "get", adapter.DisplayName(), ns, name)
		return
	}

	writeData(w, item)
}

// HandleCreateResource handles POST /api/v1/resources/{kind}[/{namespace}]
func (h *Handler) HandleCreateResource(w http.ResponseWriter, r *http.Request) {
	adapter, ok := resolveAdapter(w, r)
	if !ok {
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	if adapter.ClusterScoped() {
		ns = ""
	}

	if !h.checkAccess(w, r, user, "create", adapter.APIResource(), ns) {
		return
	}

	body, err := readBody(w, r)
	if err != nil {
		return
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	result, err := adapter.Create(r.Context(), cs, ns, body)
	if err != nil {
		if errors.Is(err, errReadOnly) {
			writeError(w, http.StatusMethodNotAllowed, adapter.DisplayName()+" is read-only", "")
			return
		}
		h.auditWrite(r, user, audit.ActionCreate, adapter.DisplayName(), ns, "", audit.ResultFailure)
		mapK8sError(w, err, "create", adapter.DisplayName(), ns, "")
		return
	}

	h.auditWrite(r, user, audit.ActionCreate, adapter.DisplayName(), ns, "", audit.ResultSuccess)
	writeCreated(w, result)
}

// HandleUpdateResource handles PUT /api/v1/resources/{kind}/{namespace}/{name}
// For cluster-scoped resources: PUT /api/v1/resources/{kind}/{name}
func (h *Handler) HandleUpdateResource(w http.ResponseWriter, r *http.Request) {
	adapter, ok := resolveAdapter(w, r)
	if !ok {
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	ns, name := extractNsName(r, adapter)

	if !h.checkAccess(w, r, user, "update", adapter.APIResource(), ns) {
		return
	}

	body, err := readBody(w, r)
	if err != nil {
		return
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	result, err := adapter.Update(r.Context(), cs, ns, name, body)
	if err != nil {
		if errors.Is(err, errReadOnly) {
			writeError(w, http.StatusMethodNotAllowed, adapter.DisplayName()+" is read-only", "")
			return
		}
		h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultFailure)
		mapK8sError(w, err, "update", adapter.DisplayName(), ns, name)
		return
	}

	h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultSuccess)
	writeData(w, result)
}

// HandleDeleteResource handles DELETE /api/v1/resources/{kind}/{namespace}/{name}
// For cluster-scoped resources: DELETE /api/v1/resources/{kind}/{name}
func (h *Handler) HandleDeleteResource(w http.ResponseWriter, r *http.Request) {
	adapter, ok := resolveAdapter(w, r)
	if !ok {
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	ns, name := extractNsName(r, adapter)

	if !h.checkAccess(w, r, user, "delete", adapter.APIResource(), ns) {
		return
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	err = adapter.Delete(r.Context(), cs, ns, name)
	if err != nil {
		if errors.Is(err, errReadOnly) {
			writeError(w, http.StatusMethodNotAllowed, adapter.DisplayName()+" is read-only", "")
			return
		}
		h.auditWrite(r, user, audit.ActionDelete, adapter.DisplayName(), ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", adapter.DisplayName(), ns, name)
		return
	}

	h.auditWrite(r, user, audit.ActionDelete, adapter.DisplayName(), ns, name, audit.ResultSuccess)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Remote reads: a cluster with no informers, read directly as the user
// ---------------------------------------------------------------------------

// listRemote pages through adapter's list on a remote cluster, as the user,
// under remoteListTimeout. base carries the caller's selectors; noun names the
// resource in error messages. truncated reports that the list still had more
// pages after k8s.RemoteListMaxPages. On failure it writes the error response and
// returns ok=false; it never falls back to the local cluster, and the raw
// error reaches only the log.
func (h *Handler) listRemote(
	w http.ResponseWriter, r *http.Request, user *auth.User,
	clusterID string, adapter ResourceAdapter, noun, ns string, base metav1.ListOptions,
) (items []any, truncated, ok bool) {
	ctx, cancel := context.WithTimeout(r.Context(), remoteListTimeout)
	defer cancel()

	cs, err := h.remoteClientFor(ctx, clusterID, user)
	if err != nil {
		h.Logger.Error("remote list: resolve cluster client",
			"cluster", clusterID, "kind", adapter.Kind(), "namespace", ns, "error", err)
		writeError(w, http.StatusBadGateway, "failed to reach the selected cluster", "")
		return nil, false, false
	}

	items, truncated, err = k8s.PageList(ctx, base, func(ctx context.Context, opts metav1.ListOptions) ([]any, string, error) {
		return adapter.ListDirect(ctx, cs, ns, opts)
	})
	if err != nil {
		// Items read before a failure are discarded: a refusal at a later
		// page means they may no longer be the user's to see, and a partial
		// answer would be served as if it were complete.
		switch {
		case apierrors.IsForbidden(err):
			writeError(w, http.StatusForbidden,
				"you do not have permission to list "+noun+inNamespace(ns)+" on the selected cluster", "")
		case errors.Is(err, errSecretsNotCached):
			// The generic route never serves Secrets on any cluster; answer
			// exactly as the local cluster does.
			writeError(w, http.StatusInternalServerError, "failed to list "+noun, "")
		default:
			h.Logger.Error("remote list",
				"cluster", clusterID, "kind", adapter.Kind(), "namespace", ns, "error", err)
			writeError(w, http.StatusBadGateway, "failed to list "+noun+" on the selected cluster", "")
		}
		return nil, false, false
	}
	if truncated {
		h.Logger.Warn("remote list: truncated",
			"cluster", clusterID, "kind", adapter.Kind(), "namespace", ns, "items", len(items))
	}
	return items, truncated, true
}

// getRemote reads one object of adapter's kind on a remote cluster, as the
// user, under remoteGetTimeout, and writes the response. Not found and
// forbidden map to fixed 404 and 403 messages; every other failure, a remote
// status or a failure to reach the cluster, is a 502. The remote API server's
// own text reaches only the log, never the body, matching listRemote. It
// never falls back to the local cluster.
func (h *Handler) getRemote(
	w http.ResponseWriter, r *http.Request, user *auth.User,
	clusterID string, adapter ResourceAdapter, ns, name string,
) {
	ctx, cancel := context.WithTimeout(r.Context(), remoteGetTimeout)
	defer cancel()

	cs, err := h.remoteClientFor(ctx, clusterID, user)
	if err != nil {
		h.Logger.Error("remote get: resolve cluster client",
			"cluster", clusterID, "kind", adapter.Kind(), "namespace", ns, "error", err)
		writeError(w, http.StatusBadGateway, "failed to reach the selected cluster", "")
		return
	}

	item, err := adapter.GetDirect(ctx, cs, ns, name)
	if err != nil {
		object := adapter.DisplayName() + " '" + name + "'"
		where := inNamespace(ns) + " on the selected cluster"
		switch {
		case errors.Is(err, errSecretsNotCached):
			// The generic route never serves Secrets on any cluster; answer
			// exactly as the local cluster does.
			mapK8sError(w, err, "get", adapter.DisplayName(), ns, name)
		case apierrors.IsNotFound(err):
			writeError(w, http.StatusNotFound, object+" not found"+where, "")
		case apierrors.IsForbidden(err):
			writeError(w, http.StatusForbidden, "you do not have permission to get "+object+where, "")
		default:
			// A remote cluster's own 401 (its stored credential rejected) must
			// not surface as a k8sCenter 401: the web client reads any 401 as
			// its own session expiring.
			h.Logger.Error("remote get",
				"cluster", clusterID, "kind", adapter.Kind(), "namespace", ns, "error", err)
			writeError(w, http.StatusBadGateway, "failed to get "+adapter.DisplayName()+" on the selected cluster", "")
		}
		return
	}

	writeData(w, item)
}

// inNamespace renders the " in namespace <ns>" clause of an error message,
// or nothing for a cluster-scoped or all-namespaces request.
func inNamespace(ns string) string {
	if ns == "" {
		return ""
	}
	return " in namespace " + ns
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// resolveAdapter extracts the {kind} URL param and looks up the adapter.
// Returns false and writes a 404 if no adapter is registered for the kind.
func resolveAdapter(w http.ResponseWriter, r *http.Request) (ResourceAdapter, bool) {
	kind := chi.URLParam(r, "kind")
	adapter := GetAdapter(kind)
	if adapter == nil {
		writeError(w, http.StatusNotFound, "unknown resource kind: "+kind, "")
		return nil, false
	}
	return adapter, true
}

// extractNsName extracts namespace and name from URL params.
// WARNING: For cluster-scoped resources, chi's {namespace} URL param is
// reinterpreted as the resource name because cluster-scoped GET/DELETE
// routes use the same 2-segment pattern /resources/{kind}/{name} which
// chi maps to the {namespace} param. This is a deliberate semantic
// mismatch to avoid registering separate route handlers.
func extractNsName(r *http.Request, adapter ResourceAdapter) (ns, name string) {
	if adapter.ClusterScoped() {
		// Route: /resources/{kind}/{name} — chi maps first segment to "namespace"
		name = chi.URLParam(r, "namespace")
		return "", name
	}
	return chi.URLParam(r, "namespace"), chi.URLParam(r, "name")
}

// readBody reads and returns the request body, limited to maxRequestBodySize.
// Writes a 400 error on failure and returns a non-nil error.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body", err.Error())
		return nil, err
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "request body is empty", "")
		return nil, fmt.Errorf("empty body")
	}
	return body, nil
}

// paginateAny implements offset-based pagination for []any slices.
// Items are sorted by namespace/name for deterministic ordering.
// Returns the page of items and the next continue token (empty if no more items).
func paginateAny(items []any, limit int, continueToken string) ([]any, string) {
	sort.Slice(items, func(i, j int) bool {
		return objectKey(items[i]) < objectKey(items[j])
	})

	// The token is an offset this function minted; anything that is not a
	// positive integer (a negative or garbled token) starts at the first page.
	start := 0
	if n, err := strconv.Atoi(continueToken); err == nil && n > 0 {
		start = n
	}

	if start >= len(items) {
		return []any{}, ""
	}

	end := start + limit
	if end > len(items) {
		end = len(items)
	}

	var nextToken string
	if end < len(items) {
		nextToken = fmt.Sprintf("%d", end)
	}

	return items[start:end], nextToken
}

// ---------------------------------------------------------------------------
// Shared helpers — label selectors, typed pagination, object sort keys
// ---------------------------------------------------------------------------

// parseSelector converts a label selector string to a labels.Selector.
// An empty string returns labels.Everything().
func parseSelector(s string) (labels.Selector, error) {
	if s == "" {
		return labels.Everything(), nil
	}
	return labels.Parse(s)
}

// parseSelectorOrReject parses the label selector and writes a 400 error if invalid.
// Returns the selector and true if valid, or zero value and false if an error was written.
func parseSelectorOrReject(w http.ResponseWriter, s string) (labels.Selector, bool) {
	sel, err := parseSelector(s)
	if err != nil {
		writeError(w, http.StatusBadRequest,
			"invalid label selector: "+s,
			err.Error(),
		)
		return nil, false
	}
	return sel, true
}

// paginate implements simple offset-based pagination using a continue token
// that represents the starting index. Items are sorted by namespace+name for
// deterministic ordering across requests. Returns the page of items and the
// next continue token (empty if no more items).
func paginate[T any](items []*T, limit int, continueToken string) ([]*T, string) {
	sort.Slice(items, func(i, j int) bool {
		a, b := objectKey(items[i]), objectKey(items[j])
		return a < b
	})

	start := 0
	if continueToken != "" {
		fmt.Sscanf(continueToken, "%d", &start)
	}

	if start >= len(items) {
		return []*T{}, ""
	}

	end := start + limit
	if end > len(items) {
		end = len(items)
	}

	var nextToken string
	if end < len(items) {
		nextToken = fmt.Sprintf("%d", end)
	}

	return items[start:end], nextToken
}

// objectKey returns "namespace/name" for a Kubernetes object to use as a sort key.
func objectKey(obj any) string {
	if acc, ok := obj.(metav1.ObjectMetaAccessor); ok {
		m := acc.GetObjectMeta()
		if m.GetNamespace() != "" {
			return m.GetNamespace() + "/" + m.GetName()
		}
		return m.GetName()
	}
	return ""
}
