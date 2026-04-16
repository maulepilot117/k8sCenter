package resources

import (
	"fmt"
	"io"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
)

// ---------------------------------------------------------------------------
// Generic CRUD handlers — dispatch to the adapter looked up from {kind}.
// ---------------------------------------------------------------------------

// HandleListResource handles GET /api/v1/resources/{kind}[/{namespace}]
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

	result, err := adapter.Create(cs, ns, body)
	if err != nil {
		if IsReadOnlyError(err) {
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

	result, err := adapter.Update(cs, ns, name, body)
	if err != nil {
		if IsReadOnlyError(err) {
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

	err = adapter.Delete(cs, ns, name)
	if err != nil {
		if IsReadOnlyError(err) {
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
// For cluster-scoped resources, the first path segment after kind is the name (no namespace).
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

	start := 0
	if continueToken != "" {
		fmt.Sscanf(continueToken, "%d", &start)
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
