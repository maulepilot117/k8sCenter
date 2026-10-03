package velero

// assurance.go — Release F U33: the backup freshness and schedule-outcome
// evaluator (KTD11, R3, R22).
//
// Evaluate is a pure function: (Observation, policies, now) -> findings. It
// reads no clock, does no network or database I/O and logs nothing, so every
// case is reproducible in a table test; the import list enforces it (see
// TestAssuranceGo_ImportsStayPure). The background collector (U34b) gathers
// the Observation and turns findings into durable exceptions.
//
// Backup freshness is not demonstrated recoverability. Nothing here creates,
// or decides to create, a Restore.

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/kubecenter/kubecenter/internal/store"
)

// Collection is how much of a collection cycle's evidence was read.
type Collection string

const (
	// CollectionOK means every list the evaluator needs was read.
	CollectionOK Collection = "ok"
	// CollectionDegraded means some lists were read and at least one, named
	// in Observation.FailedLists, was not.
	CollectionDegraded Collection = "degraded"
	// CollectionFailed means Velero could not be observed at all: discovery
	// says it is absent, or the read failed.
	CollectionFailed Collection = "failed"
)

// FreshnessOutcome is what a Backup's phase means for the freshness clock.
// It refines BackupOutcomeOf, the dashboard's classification, in one place
// only: a PartiallyFailed backup is "partial" here so a policy's
// TreatPartialAs can decide it, where the dashboard counts it as failed.
// Every FreshnessOutcome maps back onto exactly one BackupOutcome, so the
// evaluator and the schedules page never disagree about a phase.
type FreshnessOutcome string

const (
	OutcomeSuccess  FreshnessOutcome = "success"
	OutcomePartial  FreshnessOutcome = "partial"
	OutcomeFailure  FreshnessOutcome = "failure"
	OutcomeInFlight FreshnessOutcome = "in_flight"
	OutcomeUnknown  FreshnessOutcome = "unknown"
)

// phasePartiallyFailed is Velero's phase for a backup that finished with
// errors on some items.
const phasePartiallyFailed = "PartiallyFailed"

// FreshnessOutcomeOf classifies a Backup status.phase for the freshness
// clock. It is BackupOutcomeOf with PartiallyFailed split out of the
// failures; see FreshnessOutcome.
func FreshnessOutcomeOf(phase string) FreshnessOutcome {
	switch BackupOutcomeOf(phase) {
	case BackupOutcomeSucceeded:
		return OutcomeSuccess
	case BackupOutcomeFailed:
		if phase == phasePartiallyFailed {
			return OutcomePartial
		}
		return OutcomeFailure
	case BackupOutcomeInProgress:
		return OutcomeInFlight
	}
	return OutcomeUnknown
}

// Observation is one collection cycle's evidence. Collection is a required,
// non-pointer field: its zero value is not a known Collection, and Evaluate
// treats anything other than ok or degraded as failed, so a caller that
// forgets to set it gets "unknown", never a claim about backups.
type Observation struct {
	ClusterID string
	// CollectedAt is when the evidence was read. Informational: Evaluate
	// takes its clock from its now argument.
	CollectedAt time.Time
	Collection  Collection
	// FailedLists names the Velero lists that could not be read, by GVR
	// resource ("backups", "schedules", "backupstoragelocations"). A
	// degraded collection with no names is treated as missing everything.
	FailedLists []string
	Status      VeleroStatus
	Backups     []Backup
	Schedules   []Schedule
	Locations   *LocationsResponse
}

// Subject is what an exception is about. UID is the Schedule's metadata.uid
// for a schedule subject and empty for namespace and cluster subjects.
type Subject struct {
	Kind      store.AssuranceScopeKind
	Namespace string
	Name      string
	UID       string
}

// Detail is the evidence attached to an exception, stored in the privileged
// detail JSONB. Fields marked privileged may carry controller text or
// storage names and are admin-only when read back (U35's projection); the
// others are safe to show anyone who may see the subject.
type Detail struct {
	LastOutcome      FreshnessOutcome `json:"lastOutcome"`
	LastSuccessAt    *time.Time       `json:"lastSuccessAt,omitempty"`
	ExpectedRunAt    *time.Time       `json:"expectedRunAt,omitempty"`
	ExpectedRunKnown bool             `json:"expectedRunKnown"`
	CronParseError   string           `json:"cronParseError,omitempty"`
	SuppressedBy     string           `json:"suppressedBy,omitempty"`
	StorageLocation  string           `json:"storageLocation,omitempty"` // privileged
	BSLMessage       string           `json:"bslMessage,omitempty"`      // privileged
	FailureReason    string           `json:"failureReason,omitempty"`   // privileged
}

// SuppressedByPaused is Detail.SuppressedBy for a condition held open
// because its schedule is paused.
const SuppressedByPaused = "paused"

// Finding is one condition that holds for one subject under one policy.
type Finding struct {
	Subject   Subject
	PolicyID  uuid.UUID
	Condition store.AssuranceCondition
	Severity  string // store.AssuranceSeverity*
	Detail    Detail
	// Hold marks a condition that is still true but deliberately
	// suppressed: overdue or never_run on a paused schedule. Pausing fixes
	// nothing, so the reconciler keeps an already-open exception for it
	// open (an observation, not a resolution), but it must not open a new
	// one: calling a deliberately paused schedule overdue would be false.
	Hold bool
}

const (
	// expectedRunLookback caps how far back ExpectedRunsSince walks; it
	// covers monthly expressions with margin.
	expectedRunLookback = 35 * 24 * time.Hour
	// maxExpectedRunSteps bounds the walk, so "@every 1s" over the lookback
	// is "not computable" rather than three million iterations.
	maxExpectedRunSteps = 2000
)

// errCronZoneOnly rejects a CRON_TZ=/TZ= prefix with no schedule after it.
// robfig/cron v3.0.1 slices the spec at its first space without checking
// there is one, and panics on such input.
var errCronZoneOnly = errors.New("time zone prefix is not followed by a schedule")

// parseCron parses a Velero schedule expression: five fields or a
// descriptor (@daily, @every 1h), with an optional CRON_TZ=/TZ= prefix. It
// is the one parse path for schedules, shared with computeNextRun and the
// create/update validation, so none of them can reach the robfig panic.
func parseCron(expr string) (cron.Schedule, error) {
	if (strings.HasPrefix(expr, "TZ=") || strings.HasPrefix(expr, "CRON_TZ=")) && !strings.Contains(expr, " ") {
		return nil, errCronZoneOnly
	}
	return cron.ParseStandard(expr)
}

// ExpectedRunsSince walks cronExpr forward from anchor and returns the latest
// expected fire time after anchor and at or before now; a zero time with
// known=true means no run was expected in that window. known=false means the
// expression is unparseable or never fires, the walk exceeded maxSteps, or
// anchor is older than the 35-day lookback cap. The caller must then fall
// back to the plain max-age rule and say the expected run is not computable.
//
// Without a CRON_TZ=/TZ= prefix the expression is evaluated in UTC, Velero's
// container default, never in this process's local zone, so the result
// depends only on the arguments. This walk is not computeNextRun: that
// display helper clamps its start to now and so can never see a missed run.
func ExpectedRunsSince(cronExpr string, anchor, now time.Time, maxSteps int) (last time.Time, known bool) {
	sched, err := parseCron(cronExpr)
	if err != nil {
		return time.Time{}, false
	}
	return expectedRunsSince(sched, anchor, now, maxSteps)
}

func expectedRunsSince(sched cron.Schedule, anchor, now time.Time, maxSteps int) (time.Time, bool) {
	if now.Sub(anchor) > expectedRunLookback {
		return time.Time{}, false
	}
	// The zone the expression's wall-clock fields are read in: its
	// CRON_TZ=/TZ= zone, else UTC (the walk runs in UTC, and robfig reads
	// a zoneless expression in the zone of the time it is given).
	loc := time.UTC
	if spec, ok := sched.(*cron.SpecSchedule); ok && spec.Location != time.Local {
		loc = spec.Location
	}
	var last time.Time
	t := anchor.UTC()
	for range maxSteps {
		next := sched.Next(t)
		if next.IsZero() {
			// robfig returns zero for an expression with no fire time in
			// the next five years (e.g. 30 February): not computable.
			return time.Time{}, false
		}
		if next.After(now) {
			return last, true
		}
		t = next
		if !repeatedWallClock(next, loc) {
			last = next
		}
	}
	return time.Time{}, false
}

// repeatedWallClock reports whether t's wall-clock time in loc already
// occurred an hour earlier: the second pass through the repeated hour of a
// fall-back transition. robfig fires a SpecSchedule on both passes; a
// "01:30 daily" job means once a day, so the walk counts only the first.
// (A spring-forward day has no 02:30, and robfig already skips it.)
func repeatedWallClock(t time.Time, loc *time.Location) bool {
	const wall = "2006-01-02 15:04"
	return t.In(loc).Format(wall) == t.Add(-time.Hour).In(loc).Format(wall)
}

// Evaluate returns every condition that holds under the enabled policies for
// obs's cluster, sorted by subject and condition. No enabled policy means no
// findings: installing k8sCenter never implies a default policy.
//
// The hard rule: when collection is not ok, no subject whose evidence is
// missing is reported as never_run, overdue, failed, partially_failed,
// paused or location_unavailable; it is collection_unknown. A failed
// collection yields exactly one cluster-scoped collection_unknown, so a
// Velero outage is one exception rather than an alert storm, and is never
// read as "no backups exist".
func Evaluate(obs Observation, policies []store.BackupAssurancePolicy, now time.Time) []Finding {
	active := activePolicies(obs.ClusterID, policies)
	if len(active) == 0 {
		return nil
	}
	if (obs.Collection != CollectionOK && obs.Collection != CollectionDegraded) || !obs.Status.Detected {
		return []Finding{clusterCollectionUnknown(active)}
	}

	e := evaluator{obs: obs, now: now}
	var out []Finding
	for _, p := range active {
		out = append(out, e.policy(p)...)
	}
	return normalizeFindings(out)
}

// activePolicies is the enabled, well-formed policies for clusterID, in a
// deterministic order: cluster scope first, then by id.
func activePolicies(clusterID string, policies []store.BackupAssurancePolicy) []store.BackupAssurancePolicy {
	var active []store.BackupAssurancePolicy
	for _, p := range policies {
		if p.Enabled && p.ClusterID == clusterID && p.ScopeKind.Valid() {
			active = append(active, p)
		}
	}
	slices.SortFunc(active, func(a, b store.BackupAssurancePolicy) int {
		if ac, bc := a.ScopeKind == store.ScopeCluster, b.ScopeKind == store.ScopeCluster; ac != bc {
			if ac {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID.String(), b.ID.String())
	})
	return active
}

// clusterCollectionUnknown is the single finding for a failed collection,
// attributed to the first active policy (a cluster-scope one when present).
func clusterCollectionUnknown(active []store.BackupAssurancePolicy) Finding {
	return Finding{
		Subject:   Subject{Kind: store.ScopeCluster},
		PolicyID:  active[0].ID,
		Condition: store.ConditionCollectionUnknown,
		Severity:  store.AssuranceSeverityWarning,
		Detail:    Detail{LastOutcome: OutcomeUnknown},
	}
}

type evaluator struct {
	obs Observation
	now time.Time
}

// missing reports whether the list for resource was not read.
func (e *evaluator) missing(resource string) bool {
	if slices.Contains(e.obs.FailedLists, resource) {
		return true
	}
	return e.obs.Collection == CollectionDegraded && len(e.obs.FailedLists) == 0
}

func (e *evaluator) policy(p store.BackupAssurancePolicy) []Finding {
	switch p.ScopeKind {
	case store.ScopeSchedule:
		return e.schedule(p)
	case store.ScopeNamespace:
		subj := Subject{Kind: store.ScopeNamespace, Namespace: p.ScopeNamespace}
		if e.missing(BackupGVR.Resource) {
			return []Finding{collectionUnknown(subj, p)}
		}
		return e.subject(p, subj, e.backupsWhere(func(b *Backup) bool { return coversNamespace(b, p.ScopeNamespace) }), nil)
	case store.ScopeCluster:
		subj := Subject{Kind: store.ScopeCluster}
		if e.missing(BackupGVR.Resource) {
			return []Finding{collectionUnknown(subj, p)}
		}
		return e.subject(p, subj, e.backupsWhere(coversCluster), nil)
	}
	return nil
}

func (e *evaluator) schedule(p store.BackupAssurancePolicy) []Finding {
	if e.missing(ScheduleGVR.Resource) {
		// Without the schedule list the UID cannot be known.
		return []Finding{collectionUnknown(Subject{Kind: store.ScopeSchedule, Namespace: p.ScopeNamespace, Name: p.ScopeName}, p)}
	}
	var s *Schedule
	for i := range e.obs.Schedules {
		if c := &e.obs.Schedules[i]; c.Namespace == p.ScopeNamespace && c.Name == p.ScopeName {
			s = c
			break
		}
	}
	if s == nil {
		// The subject is absent; the reconciler resolves any exception it
		// had as subject_absent (Design Decisions §4).
		return nil
	}
	subj := Subject{Kind: store.ScopeSchedule, Namespace: s.Namespace, Name: s.Name, UID: s.UID}
	if e.missing(BackupGVR.Resource) || e.missing(BackupStorageLocationGVR.Resource) {
		return []Finding{collectionUnknown(subj, p)}
	}
	label := scheduleLabelValue(s.Name)
	runs := e.backupsWhere(func(b *Backup) bool { return b.Namespace == s.Namespace && b.ScheduleName == label })
	return e.subject(p, subj, runs, s)
}

// backupsWhere is the observed backups that keep returns true for.
func (e *evaluator) backupsWhere(keep func(*Backup) bool) []*Backup {
	var out []*Backup
	for i := range e.obs.Backups {
		if b := &e.obs.Backups[i]; keep(b) {
			out = append(out, b)
		}
	}
	return out
}

// coversNamespace reports whether b backs up ns: an empty or "*" include
// list means every namespace, and the exclude list wins.
func coversNamespace(b *Backup, ns string) bool {
	if slices.Contains(b.ExcludedNamespaces, ns) {
		return false
	}
	return coversCluster(b) || slices.Contains(b.IncludedNamespaces, ns)
}

// coversCluster reports whether b is a whole-cluster backup: an empty or
// "*" include list.
func coversCluster(b *Backup) bool {
	return len(b.IncludedNamespaces) == 0 || slices.Contains(b.IncludedNamespaces, "*")
}

// runTimestamp is the time a successful run proves freshness at: its
// completion, or its start when Velero recorded no completion.
func runTimestamp(b *Backup) *time.Time {
	if b.CompletionTime != nil {
		return b.CompletionTime
	}
	return b.StartTime
}

// effectiveOutcome is b's outcome for the clock. A success with no usable
// timestamp is unknown: a backup that succeeded at no known time is not
// evidence of freshness.
func effectiveOutcome(b *Backup) FreshnessOutcome {
	o := FreshnessOutcomeOf(b.Phase)
	if (o == OutcomeSuccess || o == OutcomePartial) && runTimestamp(b) == nil {
		return OutcomeUnknown
	}
	return o
}

// subject evaluates one subject's runs. s is the subject's Schedule, nil for
// namespace and cluster scope.
func (e *evaluator) subject(p store.BackupAssurancePolicy, subj Subject, runs []*Backup, s *Schedule) []Finding {
	partialIsSuccess := p.TreatPartialAs == store.AssuranceTreatPartialAsSuccess

	var newest *Backup // newest run with a terminal outcome
	var newestOutcome FreshnessOutcome
	var lastSuccess *time.Time
	inFlight, known := false, false
	for _, b := range runs {
		o := effectiveOutcome(b)
		switch o {
		case OutcomeUnknown:
			continue
		case OutcomeInFlight:
			// Never a success and never a failure: it neither resets the
			// clock nor suppresses an overdue.
			inFlight, known = true, true
			continue
		}
		known = true
		if newest == nil || newerRun(b, newest) {
			newest, newestOutcome = b, o
		}
		if o == OutcomeSuccess || o == OutcomePartial && partialIsSuccess {
			if ts := runTimestamp(b); lastSuccess == nil || ts.After(*lastSuccess) {
				lastSuccess = ts
			}
		}
	}

	base := Detail{LastOutcome: OutcomeUnknown, LastSuccessAt: lastSuccess}
	switch {
	case newest != nil:
		base.LastOutcome = newestOutcome
	case inFlight:
		base.LastOutcome = OutcomeInFlight
	}

	var out []Finding
	add := func(c store.AssuranceCondition, severity string, d Detail) *Finding {
		out = append(out, Finding{Subject: subj, PolicyID: p.ID, Condition: c, Severity: severity, Detail: d})
		return &out[len(out)-1]
	}

	if s != nil && s.Paused && p.AlertOnPaused {
		add(store.ConditionPaused, store.AssuranceSeverityWarning, base)
	}
	if s != nil {
		out = append(out, e.location(p, subj, s, base)...)
	}

	if len(runs) > 0 && !known {
		// The only evidence is runs whose outcome cannot be read: the
		// subject's freshness is unknown, not overdue.
		add(store.ConditionCollectionUnknown, store.AssuranceSeverityWarning, base)
		return out
	}

	switch newestOutcome {
	case OutcomeFailure:
		add(store.ConditionFailed, store.AssuranceSeverityCritical, base)
	case OutcomePartial:
		if !partialIsSuccess {
			add(store.ConditionPartiallyFailed, store.AssuranceSeverityWarning, base)
		}
	}

	// The age clock runs from the last success, or, for a schedule that has
	// never succeeded, from its creation.
	ref := lastSuccess
	if ref == nil && s != nil && !s.created.IsZero() {
		ref = &s.created
	}
	d := base
	missedRun := true
	if s != nil && s.Schedule != "" {
		missedRun = e.expectedRun(p, s, ref, &d)
	}
	// max_age + grace is the floor: the expected run is advisory and never
	// the sole basis for overdue. It only spares a subject whose schedule
	// expected no run since the reference point (a weekly schedule under a
	// shorter max_age).
	if (ref == nil || e.now.Sub(*ref) > p.MaxAge+p.Grace) && missedRun {
		c := store.ConditionOverdue
		if s != nil && s.LastBackup == nil && len(runs) == 0 {
			c = store.ConditionNeverRun
		}
		f := add(c, store.AssuranceSeverityWarning, d)
		if s != nil && s.Paused {
			f.Hold = true
			f.Detail.SuppressedBy = SuppressedByPaused
		}
	}
	return out
}

// expectedRun fills d's expected-run fields for s and reports whether a run
// expected since ref, older than the grace period, has been missed. When the
// expected run is not computable it reports true, leaving max_age + grace as
// the only rule.
func (e *evaluator) expectedRun(p store.BackupAssurancePolicy, s *Schedule, ref *time.Time, d *Detail) bool {
	sched, err := parseCron(s.Schedule)
	if err != nil {
		d.CronParseError = err.Error()
		return true
	}
	anchor := e.now.Add(-expectedRunLookback)
	if ref != nil && ref.After(anchor) {
		anchor = *ref
	}
	if s.created.After(anchor) {
		anchor = s.created
	}
	last, known := expectedRunsSince(sched, anchor, e.now, maxExpectedRunSteps)
	d.ExpectedRunKnown = known
	if !known {
		return true
	}
	if last.IsZero() {
		return false
	}
	d.ExpectedRunAt = &last
	return e.now.Sub(last) > p.Grace
}

// location reports location_unavailable when s's target storage location is
// missing or not Available. An empty target means Velero's default location
// in the schedule's namespace. The BSL's controller message goes only into
// the privileged Detail fields.
func (e *evaluator) location(p store.BackupAssurancePolicy, subj Subject, s *Schedule, base Detail) []Finding {
	var bsl *BackupStorageLocation
	if e.obs.Locations != nil {
		for i := range e.obs.Locations.BackupStorageLocations {
			l := &e.obs.Locations.BackupStorageLocations[i]
			if l.Namespace != s.Namespace {
				continue
			}
			if s.StorageLocation != "" && l.Name == s.StorageLocation ||
				s.StorageLocation == "" && l.Default && (bsl == nil || l.Name < bsl.Name) {
				bsl = l
			}
		}
	}
	if bsl != nil && bsl.Phase == "Available" {
		return nil
	}
	d := base
	d.StorageLocation = s.StorageLocation
	if bsl != nil {
		d.StorageLocation = bsl.Name
		d.BSLMessage = bsl.Message
	}
	return []Finding{{Subject: subj, PolicyID: p.ID, Condition: store.ConditionLocationUnavailable, Severity: store.AssuranceSeverityCritical, Detail: d}}
}

func collectionUnknown(subj Subject, p store.BackupAssurancePolicy) Finding {
	return Finding{
		Subject:   subj,
		PolicyID:  p.ID,
		Condition: store.ConditionCollectionUnknown,
		Severity:  store.AssuranceSeverityWarning,
		Detail:    Detail{LastOutcome: OutcomeUnknown},
	}
}

// normalizeFindings sorts findings by exception identity and drops repeats
// of an identity, keeping the first: the exception store admits one open
// row per identity, so the reconciler must see each at most once.
func normalizeFindings(fs []Finding) []Finding {
	slices.SortStableFunc(fs, func(a, b Finding) int {
		for _, c := range [...]int{
			strings.Compare(string(a.Subject.Kind), string(b.Subject.Kind)),
			strings.Compare(a.Subject.Namespace, b.Subject.Namespace),
			strings.Compare(a.Subject.Name, b.Subject.Name),
			strings.Compare(a.Subject.UID, b.Subject.UID),
			strings.Compare(string(a.Condition), string(b.Condition)),
		} {
			if c != 0 {
				return c
			}
		}
		return strings.Compare(a.PolicyID.String(), b.PolicyID.String())
	})
	return slices.CompactFunc(fs, func(a, b Finding) bool {
		return a.Subject == b.Subject && a.Condition == b.Condition
	})
}
