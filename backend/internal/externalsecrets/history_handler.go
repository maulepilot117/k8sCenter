package externalsecrets

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// ESOHistoryReader is the read half of store.ESOHistoryStore the history
// endpoint needs. Narrow on purpose (the BulkJobReadWriter pattern): tests
// substitute a fake, and the handler cannot reach Insert or Cleanup.
type ESOHistoryReader interface {
	QueryPage(ctx context.Context, clusterID, uid string, after *store.ESOHistoryCursor, limit int) (store.ESOHistoryPage, error)
}

// projectionLevel is how much of a history row a caller may see. It is
// resolved per request from the caller's own RBAC in the ExternalSecret's
// namespace; nothing in the request can raise it.
type projectionLevel string

const (
	// projectionOutcomeOnly: `get externalsecrets` allowed, `get secrets`
	// denied. The caller sees that attempts happened and how they ended,
	// never what the Secret contains or what the provider said.
	projectionOutcomeOnly projectionLevel = "outcome-only"
	// projectionFull: both allowed.
	projectionFull projectionLevel = "full"
)

// Byte bounds on controller text returned at the full projection.
const (
	historyMessageMaxBytes = 2048
	historyReasonMaxBytes  = 256
)

// outcomeOnlyDroppedFields names, for the client, the entry fields an
// outcome-only response leaves out, so it can say "hidden" rather than
// "empty". Presentation only — the omission itself is done by
// projectHistoryEntry leaving those DTO fields nil.
var outcomeOnlyDroppedFields = []string{"message", "diffKeys", "syncedResourceVersion"}

// knownESOReasons is the closed set of Ready-condition reasons an
// outcome-only caller may see verbatim. It is ESO's own condition vocabulary
// (apis/externalsecrets/v1/externalsecret_types.go, checked against v2.4.1,
// the version the homelab runs). persist.go stores the Ready condition's
// reason, so these are the only values a well-behaved controller writes.
// Anything else is reported as "Unknown": the reason column is controller
// text, and an allowlist is what stops an unexpected value from carrying
// provider detail past the projection.
var knownESOReasons = map[string]struct{}{
	"SecretSynced":        {},
	"SecretSyncedError":   {},
	"SecretDeleted":       {},
	"SecretMissing":       {},
	"ResourceSynced":      {},
	"ResourceSyncedError": {},
	"ResourceDeleted":     {},
	"ResourceMissing":     {},
}

type historyProjection struct {
	Level         projectionLevel `json:"level"`
	DroppedFields []string        `json:"droppedFields"`
}

type diffKeyCounts struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Changed int `json:"changed"`
}

// historyEntryDTO is one history row on the wire. Every field an
// outcome-only caller must not see is a pointer with omitempty, so at that
// level the key is ABSENT from the JSON. At the full level the pointers are
// always set, so an empty diff is [] and an empty message is "" — which is
// how a client tells "hidden" apart from "there was nothing".
type historyEntryDTO struct {
	ID                    int64         `json:"id"`
	AttemptAt             time.Time     `json:"attemptAt"`
	Outcome               string        `json:"outcome"`
	Reason                string        `json:"reason"`
	Message               *string       `json:"message,omitempty"`
	MessageTruncated      *bool         `json:"messageTruncated,omitempty"`
	DiffKeysAdded         *[]string     `json:"diffKeysAdded,omitempty"`
	DiffKeysRemoved       *[]string     `json:"diffKeysRemoved,omitempty"`
	DiffKeysChanged       *[]string     `json:"diffKeysChanged,omitempty"`
	DiffKeyCounts         diffKeyCounts `json:"diffKeyCounts"`
	SyncedResourceVersion *string       `json:"syncedResourceVersion,omitempty"`
}

type historyResponse struct {
	UID        string            `json:"uid"`
	ClusterID  string            `json:"clusterId"`
	Projection historyProjection `json:"projection"`
	Entries    []historyEntryDTO `json:"entries"`
}

// HandleGetExternalSecretHistory serves one page of an ExternalSecret's sync
// history, newest first.
//
//	GET /externalsecrets/externalsecrets/{namespace}/{name}/history?limit=50&cursor=<opaque>
//
// Gate order is part of the contract:
//
//  1. 401 without a user.
//  2. 501 remote_history_unsupported for a non-local cluster. Rows exist only
//     for the cluster this process polls; an empty 200 would read as "this
//     ExternalSecret never synced".
//  3. 503 eso_not_detected.
//  4. 403 without `get externalsecrets` in the namespace.
//  5. The ExternalSecret is read live through the impersonating client
//     (403/404 mapped) and its UID taken from that read. A UID is never
//     accepted from the request, so a deleted-and-recreated object starts
//     with an empty history and a caller cannot name another object's rows.
//  6. Projection level: full when the caller may also `get secrets` in the
//     namespace, otherwise outcome-only. A failed check is outcome-only.
//  7. 503 history_unavailable when no history store is configured.
//  8. 400 invalid_limit / invalid_cursor, then 503 history_unavailable on a
//     store fault. A fault is never an empty page.
//
// The Secret grant is read through AccessChecker, whose results are cached
// for accessCacheTTL (60s). A revoked `get secrets` grant can therefore keep
// a caller at the full projection for up to a minute — the same window every
// other RBAC decision on the platform has.
//
// Why not maskedSecret: that helper masks Secret VALUES. History holds no
// values — only key names and controller text — so the projection here is
// the redaction boundary, not a masking pass.
func (h *Handler) HandleGetExternalSecretHistory(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	clusterID := h.historyClusterID()
	if reqCluster := middleware.ClusterIDFromContext(ctx); !k8s.IsLocalClusterID(reqCluster) && reqCluster != clusterID {
		httputil.WriteErrorWithReason(w, http.StatusNotImplemented,
			"ExternalSecret history is recorded for the local cluster only",
			"remote_history_unsupported", nil)
		return
	}

	if !h.Discoverer.IsAvailable(ctx) {
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, "ESO not detected", "eso_not_detected", nil)
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.canAccess(ctx, user, "get", "externalsecrets", ns) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, err := h.dynForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("create impersonating dynamic client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	obj, err := dynClient.Resource(ExternalSecretGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		switch {
		case apierrors.IsForbidden(err):
			httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		case apierrors.IsNotFound(err):
			httputil.WriteError(w, http.StatusNotFound, "external secret not found", "")
		default:
			h.Logger.Error("get externalsecret for history", "namespace", ns, "name", name, "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch external secret", "")
		}
		return
	}
	uid := string(obj.GetUID())
	if uid == "" {
		// An object without a UID cannot be keyed to its history. Querying
		// with "" would be wrong and answering "empty" would be a lie.
		h.Logger.Error("externalsecret has no uid", "namespace", ns, "name", name)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch external secret", "")
		return
	}

	level := projectionOutcomeOnly
	if h.canAccessCore(ctx, user, "get", "secrets", ns) {
		level = projectionFull
	}

	if h.HistoryStore == nil {
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable,
			"history persistence is not configured", "history_unavailable", nil)
		return
	}

	limit, after, ok := parseHistoryPageParams(w, r)
	if !ok {
		return
	}

	page, err := h.HistoryStore.QueryPage(ctx, clusterID, uid, after, limit)
	if err != nil {
		h.Logger.Error("query eso sync history", "namespace", ns, "name", name, "uid", uid, "error", err)
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable,
			"sync history is temporarily unavailable", "history_unavailable", nil)
		return
	}

	resp := historyResponse{
		UID:        uid,
		ClusterID:  clusterID,
		Projection: historyProjection{Level: level, DroppedFields: []string{}},
		Entries:    make([]historyEntryDTO, 0, len(page.Entries)),
	}
	if level == projectionOutcomeOnly {
		resp.Projection.DroppedFields = outcomeOnlyDroppedFields
	}
	for _, e := range page.Entries {
		resp.Entries = append(resp.Entries, projectHistoryEntry(e, level))
	}

	httputil.WriteJSON(w, http.StatusOK, api.Response{
		Data:     resp,
		Metadata: &api.Metadata{Total: len(resp.Entries), Continue: page.NextCursor},
	})
}

// historyClusterID is the cluster id history rows are stamped with. Empty
// falls back to the configuration default so an unwired handler still reads
// the rows a default-configured poller writes.
func (h *Handler) historyClusterID() string {
	if h.ClusterID == "" {
		return k8s.LocalClusterID
	}
	return h.ClusterID
}

// parseHistoryPageParams reads ?limit= and ?cursor=. An absent limit is the
// store default and an out-of-range one is clamped by the store, but a limit
// that is not an integer is rejected: guessing what the client meant would
// hide its bug. A malformed cursor is always a 400, never page one, because a
// silent restart would loop a paginating client forever.
func parseHistoryPageParams(w http.ResponseWriter, r *http.Request) (int, *store.ESOHistoryCursor, bool) {
	q := r.URL.Query()

	limit := 0
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteErrorWithReason(w, http.StatusBadRequest, "limit must be an integer", "invalid_limit", nil)
			return 0, nil, false
		}
		limit = n
	}

	var after *store.ESOHistoryCursor
	if raw := q.Get("cursor"); raw != "" {
		c, err := store.DecodeESOHistoryCursor(raw)
		if err != nil {
			httputil.WriteErrorWithReason(w, http.StatusBadRequest, "cursor is not valid", "invalid_cursor", nil)
			return 0, nil, false
		}
		after = &c
	}
	return limit, after, true
}

// projectHistoryEntry renders one stored row at the given level. At
// outcome-only it copies only outcome, the allowlisted reason, timing, the
// row id and the diff cardinalities; every other field stays nil and is
// therefore absent from the JSON.
func projectHistoryEntry(e store.ESOSyncHistoryEntry, lvl projectionLevel) historyEntryDTO {
	dto := historyEntryDTO{
		ID:        e.ID,
		AttemptAt: e.AttemptAt,
		Outcome:   e.Outcome,
		Reason:    projectReason(e.Reason, lvl),
		DiffKeyCounts: diffKeyCounts{
			Added:   len(e.DiffKeysAdded),
			Removed: len(e.DiffKeysRemoved),
			Changed: len(e.DiffKeysChanged),
		},
	}
	if lvl != projectionFull {
		return dto
	}
	msg, truncated := sanitizeControllerText(e.Message, historyMessageMaxBytes)
	dto.Message = &msg
	dto.MessageTruncated = &truncated
	dto.DiffKeysAdded = nonNilKeys(e.DiffKeysAdded)
	dto.DiffKeysRemoved = nonNilKeys(e.DiffKeysRemoved)
	dto.DiffKeysChanged = nonNilKeys(e.DiffKeysChanged)
	rv := e.SyncedResourceVersion
	dto.SyncedResourceVersion = &rv
	return dto
}

// nonNilKeys returns a pointer to keys, or to an empty slice when keys is
// nil, so the full projection marshals an empty diff as [] rather than null.
func nonNilKeys(keys []string) *[]string {
	if keys == nil {
		keys = []string{}
	}
	return &keys
}

// projectReason returns reason as an outcome-only caller may see it — itself
// when on the knownESOReasons allowlist, "Unknown" otherwise — or, at the
// full level, sanitized controller text.
func projectReason(reason string, lvl projectionLevel) string {
	if lvl == projectionFull {
		out, _ := sanitizeControllerText(reason, historyReasonMaxBytes)
		return out
	}
	if _, ok := knownESOReasons[reason]; ok {
		return reason
	}
	return "Unknown"
}

// sanitizeControllerText makes controller-written free text safe to return:
//
//  1. invalid UTF-8 becomes U+FFFD;
//  2. control characters are dropped except \n and \t — this removes ESC,
//     BEL and the C1 CSI, so no ANSI/OSC sequence can act on a terminal
//     that prints the API response;
//  3. runs of more than two newlines collapse to two;
//  4. the result is cut to at most maxBytes on a rune boundary, ending in
//     "…", and truncated reports whether that happened.
//
// It does not HTML-escape. The web client renders text children, which
// Preact escapes; escaping here too would show "&lt;" to the user.
func sanitizeControllerText(s string, maxBytes int) (string, bool) {
	s = strings.ToValidUTF8(s, "�")

	var b strings.Builder
	b.Grow(min(len(s), maxBytes+utf8.UTFMax))
	newlines := 0
	for _, r := range s {
		switch {
		case r == '\n':
			newlines++
			if newlines > 2 {
				continue
			}
		case r == '\t':
			newlines = 0
		case unicode.IsControl(r):
			// Dropped without resetting newlines, so "\n\x00\n\n" still
			// collapses.
			continue
		default:
			newlines = 0
		}
		b.WriteRune(r)
		// Stop copying once the bound is certainly exceeded; the tail can
		// only be discarded.
		if b.Len() > maxBytes {
			break
		}
	}
	out := b.String()
	if len(out) <= maxBytes {
		return out, false
	}

	const ellipsis = "…"
	cut := maxBytes - len(ellipsis)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(out[cut]) {
		cut--
	}
	return out[:cut] + ellipsis, true
}
