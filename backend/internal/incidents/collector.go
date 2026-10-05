package incidents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/store"
)

// This file is the evidence envelope (plan §3.2) and the bounded collector
// (plan §3.4). The collector writes nothing: Capture returns a CaptureReport
// and the capture endpoint (U23b) persists its items through
// store.IncidentEvidenceStore.InsertBatch, so cancellation (rule 6) is the
// caller's contract and is made safe here by returning ctx.Err() with no
// items when the request context ends.
//
// Goroutine shape, for anyone adding a source or a raw goroutine: every
// source runs in an errgroup worker launched through recoverutil.Go, results
// land in a pre-sized slice indexed by position (no shared map, no append,
// no lock, no lost write), and nothing that must run regardless of a panic
// lives inside the wrapped closure. There is deliberately no sync.WaitGroup
// and no counted channel here, so the cleanup-inside-the-closure hazard in
// docs/solutions/backend-resilience-conventions.md is unreachable.

// Completeness says how much of what a source set out to observe it did.
type Completeness string

const (
	CompletenessComplete  Completeness = store.EvidenceCompletenessComplete
	CompletenessPartial   Completeness = store.EvidenceCompletenessPartial
	CompletenessFailed    Completeness = store.EvidenceCompletenessFailed
	CompletenessForbidden Completeness = store.EvidenceCompletenessForbidden
	CompletenessTimedOut  Completeness = store.EvidenceCompletenessTimedOut
)

// EvidenceMode distinguishes a retained payload from a live reference.
type EvidenceMode string

const (
	// ModeSnapshot stores an immutable, redacted payload. What it says is
	// what was observed at CollectedAt; it never changes if the live object
	// does.
	ModeSnapshot EvidenceMode = store.EvidenceModeSnapshot
	// ModeLiveLink stores only a reference. It is resolved against the live
	// cluster at read time under the reader's current authorization, and may
	// legitimately 404 (deleted) or 403 (revoked) later.
	ModeLiveLink EvidenceMode = store.EvidenceModeLiveLink
)

// Evidence kinds (the incident_evidence CHECK set).
const (
	EvidenceKindDiagnosticCheck = store.EvidenceKindDiagnosticCheck
	EvidenceKindObjectSummary   = store.EvidenceKindObjectSummary
	EvidenceKindEventList       = store.EvidenceKindEventList
)

// Withheld reasons (Q1 P4).
const (
	WithheldForbidden                     = "forbidden"
	WithheldAuthorizationCheckUnavailable = "authorization_check_unavailable"
)

// SourceRef identifies the object an evidence item was captured from. It is
// also the item's authorization scope: the read path re-checks
// (ClusterID, APIGroup, Resource, Namespace) with CanAccessGroupResource, so
// Resource is the plural lowercase API resource ("pods", "deployments").
type SourceRef struct {
	ClusterID       string `json:"clusterId"`
	APIGroup        string `json:"apiGroup"` // "" for core
	Resource        string `json:"resource"` // plural, lowercase: the SAR resource
	Kind            string `json:"kind"`     // display only
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	UID             string `json:"uid,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
	// IdentityWeak is true when no UID could be observed (Q1 P9): the item
	// is bound to a name, and the UI must say so rather than imply object
	// identity.
	IdentityWeak bool `json:"identityWeak,omitempty"`
}

// Evidence is one captured item. SourceObservedAt is when the cluster
// observed the fact (an Event's lastTimestamp, a check's observedAt) and is
// nil when the source has none; CollectedAt is when k8sCenter wrote it down.
// They routinely differ by minutes.
type Evidence struct {
	ID                 string          `json:"id"`
	IncidentID         string          `json:"incidentId"`
	EvidenceKind       string          `json:"evidenceKind"`
	Mode               EvidenceMode    `json:"mode"`
	Source             SourceRef       `json:"source"`
	SourceObservedAt   *time.Time      `json:"sourceObservedAt,omitempty"`
	CollectedAt        time.Time       `json:"collectedAt"`
	Completeness       Completeness    `json:"completeness"`
	CompletenessDetail string          `json:"completenessDetail,omitempty"`
	Redaction          RedactionMeta   `json:"redaction"`
	Payload            json.RawMessage `json:"payload,omitempty"` // snapshot only
	PayloadBytes       int             `json:"payloadBytes"`
	CaptureKey         string          `json:"-"`

	// discriminator is the kind-specific part of the capture key (see
	// CaptureKey). Adapters set it; the collector derives the key.
	discriminator []string
}

// WithheldEvidence is the ONLY shape emitted for an item the caller may not
// read. It deliberately carries no scope fields: namespace, name, kind and
// resource are themselves disclosure (Q1 P4).
type WithheldEvidence struct {
	ID             string    `json:"id"`
	EvidenceKind   string    `json:"evidenceKind"`
	CollectedAt    time.Time `json:"collectedAt"`
	Withheld       bool      `json:"withheld"` // always true
	WithheldReason string    `json:"withheldReason"`
}

// Limits bound one capture (plan A-6). The byte, item and scope limits mirror
// store.EvidenceLimits and can never exceed the SQL CHECK ceilings.
type Limits struct {
	MaxItemBytes     int
	MaxIncidentBytes int
	MaxItems         int
	MaxScopes        int
	CaptureTimeout   time.Duration
	SourceTimeout    time.Duration
	MaxConcurrency   int
}

// DefaultLimits returns the Release D defaults: 1 MiB per item, 10 MiB per
// incident, 500 items, 20 scopes, 20 s per capture, 5 s per source, 4
// concurrent sources.
func DefaultLimits() Limits {
	return Limits{
		MaxItemBytes:     store.EvidenceMaxItemBytesCeiling,
		MaxIncidentBytes: store.EvidenceMaxIncidentBytesCeiling,
		MaxItems:         store.EvidenceMaxItemsCeiling,
		MaxScopes:        store.EvidenceMaxScopesCeiling,
		CaptureTimeout:   20 * time.Second,
		SourceTimeout:    5 * time.Second,
		MaxConcurrency:   4,
	}
}

// EvidenceLimits is the store's view of the same bounds, for InsertBatch.
func (l Limits) EvidenceLimits() store.EvidenceLimits {
	return store.EvidenceLimits{
		MaxItemBytes:     l.MaxItemBytes,
		MaxIncidentBytes: l.MaxIncidentBytes,
		MaxItems:         l.MaxItems,
		MaxScopes:        l.MaxScopes,
	}
}

// Validate rejects limits the store would refuse, a per-item bound the
// Redactor cannot meet, and non-positive timeouts or concurrency.
func (l Limits) Validate() error {
	if err := l.EvidenceLimits().Validate(); err != nil {
		return err
	}
	if l.MaxItemBytes < MinMaxBytes {
		return fmt.Errorf("incidents: max item bytes must be at least %d, got %d", MinMaxBytes, l.MaxItemBytes)
	}
	if l.CaptureTimeout <= 0 || l.SourceTimeout <= 0 {
		return fmt.Errorf("incidents: capture and source timeouts must be positive, got %s and %s", l.CaptureTimeout, l.SourceTimeout)
	}
	if l.MaxConcurrency < 1 {
		return fmt.Errorf("incidents: max concurrency must be at least 1, got %d", l.MaxConcurrency)
	}
	return nil
}

// TargetRef names the object a capture is about. Resource is the plural
// lowercase API resource; Version may be empty when the adapter can resolve
// it through a REST mapper. UID is optional: when the caller already knows
// it, the events adapter needs no object read.
type TargetRef struct {
	APIGroup  string
	Version   string
	Resource  string
	Kind      string
	Namespace string
	Name      string
	UID       string
}

// CaptureRequest is one capture. Sources names the source ids to run; empty
// means every registered source. Only the local cluster is supported in
// Release D (plan A-12).
type CaptureRequest struct {
	ClusterID string
	User      *auth.User
	Target    TargetRef
	Sources   []string
}

// SourceResult is what one Source observed.
type SourceResult struct {
	Items        []Evidence
	Completeness Completeness
	// Detail is a fixed, scope-free explanation. It is persisted and shown to
	// collaborators, so it must never name a namespace, object, kind or
	// resource (Q1 P4).
	Detail string
}

// Source is an evidence adapter. ID is a stable identity used in logs and
// per-source reporting ("diagnostics", "object", "events"). Collect must
// authorize every read through the caller's impersonated identity before
// performing it, redact before returning, and honour ctx; it may return an
// error for an unexpected failure (the collector records it as failed, or
// timed_out when the source deadline passed) but must never panic the
// process: a panic is recovered and recorded as failed.
type Source interface {
	ID() string
	Collect(ctx context.Context, req CaptureRequest) (SourceResult, error)
}

// SourceReport is the per-source breakdown of a capture.
type SourceReport struct {
	ID           string       `json:"id"`
	Completeness Completeness `json:"completeness"`
	Detail       string       `json:"detail,omitempty"`
	Items        int          `json:"items"`
}

// CaptureReport is what Capture returns. Completeness is complete only when
// every source is; otherwise partial, with the per-source breakdown saying
// which source fell short and why. Items are finalized (CollectedAt,
// CaptureKey, PayloadBytes set, per-item bound and store validation applied)
// and ready for Row.
type CaptureReport struct {
	Completeness Completeness   `json:"completeness"`
	CollectedAt  time.Time      `json:"collectedAt"`
	Sources      []SourceReport `json:"sources"`
	Items        []Evidence     `json:"items"`
}

// Typed request errors. The capture endpoint maps each to its own status.
var (
	// ErrRemoteCaptureUnsupported: the request named a cluster other than
	// the local one (400 remote_capture_unsupported).
	ErrRemoteCaptureUnsupported = errors.New("incidents: evidence capture is supported on the local cluster only")
	// ErrInvalidCaptureRequest: the request lacks a user identity or a
	// target (400).
	ErrInvalidCaptureRequest = errors.New("incidents: invalid capture request")
	// ErrUnknownSource: the request named a source id the collector does not
	// have (400). The wrapping *UnknownSourceError carries the id.
	ErrUnknownSource = errors.New("incidents: unknown evidence source")
)

// UnknownSourceError names the unregistered source id a request asked for.
type UnknownSourceError struct{ ID string }

func (e *UnknownSourceError) Error() string { return fmt.Sprintf("%v: %q", ErrUnknownSource, e.ID) }

// Is makes errors.Is(err, ErrUnknownSource) hold.
func (e *UnknownSourceError) Is(target error) bool { return target == ErrUnknownSource }

// Collector runs sources under bounded concurrency, per-source and
// whole-capture deadlines, and panic recovery, and turns their results into
// one honest report. It is safe for concurrent use.
type Collector struct {
	sources []Source
	limits  Limits
	logger  *slog.Logger
	now     func() time.Time
}

// NewCollector validates limits and registers sources. Source ids must be
// unique.
func NewCollector(sources []Source, limits Limits, logger *slog.Logger) (*Collector, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(sources))
	for _, s := range sources {
		if s == nil || s.ID() == "" {
			return nil, errors.New("incidents: a source must have a non-empty id")
		}
		if seen[s.ID()] {
			return nil, fmt.Errorf("incidents: duplicate source id %q", s.ID())
		}
		seen[s.ID()] = true
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Collector{sources: sources, limits: limits, logger: logger, now: time.Now}, nil
}

// sourceOutcome is one worker's slot. done is false when the worker never
// wrote its slot, which after Wait means it panicked.
type sourceOutcome struct {
	done   bool
	result SourceResult
	err    error
	ctxErr error // the source context's error when the worker returned
}

// Capture runs the requested sources and returns the report.
//
// Rules (plan §3.4): a plain errgroup bounds concurrency; each worker is
// launched through recoverutil.Go and writes its own slot; a source's
// failure, timeout or panic is recorded in that slot and never cancels a
// sibling (the group derives no shared context, so even a recovered panic,
// which recoverutil.Go surfaces as a returned error, cannot cancel the
// others); every source gets its own deadline under the whole-capture
// deadline. Redaction happened inside the adapters, so the per-item byte
// bound, store validation, in-batch de-duplication and the item count bound
// are applied here, after collection and before anything is returned.
//
// When ctx (the request context, not the capture deadline) is done by the
// time the sources finish, Capture returns ctx.Err() and an empty report:
// a cancelled capture gives the caller nothing to persist (rule 6).
func (c *Collector) Capture(ctx context.Context, req CaptureRequest) (CaptureReport, error) {
	selected, err := c.selectSources(req.Sources)
	if err != nil {
		return CaptureReport{}, err
	}
	if err := validateRequest(req); err != nil {
		return CaptureReport{}, err
	}
	req.ClusterID = k8s.LocalClusterID
	if err := ctx.Err(); err != nil {
		return CaptureReport{}, err
	}

	captureCtx, cancel := context.WithTimeout(ctx, c.limits.CaptureTimeout)
	defer cancel()

	var g errgroup.Group
	g.SetLimit(c.limits.MaxConcurrency)
	outcomes := make([]sourceOutcome, len(selected))
	for i, src := range selected {
		recoverutil.Go(&g, c.logger, "incidents capture "+src.ID(), func() error {
			srcCtx, cancel := context.WithTimeout(captureCtx, c.limits.SourceTimeout)
			defer cancel()
			res, err := src.Collect(srcCtx, req)
			outcomes[i] = sourceOutcome{done: true, result: res, err: err, ctxErr: srcCtx.Err()}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		// Only a recovered panic reaches here; it is already logged with
		// its stack and is reported below as a failed source.
		c.logger.Warn("incident capture source panicked", "error", err)
	}
	if err := ctx.Err(); err != nil {
		return CaptureReport{}, err
	}

	now := c.now().UTC()
	report := CaptureReport{Completeness: CompletenessComplete, CollectedAt: now, Sources: make([]SourceReport, 0, len(selected))}
	keys := make(map[string]bool)
	for i, src := range selected {
		sr, items := c.finalize(src.ID(), outcomes[i], now, keys)
		if sr.Completeness != CompletenessComplete {
			report.Completeness = CompletenessPartial
		}
		if room := c.limits.MaxItems - len(report.Items); len(items) > room {
			items = items[:room]
			sr.Items = len(items)
			sr.Completeness, sr.Detail = downgrade(sr.Completeness, sr.Detail, "the capture item count bound was reached and later items were dropped")
			report.Completeness = CompletenessPartial
		}
		report.Items = append(report.Items, items...)
		report.Sources = append(report.Sources, sr)
	}
	if report.Items == nil {
		report.Items = []Evidence{}
	}
	return report, nil
}

// selectSources resolves the requested ids (deduplicated, registration
// order) or every source when none were named.
func (c *Collector) selectSources(ids []string) ([]Source, error) {
	if len(ids) == 0 {
		return c.sources, nil
	}
	byID := make(map[string]Source, len(c.sources))
	for _, s := range c.sources {
		byID[s.ID()] = s
	}
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		if _, ok := byID[id]; !ok {
			return nil, &UnknownSourceError{ID: id}
		}
		wanted[id] = true
	}
	out := make([]Source, 0, len(wanted))
	for _, s := range c.sources {
		if wanted[s.ID()] {
			out = append(out, s)
		}
	}
	return out, nil
}

func validateRequest(req CaptureRequest) error {
	if !k8s.IsLocalClusterID(req.ClusterID) {
		return ErrRemoteCaptureUnsupported
	}
	if req.User == nil || req.User.KubernetesUsername == "" {
		return fmt.Errorf("%w: a user identity is required", ErrInvalidCaptureRequest)
	}
	if strings.TrimSpace(req.Target.Resource) == "" || strings.TrimSpace(req.Target.Name) == "" {
		return fmt.Errorf("%w: a target resource and name are required", ErrInvalidCaptureRequest)
	}
	return nil
}

// Fixed, scope-free details. They are persisted and shown to collaborators.
const (
	detailPanicked     = "source failed unexpectedly"
	detailFailed       = "source failed"
	detailTimedOut     = "source did not finish within its time limit"
	detailOversize     = "one or more items exceeded the per-item size bound and were dropped"
	detailInvalid      = "one or more items could not be stored and were dropped"
	detailUnknownState = "source reported an unknown completeness"
)

// finalize turns one worker's slot into its report and finalized items.
func (c *Collector) finalize(id string, o sourceOutcome, now time.Time, keys map[string]bool) (SourceReport, []Evidence) {
	sr := SourceReport{ID: id}
	switch {
	case !o.done:
		sr.Completeness, sr.Detail = CompletenessFailed, detailPanicked
		return sr, nil
	case o.err != nil:
		if errors.Is(o.ctxErr, context.DeadlineExceeded) || errors.Is(o.err, context.DeadlineExceeded) {
			sr.Completeness, sr.Detail = CompletenessTimedOut, detailTimedOut
		} else {
			sr.Completeness, sr.Detail = CompletenessFailed, detailFailed
		}
		c.logger.Warn("incident capture source failed", "source", id, "completeness", sr.Completeness, "error", o.err)
		return sr, nil
	}
	switch o.result.Completeness {
	case CompletenessComplete, CompletenessPartial, CompletenessFailed, CompletenessForbidden, CompletenessTimedOut:
		sr.Completeness, sr.Detail = o.result.Completeness, o.result.Detail
	default:
		sr.Completeness, sr.Detail = CompletenessFailed, detailUnknownState
		return sr, nil
	}

	var oversize, invalid bool
	items := make([]Evidence, 0, len(o.result.Items))
	for _, e := range o.result.Items {
		e.CollectedAt = now
		e.Source.ClusterID = k8s.LocalClusterID
		e.Source.IdentityWeak = e.Source.UID == ""
		if e.Mode == ModeLiveLink {
			e.Payload = nil
		}
		e.PayloadBytes = len(e.Payload)
		e.CaptureKey = CaptureKey(e.EvidenceKind, e.Mode, e.Source, e.discriminator)
		if e.PayloadBytes > c.limits.MaxItemBytes {
			oversize = true
			continue
		}
		row, err := e.Row()
		if err == nil {
			err = store.ValidateEvidenceRow(row)
		}
		if err != nil {
			invalid = true
			c.logger.Warn("incident capture item dropped", "source", id, "kind", e.EvidenceKind, "mode", e.Mode, "error", err)
			continue
		}
		if keys[e.CaptureKey] {
			continue // the same observation twice in one capture
		}
		keys[e.CaptureKey] = true
		items = append(items, e)
	}
	if oversize {
		sr.Completeness, sr.Detail = downgrade(sr.Completeness, sr.Detail, detailOversize)
	}
	if invalid {
		sr.Completeness, sr.Detail = downgrade(sr.Completeness, sr.Detail, detailInvalid)
	}
	sr.Items = len(items)
	return sr, items
}

// downgrade marks a complete source partial and appends a note, keeping
// the detail within the store's bound.
func downgrade(cur Completeness, detail, note string) (Completeness, string) {
	if cur == CompletenessComplete {
		cur = CompletenessPartial
	}
	if detail == "" {
		detail = note
	} else if !strings.Contains(detail, note) {
		detail += "; " + note
	}
	if n := []rune(detail); len(n) > store.EvidenceMaxDetailChars {
		detail = string(n[:store.EvidenceMaxDetailChars])
	}
	return cur, detail
}

// CaptureKey derives the idempotency key of an observation (Q1 P15): the hex
// SHA-256 of the JSON array [kind, mode, cluster, apiGroup, resource,
// namespace, name, uid, discriminator...]. A JSON array keeps every field
// boundary, so shifting bytes between adjacent fields changes the key
// (naive joining would not). The discriminator carries what makes the
// observation new and EXCLUDES capture-time volatility: a diagnostic check
// contributes its id, status, reason and sorted evidence; an object snapshot
// its resourceVersion; a live link nothing; an event list a digest of its
// (uid, count, time) tuples. Two captures seconds apart of an unchanged
// outcome therefore dedupe; a changed outcome does not.
func CaptureKey(kind string, mode EvidenceMode, src SourceRef, discriminator []string) string {
	parts := make([]string, 0, 8+len(discriminator))
	parts = append(parts, kind, string(mode), src.ClusterID, src.APIGroup, src.Resource, src.Namespace, src.Name, src.UID)
	parts = append(parts, discriminator...)
	b, err := json.Marshal(parts)
	if err != nil {
		// A []string always marshals; invalid UTF-8 is coerced, never fatal.
		b = []byte(strings.Join(parts, "\x00"))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Row maps the item to the store row InsertBatch takes. ID and IncidentID
// are left for the store; Redaction is marshalled with encoding/json so the
// store's secretDerived cross-check always sees the exact key.
func (e Evidence) Row() (store.IncidentEvidenceRow, error) {
	red, err := json.Marshal(e.Redaction)
	if err != nil {
		return store.IncidentEvidenceRow{}, fmt.Errorf("marshal redaction: %w", err)
	}
	var payload json.RawMessage
	if e.Mode == ModeSnapshot {
		payload = e.Payload
	}
	return store.IncidentEvidenceRow{
		EvidenceKind:       e.EvidenceKind,
		Mode:               string(e.Mode),
		ClusterID:          e.Source.ClusterID,
		APIGroup:           e.Source.APIGroup,
		Resource:           e.Source.Resource,
		SourceKind:         e.Source.Kind,
		Namespace:          e.Source.Namespace,
		Name:               e.Source.Name,
		SourceUID:          e.Source.UID,
		ResourceVersion:    e.Source.ResourceVersion,
		SecretDerived:      e.Redaction.SecretDerived,
		SourceObservedAt:   e.SourceObservedAt,
		CollectedAt:        e.CollectedAt,
		Completeness:       string(e.Completeness),
		CompletenessDetail: e.CompletenessDetail,
		Redaction:          red,
		Payload:            payload,
		PayloadBytes:       len(payload),
		CaptureKey:         e.CaptureKey,
	}, nil
}

// EvidenceRows maps a report's items for InsertBatch.
func EvidenceRows(items []Evidence) ([]store.IncidentEvidenceRow, error) {
	rows := make([]store.IncidentEvidenceRow, 0, len(items))
	for i, e := range items {
		row, err := e.Row()
		if err != nil {
			return nil, fmt.Errorf("evidence item %d: %w", i, err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// EvidenceFromRow is the inverse of Row for the read path. The row's
// secret_derived column wins over the redaction metadata (the column may be
// stricter, never laxer), and a row without a UID is identity-weak.
func EvidenceFromRow(row store.IncidentEvidenceRow) (Evidence, error) {
	var meta RedactionMeta
	if len(row.Redaction) > 0 {
		if err := json.Unmarshal(row.Redaction, &meta); err != nil {
			return Evidence{}, fmt.Errorf("decode redaction: %w", err)
		}
	}
	meta.SecretDerived = meta.SecretDerived || row.SecretDerived
	e := Evidence{
		EvidenceKind: row.EvidenceKind,
		Mode:         EvidenceMode(row.Mode),
		Source: SourceRef{
			ClusterID:       row.ClusterID,
			APIGroup:        row.APIGroup,
			Resource:        row.Resource,
			Kind:            row.SourceKind,
			Namespace:       row.Namespace,
			Name:            row.Name,
			UID:             row.SourceUID,
			ResourceVersion: row.ResourceVersion,
			IdentityWeak:    row.SourceUID == "",
		},
		SourceObservedAt:   row.SourceObservedAt,
		CollectedAt:        row.CollectedAt,
		Completeness:       Completeness(row.Completeness),
		CompletenessDetail: row.CompletenessDetail,
		Redaction:          meta,
		CaptureKey:         row.CaptureKey,
	}
	if row.ID != uuid.Nil {
		e.ID = row.ID.String()
	}
	if row.IncidentID != uuid.Nil {
		e.IncidentID = row.IncidentID.String()
	}
	if e.Mode == ModeSnapshot && len(row.Payload) > 0 {
		e.Payload = row.Payload
		e.PayloadBytes = len(row.Payload)
	}
	return e, nil
}
