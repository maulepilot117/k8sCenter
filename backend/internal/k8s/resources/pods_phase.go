package resources

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
)

// queryPhase narrows a pod list to the given phases. It may repeat
// (?phase=Running&phase=Succeeded); a pod matching any of them is kept. The
// pod list's status chips send it, so a chip filters the whole list and not
// only the page the browser has loaded.
const queryPhase = "phase"

// knownPodPhases are the phases a pod can report. A filter naming anything
// else is rejected rather than silently matching nothing.
var knownPodPhases = map[corev1.PodPhase]bool{
	corev1.PodPending:   true,
	corev1.PodRunning:   true,
	corev1.PodSucceeded: true,
	corev1.PodFailed:    true,
	corev1.PodUnknown:   true,
}

// errInvalidPodPhase marks a phase parameter that is not a pod phase. The
// handler turns it into a 400.
var errInvalidPodPhase = errors.New("invalid pod phase filter")

// podPhaseFilter keeps pods whose phase is in the set. The empty set matches
// every pod, so a request without the parameter lists exactly what it did
// before the filter existed.
type podPhaseFilter map[corev1.PodPhase]bool

// parsePodPhaseFilter reads and validates the phase parameters. Empty values
// are skipped; any other value must be a pod phase, spelled as the API spells
// it (Failed, not failed).
func parsePodPhaseFilter(q url.Values) (podPhaseFilter, error) {
	f := podPhaseFilter{}
	for _, v := range q[queryPhase] {
		if v == "" {
			continue
		}
		p := corev1.PodPhase(v)
		if !knownPodPhases[p] {
			return nil, errInvalidPodPhase
		}
		f[p] = true
	}
	return f, nil
}

// active reports whether the filter constrains anything.
func (f podPhaseFilter) active() bool { return len(f) > 0 }

// fieldSelector renders the filter as an API server field selector. Field
// selectors have no OR, so only a single phase is pushed down; several
// phases list unfiltered and rely on filterPods. The value is escaped by
// fields.OneTermEqualSelector, though validation already limits it to a
// known phase.
func (f podPhaseFilter) fieldSelector() string {
	if len(f) != 1 {
		return ""
	}
	for p := range f {
		return fields.OneTermEqualSelector("status.phase", string(p)).String()
	}
	return ""
}

// filterPods keeps the pods f matches. The inactive filter returns items
// untouched.
func (f podPhaseFilter) filterPods(items []any) []any {
	if !f.active() {
		return items
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		if p, ok := item.(*corev1.Pod); ok && f[p.Status.Phase] {
			out = append(out, item)
		}
	}
	return out
}

// podAdapter answers its list from the request (the phase filter and the
// selected cluster). The assertion keeps a signature drift from silently
// sending pods back to the unfiltered generic path.
var _ requestLister = podAdapter{}

func (podAdapter) listForRequest(h *Handler, w http.ResponseWriter, r *http.Request, user *auth.User, ns string, sel labels.Selector, params ListParams) {
	h.handleListPods(w, r, user, ns, sel, params)
}

// handleListPods serves the pod list once HandleListResource has authorized
// it. The local cluster reads the informer cache; a remote cluster is listed
// directly, as the user, with a single phase pushed down as a field selector.
// Either way the result is filtered here before it is paginated, so total and
// the continue token describe the filtered set, and an API server that
// ignored the selector still cannot widen the answer.
func (h *Handler) handleListPods(w http.ResponseWriter, r *http.Request, user *auth.User, ns string, sel labels.Selector, params ListParams) {
	filter, err := parsePodPhaseFilter(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest,
			"invalid "+queryPhase,
			"phase must be one of Pending, Running, Succeeded, Failed or Unknown",
		)
		return
	}

	var (
		items     []any
		truncated bool
	)
	clusterID := middleware.ClusterIDFromContext(r.Context())
	if k8s.IsLocalClusterID(clusterID) {
		items, err = podAdapter{}.ListFromCache(h.Informers, ns, sel)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list Pod", err.Error())
			return
		}
	} else {
		var ok bool
		items, truncated, ok = h.listRemote(w, r, user, clusterID, podAdapter{}, podAdapter{}.DisplayName(), ns,
			metav1.ListOptions{LabelSelector: sel.String(), FieldSelector: filter.fieldSelector()})
		if !ok {
			return
		}
	}

	items = filter.filterPods(items)
	page, token := paginateAny(items, params.Limit, params.Continue)
	writeListPage(w, page, len(items), token, truncated)
}
