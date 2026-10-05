package incidents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/diagnostics"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/topology"
)

// The three first-release adapters (plan §3.4 rule 8). Each one authorizes
// before it reads: a SelfSubjectAccessReview through CanAccessGroupResource
// with the capturing user's identity on the local cluster, then the read
// through that user's impersonated client. Nothing is read from the
// non-impersonated informer cache without a SAR for it (diagnostics' related
// pods and ReplicaSets are gated by the same resolution the diagnostics
// endpoint runs). Denial is reported as forbidden with a fixed detail that
// names nothing; an undetermined check is failed, never forbidden (Q1 P4).
// Redaction happens here, before the collector measures anything.

// Source ids.
const (
	SourceDiagnostics = "diagnostics"
	SourceObject      = "object"
	SourceEvents      = "events"
)

// maxEventsPerCapture bounds the impersonated event list.
const maxEventsPerCapture = 200

// Fixed, scope-free source details.
const (
	detailForbidden          = "the capturing user is not authorized to read this evidence"
	detailAuthzUnavailable   = "the capturing user's authorization could not be determined"
	detailNotFound           = "target not found"
	detailUnsupportedKind    = "target kind is not supported by diagnostics"
	detailUnresolvedVersion  = "target API version could not be resolved"
	detailClientUnavailable  = "impersonated client unavailable"
	detailResolveFailed      = "diagnostics could not resolve the target"
	detailReadFailed         = "target could not be read"
	detailListFailed         = "events could not be listed"
	detailProjectionTooLarge = "the projection was cut to fit the per-item size bound"
)

// ClientProvider is the impersonated-client seam (*k8s.ClientFactory in
// production).
type ClientProvider interface {
	ClientForUser(username string, groups []string) (kubernetes.Interface, error)
	DynamicClientForUser(username string, groups []string) (dynamic.Interface, error)
}

// authorize runs the SAR gate every adapter read goes through. done is true
// when the caller must return res (denied or undetermined) without reading.
func authorize(ctx context.Context, ac *resources.AccessChecker, logger *slog.Logger, req CaptureRequest, verb, apiGroup, resource, namespace string) (res SourceResult, done bool) {
	allowed, err := ac.CanAccessGroupResource(ctx, req.ClusterID, req.User.KubernetesUsername, req.User.KubernetesGroups, verb, apiGroup, resource, namespace)
	if err != nil {
		logger.Warn("incident capture authorization check failed", "verb", verb, "group", apiGroup, "resource", resource, "error", err)
		return SourceResult{Completeness: CompletenessFailed, Detail: detailAuthzUnavailable}, true
	}
	if !allowed {
		return SourceResult{Completeness: CompletenessForbidden, Detail: detailForbidden}, true
	}
	return SourceResult{}, false
}

func failed(detail string) SourceResult {
	return SourceResult{Completeness: CompletenessFailed, Detail: detail}
}

func targetSourceRef(req CaptureRequest) SourceRef {
	return SourceRef{
		ClusterID: req.ClusterID,
		APIGroup:  req.Target.APIGroup,
		Resource:  req.Target.Resource,
		Kind:      req.Target.Kind,
		Namespace: req.Target.Namespace,
		Name:      req.Target.Name,
		UID:       req.Target.UID,
	}
}

// secretDerivedObject decides Q1 P11.4 on an object's content the same way
// RedactObject does: a Secret by kind or by resource, or any Secret
// reference in a pod spec.
func secretDerivedObject(obj map[string]any, resource string) bool {
	return asString(obj["kind"]) == "Secret" || isSecretsResource(resource) || referencesSecret(obj)
}

func isSecretTarget(t TargetRef) bool {
	return t.Kind == "Secret" || isSecretsResource(t.Resource)
}

// textMeta accumulates sanitization flags for free-text fields.
type textMeta struct{ sanitized, truncated bool }

// text sanitizes controller-authored text to the per-field bound.
func (m *textMeta) text(s string) string {
	out, changed, truncated := sanitize(s, maxFieldBytes)
	m.sanitized = m.sanitized || changed
	m.truncated = m.truncated || truncated
	return out
}

func (m textMeta) apply(meta *RedactionMeta) {
	if m.sanitized {
		meta.Rules = append(meta.Rules, RuleTextSanitized)
	}
	if m.truncated {
		meta.Truncated = true
	}
}

// finishRules appends RuleTruncated when Truncated is set and settles
// Applied.
func finishRules(meta *RedactionMeta) {
	if meta.Truncated {
		meta.Rules = append(meta.Rules, RuleTruncated)
	}
	meta.Applied = len(meta.Rules) > 0
}

// readTarget performs the impersonated GET of the target. A nil object with
// a nil error means res explains why (not found, forbidden, unresolved); a
// non-nil error is the context's, for the collector to classify.
func readTarget(ctx context.Context, clients ClientProvider, mapper meta.RESTMapper, logger *slog.Logger, req CaptureRequest) (*unstructured.Unstructured, SourceResult, error) {
	gvr, ok := targetGVR(req.Target, mapper)
	if !ok {
		return nil, failed(detailUnresolvedVersion), nil
	}
	dyn, err := clients.DynamicClientForUser(req.User.KubernetesUsername, req.User.KubernetesGroups)
	if err != nil {
		logger.Error("incident capture impersonated dynamic client unavailable", "error", err)
		return nil, failed(detailClientUnavailable), nil
	}
	ri := dyn.Resource(gvr)
	var obj *unstructured.Unstructured
	if req.Target.Namespace != "" {
		obj, err = ri.Namespace(req.Target.Namespace).Get(ctx, req.Target.Name, metav1.GetOptions{})
	} else {
		obj, err = ri.Get(ctx, req.Target.Name, metav1.GetOptions{})
	}
	switch {
	case err == nil && obj != nil:
		return obj, SourceResult{}, nil
	case ctx.Err() != nil:
		return nil, SourceResult{}, ctx.Err()
	case err == nil || apierrors.IsNotFound(err):
		return nil, failed(detailNotFound), nil
	case apierrors.IsForbidden(err):
		return nil, SourceResult{Completeness: CompletenessForbidden, Detail: detailForbidden}, nil
	default:
		logger.Warn("incident capture target read failed", "error", err)
		return nil, failed(detailReadFailed), nil
	}
}

// targetGVR resolves the target's GroupVersionResource: the request's
// version when given, else the mapper's preferred version.
func targetGVR(t TargetRef, mapper meta.RESTMapper) (schema.GroupVersionResource, bool) {
	if t.Version != "" {
		return schema.GroupVersionResource{Group: t.APIGroup, Version: t.Version, Resource: t.Resource}, true
	}
	if mapper == nil {
		return schema.GroupVersionResource{}, false
	}
	gvr, err := mapper.ResourceFor(schema.GroupVersionResource{Group: t.APIGroup, Resource: t.Resource})
	if err != nil || gvr.Version == "" {
		return schema.GroupVersionResource{}, false
	}
	return gvr, true
}

// ---------------------------------------------------------------------------
// diagnostics
// ---------------------------------------------------------------------------

type diagnosticsSource struct {
	lister   topology.ResourceLister
	access   *resources.AccessChecker
	redactor *Redactor
	logger   *slog.Logger
}

// NewDiagnosticsSource captures one diagnostic_check snapshot per normalized
// check result. Supported kinds are exactly the kinds diagnostics resolves.
// The target is SAR-gated for `get`; related pods and ReplicaSets are gated
// by diagnostics.ResolveRelatedRBAC, as the diagnostics endpoint gates them.
func NewDiagnosticsSource(lister topology.ResourceLister, access *resources.AccessChecker, redactor *Redactor, logger *slog.Logger) Source {
	if logger == nil {
		logger = slog.Default()
	}
	return &diagnosticsSource{lister: lister, access: access, redactor: redactor, logger: logger}
}

func (s *diagnosticsSource) ID() string { return SourceDiagnostics }

func (s *diagnosticsSource) Collect(ctx context.Context, req CaptureRequest) (SourceResult, error) {
	t := req.Target
	group, _, resource, ok := diagnostics.TargetResource(t.Kind)
	if !ok {
		return failed(detailUnsupportedKind), nil
	}
	if res, done := authorize(ctx, s.access, s.logger, req, "get", group, resource, t.Namespace); done {
		return res, nil
	}
	related := diagnostics.ResolveRelatedRBAC(ctx, s.access, s.logger, req.User, req.ClusterID, t.Kind, t.Namespace)
	target, err := diagnostics.Resolve(ctx, s.lister, t.Namespace, t.Kind, t.Name, related)
	if err != nil {
		if ctx.Err() != nil {
			return SourceResult{}, ctx.Err()
		}
		if strings.Contains(err.Error(), "not found") {
			return failed(detailNotFound), nil
		}
		s.logger.Warn("incident capture diagnostics resolve failed", "error", err)
		return failed(detailResolveFailed), nil
	}
	results := diagnostics.RunDiagnostics(ctx, target)
	observedAt := time.Now().UTC()
	checks := diagnostics.Normalize(req.ClusterID, target, observedAt, results)

	secretDerived := false
	resourceVersion := ""
	if target.Object != nil {
		if content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(target.Object); err == nil {
			secretDerived = secretDerivedObject(content, resource)
		}
		if acc, err := meta.Accessor(target.Object); err == nil {
			resourceVersion = acc.GetResourceVersion()
		}
	}

	items := make([]Evidence, 0, len(checks))
	for _, ch := range checks {
		var tm textMeta
		ch.Message = tm.text(ch.Message)
		ch.Detail = tm.text(ch.Detail)
		ch.Remediation = tm.text(ch.Remediation)
		payload, err := json.Marshal(ch)
		if err != nil {
			s.logger.Warn("incident capture check result did not marshal", "checkId", ch.CheckID, "error", err)
			continue
		}
		redaction := RedactionMeta{SecretDerived: secretDerived}
		tm.apply(&redaction)
		finishRules(&redaction)
		observed := ch.ObservedAt
		items = append(items, Evidence{
			EvidenceKind: EvidenceKindDiagnosticCheck,
			Mode:         ModeSnapshot,
			Source: SourceRef{
				ClusterID: ch.Source.ClusterID, APIGroup: ch.Source.Group, Resource: ch.Source.Resource, Kind: ch.Source.Kind,
				Namespace: ch.Source.Namespace, Name: ch.Source.Name, UID: ch.Source.UID, ResourceVersion: resourceVersion,
			},
			SourceObservedAt: &observed,
			Completeness:     CompletenessComplete,
			Redaction:        redaction,
			Payload:          payload,
			discriminator:    checkDiscriminator(ch),
		})
	}
	return SourceResult{Items: items, Completeness: CompletenessComplete}, nil
}

// checkDiscriminator is the capture-key part of a check: its id, status,
// reason and sorted evidence. ObservedAt is deliberately absent (D-2).
func checkDiscriminator(ch diagnostics.CheckResult) []string {
	out := []string{ch.CheckID, string(ch.Status), ch.Reason}
	keys := make([]string, 0, len(ch.Evidence))
	for k := range ch.Evidence {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k, ch.Evidence[k])
	}
	return out
}

// ---------------------------------------------------------------------------
// object
// ---------------------------------------------------------------------------

type objectSource struct {
	clients  ClientProvider
	mapper   meta.RESTMapper
	access   *resources.AccessChecker
	redactor *Redactor
	logger   *slog.Logger
}

// NewObjectSource captures the target object as a redacted object_summary
// snapshot paired with a live_link to the same object. mapper resolves the
// API version when the request leaves it empty; nil is allowed when callers
// always supply one.
func NewObjectSource(clients ClientProvider, mapper meta.RESTMapper, access *resources.AccessChecker, redactor *Redactor, logger *slog.Logger) Source {
	if logger == nil {
		logger = slog.Default()
	}
	return &objectSource{clients: clients, mapper: mapper, access: access, redactor: redactor, logger: logger}
}

func (s *objectSource) ID() string { return SourceObject }

func (s *objectSource) Collect(ctx context.Context, req CaptureRequest) (SourceResult, error) {
	t := req.Target
	if res, done := authorize(ctx, s.access, s.logger, req, "get", t.APIGroup, t.Resource, t.Namespace); done {
		return res, nil
	}
	obj, res, err := readTarget(ctx, s.clients, s.mapper, s.logger, req)
	if err != nil {
		return SourceResult{}, err
	}
	if obj == nil {
		return res, nil
	}
	// The plural lowercase resource is what selects the Secret projection
	// for an object whose kind is absent (U22a contract).
	projection, redaction := s.redactor.RedactObject(obj.Object, t.Resource)
	payload, err := json.Marshal(projection)
	if err != nil {
		s.logger.Warn("incident capture projection did not marshal", "error", err)
		return failed(detailReadFailed), nil
	}
	src := targetSourceRef(req)
	src.UID = string(obj.GetUID())
	src.ResourceVersion = obj.GetResourceVersion()

	snapshot := Evidence{
		EvidenceKind:  EvidenceKindObjectSummary,
		Mode:          ModeSnapshot,
		Source:        src,
		Completeness:  CompletenessComplete,
		Redaction:     redaction,
		Payload:       payload,
		discriminator: []string{src.ResourceVersion},
	}
	if redaction.Truncated {
		snapshot.Completeness, snapshot.CompletenessDetail = CompletenessPartial, detailProjectionTooLarge
	}
	live := Evidence{
		EvidenceKind: EvidenceKindObjectSummary,
		Mode:         ModeLiveLink,
		Source:       src,
		Completeness: CompletenessComplete,
		Redaction:    RedactionMeta{SecretDerived: redaction.SecretDerived},
	}
	return SourceResult{Items: []Evidence{snapshot, live}, Completeness: CompletenessComplete}, nil
}

// ---------------------------------------------------------------------------
// events
// ---------------------------------------------------------------------------

type eventsSource struct {
	clients  ClientProvider
	mapper   meta.RESTMapper
	access   *resources.AccessChecker
	redactor *Redactor
	logger   *slog.Logger
}

// NewEventsSource captures the target's events as one event_list snapshot.
// Events are SAR-gated for `list` and listed through the impersonated typed
// client (never the informer cache) by involvedObject.uid. The UID comes from
// the request when the caller knows it, else from a SAR-gated impersonated
// GET of the target; without one the list falls back to involvedObject
// name and kind and the item is identity-weak.
func NewEventsSource(clients ClientProvider, mapper meta.RESTMapper, access *resources.AccessChecker, redactor *Redactor, logger *slog.Logger) Source {
	if logger == nil {
		logger = slog.Default()
	}
	return &eventsSource{clients: clients, mapper: mapper, access: access, redactor: redactor, logger: logger}
}

func (s *eventsSource) ID() string { return SourceEvents }

func (s *eventsSource) Collect(ctx context.Context, req CaptureRequest) (SourceResult, error) {
	t := req.Target
	if res, done := authorize(ctx, s.access, s.logger, req, "list", "", "events", t.Namespace); done {
		return res, nil
	}
	src := targetSourceRef(req)
	secretDerived := isSecretTarget(t)
	if src.UID == "" {
		// A light impersonated GET for the UID, under its own gate. Any
		// outcome short of a context error leaves the UID empty.
		if _, denied := authorize(ctx, s.access, s.logger, req, "get", t.APIGroup, t.Resource, t.Namespace); !denied {
			obj, _, err := readTarget(ctx, s.clients, s.mapper, s.logger, req)
			if err != nil {
				return SourceResult{}, err
			}
			if obj != nil {
				src.UID = string(obj.GetUID())
				src.ResourceVersion = obj.GetResourceVersion()
				secretDerived = secretDerived || secretDerivedObject(obj.Object, t.Resource)
			}
		}
	}

	cs, err := s.clients.ClientForUser(req.User.KubernetesUsername, req.User.KubernetesGroups)
	if err != nil {
		s.logger.Error("incident capture impersonated client unavailable", "error", err)
		return failed(detailClientUnavailable), nil
	}
	var selector fields.Selector
	if src.UID != "" {
		selector = fields.OneTermEqualSelector("involvedObject.uid", src.UID)
	} else {
		selector = fields.SelectorFromSet(fields.Set{"involvedObject.name": t.Name, "involvedObject.kind": t.Kind})
	}
	list, err := cs.CoreV1().Events(t.Namespace).List(ctx, metav1.ListOptions{FieldSelector: selector.String(), Limit: maxEventsPerCapture})
	switch {
	case err == nil:
	case ctx.Err() != nil:
		return SourceResult{}, ctx.Err()
	case apierrors.IsForbidden(err):
		return SourceResult{Completeness: CompletenessForbidden, Detail: detailForbidden}, nil
	default:
		s.logger.Warn("incident capture event list failed", "error", err)
		return failed(detailListFailed), nil
	}

	// The server applied the selector; it is re-applied here so a list
	// that ignores field selectors (a fake, a lenient proxy) can never
	// attach another object's events to this target.
	matching := make([]corev1.Event, 0, len(list.Items))
	for _, ev := range list.Items {
		if src.UID != "" && string(ev.InvolvedObject.UID) != src.UID {
			continue
		}
		if src.UID == "" && (ev.InvolvedObject.Name != t.Name || ev.InvolvedObject.Kind != t.Kind) {
			continue
		}
		matching = append(matching, ev)
	}
	projected := projectEvents(s.redactor, matching, list.Continue != "", secretDerived)

	item := Evidence{
		EvidenceKind:     EvidenceKindEventList,
		Mode:             ModeSnapshot,
		Source:           src,
		SourceObservedAt: projected.observedAt,
		Completeness:     CompletenessComplete,
		Redaction:        projected.meta,
		Payload:          projected.payload,
		discriminator:    []string{projected.digest},
	}
	if projected.meta.Truncated {
		item.Completeness, item.CompletenessDetail = CompletenessPartial, detailProjectionTooLarge
	}
	return SourceResult{Items: []Evidence{item}, Completeness: CompletenessComplete}, nil
}

// eventListPayload is the allowlisted event_list shape. Observed is how many
// events the list returned (at most maxEventsPerCapture); Truncated is true
// when more existed than were kept, for count or for size.
type eventListPayload struct {
	Events    []eventSummary `json:"events"`
	Observed  int            `json:"observed"`
	Truncated bool           `json:"truncated"`
}

type eventSummary struct {
	Type            string         `json:"type,omitempty"`
	Reason          string         `json:"reason,omitempty"`
	Message         string         `json:"message,omitempty"`
	Count           int32          `json:"count,omitempty"`
	FirstTimestamp  *time.Time     `json:"firstTimestamp,omitempty"`
	LastTimestamp   *time.Time     `json:"lastTimestamp,omitempty"`
	EventTime       *time.Time     `json:"eventTime,omitempty"`
	InvolvedObject  eventObjectRef `json:"involvedObject"`
	SourceComponent string         `json:"sourceComponent,omitempty"`
}

type eventObjectRef struct {
	Kind string `json:"kind,omitempty"`
	Name string `json:"name,omitempty"`
	UID  string `json:"uid,omitempty"`
}

// projectedEvents is projectEvents' result.
type projectedEvents struct {
	payload    json.RawMessage
	meta       RedactionMeta
	observedAt *time.Time // newest event time, nil when no event carries one
	digest     string     // capture-key discriminator over every observed event
}

// projectEvents projects events onto the allowlist (type, reason, sanitized
// message, count, timestamps, involvedObject kind/name/uid, source
// component), newest first, at most maxEventsPerCapture, and cuts the tail
// until the payload fits r's byte bound. Every cut is flagged. more says the
// server had further events beyond the list. The digest covers the (uid,
// count, time) tuples of every event the list returned, so an unchanged
// list dedupes whatever the size cut kept.
func projectEvents(r *Redactor, events []corev1.Event, more, secretDerived bool) projectedEvents {
	sort.SliceStable(events, func(i, j int) bool {
		return eventObservedAt(&events[i]).After(eventObservedAt(&events[j]))
	})
	truncated := more
	if len(events) > maxEventsPerCapture {
		events = events[:maxEventsPerCapture]
		truncated = true
	}

	var tm textMeta
	var newest time.Time
	tuples := make([]string, 0, len(events))
	summaries := make([]eventSummary, 0, len(events))
	for i := range events {
		ev := &events[i]
		if at := eventObservedAt(ev); at.After(newest) {
			newest = at
		}
		if ev.InvolvedObject.Kind == "Secret" {
			secretDerived = true
		}
		count := ev.Count
		if count == 0 && ev.Series != nil {
			count = ev.Series.Count
		}
		tuples = append(tuples, string(ev.UID)+"\x00"+ev.Name+"\x00"+itoa(count)+"\x00"+eventObservedAt(ev).UTC().Format(time.RFC3339Nano))
		summaries = append(summaries, eventSummary{
			Type:            tm.text(ev.Type),
			Reason:          tm.text(ev.Reason),
			Message:         tm.text(ev.Message),
			Count:           count,
			FirstTimestamp:  optionalTime(ev.FirstTimestamp.Time),
			LastTimestamp:   optionalTime(ev.LastTimestamp.Time),
			EventTime:       optionalTime(ev.EventTime.Time),
			InvolvedObject:  eventObjectRef{Kind: tm.text(ev.InvolvedObject.Kind), Name: tm.text(ev.InvolvedObject.Name), UID: tm.text(string(ev.InvolvedObject.UID))},
			SourceComponent: tm.text(ev.Source.Component),
		})
	}
	sort.Strings(tuples)
	digestInput, _ := json.Marshal(tuples)
	sum := sha256.Sum256(digestInput)

	// A string cut past the field bound is a cut of the observation too:
	// the payload's flag and the metadata's must agree.
	truncated = truncated || tm.truncated
	payload := eventListPayload{Events: summaries, Observed: len(events), Truncated: truncated}
	var b []byte
	for {
		payload.Truncated = truncated
		var err error
		b, err = json.Marshal(payload)
		if err == nil && len(b) <= r.maxBytes {
			break
		}
		truncated = true
		if len(payload.Events) == 0 {
			// Even the empty list does not fit (or did not marshal): the
			// smallest honest payload.
			b = []byte(`{"events":[],"observed":` + itoa(int32(len(events))) + `,"truncated":true}`)
			break
		}
		payload.Events = payload.Events[:len(payload.Events)/2]
	}

	meta := RedactionMeta{SecretDerived: secretDerived, FieldsRemoved: len(events)}
	if len(events) > 0 {
		meta.Rules = append(meta.Rules, RuleFieldAllowlist)
	}
	tm.apply(&meta)
	meta.Truncated = meta.Truncated || truncated
	finishRules(&meta)

	out := projectedEvents{payload: b, meta: meta, digest: hex.EncodeToString(sum[:])}
	if !newest.IsZero() {
		at := newest.UTC()
		out.observedAt = &at
	}
	return out
}

// eventObservedAt is the newest of an event's timestamps, zero when it has
// none.
func eventObservedAt(ev *corev1.Event) time.Time {
	var at time.Time
	for _, t := range []time.Time{ev.LastTimestamp.Time, ev.EventTime.Time, ev.FirstTimestamp.Time} {
		if t.After(at) {
			at = t
		}
	}
	return at
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func itoa(n int32) string { return strconv.FormatInt(int64(n), 10) }
