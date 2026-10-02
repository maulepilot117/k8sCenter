package resources

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/pkg/api"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/validation/path"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
)

// Query parameters that narrow an events list to the events about one object.
// The resource detail page's Events tab sends both.
const (
	queryInvolvedObjectKind = "involvedObjectKind"
	queryInvolvedObjectName = "involvedObjectName"
)

// maxInvolvedObjectNameLen is the longest object name Kubernetes accepts
// (a DNS subdomain, the widest name format any built-in kind uses).
const maxInvolvedObjectNameLen = 253

// remoteEventsListTimeout bounds the whole paged events list on a remote
// cluster, including client resolution.
const remoteEventsListTimeout = 10 * time.Second

// involvedObjectKindRegexp matches a Kubernetes Kind: an identifier of ASCII
// letters and digits, starting with a letter (Pod, HorizontalPodAutoscaler).
// 63 characters is the DNS-label bound CRD kinds are held to.
var involvedObjectKindRegexp = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,62}$`)

// errInvalidInvolvedObject marks a filter parameter that is not a valid
// Kind or object name. The handler turns it into a 400.
var errInvalidInvolvedObject = errors.New("invalid involved object filter")

// involvedObjectFilter narrows events to those whose involvedObject has the
// given kind and name. An empty field does not constrain; the zero value
// matches every event.
type involvedObjectFilter struct {
	kind string
	name string
}

// parseInvolvedObjectFilter reads and validates the filter parameters. An
// absent or empty parameter leaves that field unconstrained, so a request
// without them lists exactly what it listed before the filter existed.
//
// Validation is defence in depth: values reach the remote API server through
// fields.OneTermEqualSelector, which escapes selector syntax, and the local
// path compares them as plain strings. Rejecting what can never be a real
// Kind or name still keeps junk out of logs and off remote API servers.
func parseInvolvedObjectFilter(q url.Values) (involvedObjectFilter, error) {
	f := involvedObjectFilter{
		kind: q.Get(queryInvolvedObjectKind),
		name: q.Get(queryInvolvedObjectName),
	}
	if f.kind != "" && !involvedObjectKindRegexp.MatchString(f.kind) {
		return involvedObjectFilter{}, errInvalidInvolvedObject
	}
	if f.name != "" && !validInvolvedObjectName(f.name) {
		return involvedObjectFilter{}, errInvalidInvolvedObject
	}
	return f, nil
}

// validInvolvedObjectName accepts any name some Kubernetes kind can carry.
// Names are not all DNS subdomains (RBAC names may hold ':' or ','), so the
// check is the API server's own universal rule, a valid path segment, plus a
// length bound and no control characters.
func validInvolvedObjectName(name string) bool {
	if len(name) > maxInvolvedObjectNameLen || !utf8.ValidString(name) {
		return false
	}
	if len(path.IsValidPathSegmentName(name)) > 0 {
		return false
	}
	return !strings.ContainsFunc(name, unicode.IsControl)
}

// active reports whether the filter constrains anything.
func (f involvedObjectFilter) active() bool {
	return f.kind != "" || f.name != ""
}

// matches reports whether e is about the filtered object.
func (f involvedObjectFilter) matches(e *corev1.Event) bool {
	if f.kind != "" && e.InvolvedObject.Kind != f.kind {
		return false
	}
	return f.name == "" || e.InvolvedObject.Name == f.name
}

// fieldSelector renders the filter as an API server field selector. Each
// value is escaped by fields.OneTermEqualSelector, so a ',' or '=' in a name
// stays part of that name and cannot add a term. The inactive filter renders
// as the empty selector.
func (f involvedObjectFilter) fieldSelector() string {
	var terms []fields.Selector
	if f.kind != "" {
		terms = append(terms, fields.OneTermEqualSelector("involvedObject.kind", f.kind))
	}
	if f.name != "" {
		terms = append(terms, fields.OneTermEqualSelector("involvedObject.name", f.name))
	}
	if len(terms) == 0 {
		return ""
	}
	return fields.AndSelectors(terms...).String()
}

// filterEvents keeps the events f matches. The inactive filter returns items
// untouched.
func (f involvedObjectFilter) filterEvents(items []any) []any {
	if !f.active() {
		return items
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		if e, ok := item.(*corev1.Event); ok && f.matches(e) {
			out = append(out, item)
		}
	}
	return out
}

// eventAdapter answers its list from the request (the involvedObject filter
// and the selected cluster), not from the informer cache alone. The
// assertion keeps a signature drift from silently sending events back to the
// cache-only path.
var _ requestLister = eventAdapter{}

func (eventAdapter) listForRequest(h *Handler, w http.ResponseWriter, r *http.Request, user *auth.User, ns string, sel labels.Selector, params ListParams) {
	h.handleListEvents(w, r, user, ns, sel, params)
}

// handleListEvents serves the events list once HandleListResource has
// authorized it. The local cluster reads the informer cache; a remote cluster
// has no informers, so its events are listed directly, as the user, through
// ClusterRouter. A remote list stopped at its page cap is still served, with
// metadata.truncated set so the client knows the set is incomplete.
func (h *Handler) handleListEvents(w http.ResponseWriter, r *http.Request, user *auth.User, ns string, sel labels.Selector, params ListParams) {
	filter, err := parseInvolvedObjectFilter(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest,
			"invalid "+queryInvolvedObjectKind+" or "+queryInvolvedObjectName,
			"the kind must be a Kubernetes Kind and the name a valid object name",
		)
		return
	}

	var (
		items     []any
		truncated bool
	)
	clusterID := middleware.ClusterIDFromContext(r.Context())
	if k8s.IsLocalClusterID(clusterID) {
		items, err = eventAdapter{}.ListFromCache(h.Informers, ns, sel)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list Event", err.Error())
			return
		}
	} else {
		var ok bool
		items, truncated, ok = h.listRemoteEvents(w, r, user, clusterID, ns, sel, filter)
		if !ok {
			return
		}
	}

	items = filter.filterEvents(items)
	page, token := paginateAny(items, params.Limit, params.Continue)
	writeJSON(w, http.StatusOK, api.Response{
		Data: page,
		Metadata: &api.Metadata{
			Total:     len(items),
			Continue:  token,
			Truncated: truncated,
		},
	})
}

// listRemoteEvents pages through the events on a remote cluster with the
// filter pushed down as a field selector. The result is filtered again by the
// caller, so an API server that ignored the selector still cannot widen the
// answer. truncated reports that the list still had more pages after
// remoteListMaxPages. On failure it writes the error response and returns
// ok=false; it never falls back to the local cluster.
func (h *Handler) listRemoteEvents(
	w http.ResponseWriter, r *http.Request, user *auth.User,
	clusterID, ns string, sel labels.Selector, filter involvedObjectFilter,
) (items []any, truncated, ok bool) {
	ctx, cancel := context.WithTimeout(r.Context(), remoteEventsListTimeout)
	defer cancel()

	cs, err := h.remoteClientFor(ctx, clusterID, user)
	if err != nil {
		h.Logger.Error("remote events: resolve cluster client", "cluster", clusterID, "error", err)
		writeError(w, http.StatusBadGateway, "failed to reach the selected cluster", "")
		return nil, false, false
	}

	base := metav1.ListOptions{
		LabelSelector: sel.String(),
		FieldSelector: filter.fieldSelector(),
	}
	items, truncated, err = pageRemoteList(ctx, base, func(ctx context.Context, opts metav1.ListOptions) ([]any, string, error) {
		list, err := cs.CoreV1().Events(ns).List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		page := make([]any, len(list.Items))
		for i := range list.Items {
			page[i] = &list.Items[i]
		}
		return page, list.Continue, nil
	})
	if err != nil {
		// Items read before a failure are discarded: a refusal at a later
		// page means they may no longer be the user's to see, and a partial
		// answer would be served as if it were complete.
		if apierrors.IsForbidden(err) {
			writeError(w, http.StatusForbidden,
				"you do not have permission to list events in namespace "+ns+" on the selected cluster", "")
			return nil, false, false
		}
		h.Logger.Error("remote events: list", "cluster", clusterID, "namespace", ns, "error", err)
		writeError(w, http.StatusBadGateway, "failed to list events on the selected cluster", "")
		return nil, false, false
	}
	if truncated {
		h.Logger.Warn("remote events: list truncated",
			"cluster", clusterID, "namespace", ns, "items", len(items))
	}
	return items, truncated, true
}
