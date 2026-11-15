package rubrik

// This file is the normalized model the rest of the feature reads. RSC
// protects Kubernetes workloads through two families of objects:
//
//   - per-namespace objects (K8sNamespace, listed by k8sNamespaces under an
//     RSC-native K8sCluster), and
//   - protection sets (KubernetesProtectionSet, listed by
//     kubernetesProtectionSets under a CDM KubernetesCluster).
//
// Both decode into Object (KTD4). The decoders in operations.go are pure
// functions over the GraphQL answer's data so they can be fuzzed; every
// rejection carries a fixed text and never quotes the answer.

import (
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Family names the RSC object family an Object, a cluster or a job handle
// belongs to.
type Family string

const (
	// FamilyNamespace is RSC's per-namespace object (K8sNamespace) and the
	// RSC-native K8sCluster it lives under.
	FamilyNamespace Family = "namespace"
	// FamilyProtectionSet is a CDM protection set (KubernetesProtectionSet)
	// and the CDM KubernetesCluster it lives under.
	FamilyProtectionSet Family = "protectionSet"
)

// Cluster is a Kubernetes cluster RSC knows about. The two families list
// their clusters separately and with different ids, so a mapping must say
// which family its RSC cluster id belongs to.
type Cluster struct {
	Family Family `json:"family"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	// Status is RSC's connection status as RSC spells it (for example
	// STATUS_CONNECTED for a K8sCluster). Empty when RSC gave none.
	Status string `json:"status,omitempty"`
}

// SLARef names an SLA domain.
type SLARef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// unprotectedSLAIDs are the pseudo SLA domain ids RSC reports for an object
// with no protecting SLA: no assignment, or an explicit Do Not Protect. They
// are an RSC convention, not values the schema enumerates.
var unprotectedSLAIDs = []string{"", "UNPROTECTED", "DO_NOT_PROTECT", "DONOTPROTECT", "NO_SLA"}

// Protects reports whether the SLA domain protects an object, that is, it is
// a real SLA domain and not one of RSC's unprotected pseudo domains.
func (s SLARef) Protects() bool {
	id := strings.TrimSpace(s.ID)
	for _, u := range unprotectedSLAIDs {
		if strings.EqualFold(id, u) {
			return false
		}
	}
	return true
}

// Coverage is the set of Kubernetes namespaces an object protects.
type Coverage struct {
	// All means the object protects every namespace in its cluster.
	All bool `json:"all,omitempty"`
	// Namespaces are the namespaces it protects when All is false.
	Namespaces []string `json:"namespaces,omitempty"`
	// Unresolved means RSC does not resolve the coverage to a list of
	// namespaces (patterns, a label selector, or an unknown set type). An
	// unresolved object protects no namespace for counting purposes and is
	// admin-only (R7).
	Unresolved bool `json:"unresolved,omitempty"`
}

// Covers reports whether the coverage includes namespace ns.
func (c Coverage) Covers(ns string) bool {
	if c.Unresolved || ns == "" {
		return false
	}
	return c.All || slices.Contains(c.Namespaces, ns)
}

// MultiNamespace reports whether the object may protect more than one
// namespace. Such an object is admin-only to act on (R7).
func (c Coverage) MultiNamespace() bool {
	return c.All || c.Unresolved || len(c.Namespaces) > 1
}

// Object is one RSC object that protects Kubernetes resources, from either
// family.
type Object struct {
	Family Family `json:"family"`
	ID     string `json:"id"`
	// Name is the namespace name for a per-namespace object and the set name
	// for a protection set.
	Name string `json:"name"`
	// SetType is a protection set's type as RSC spells it (rsType, for
	// example "namespace" or "cluster"). Empty for a per-namespace object.
	SetType  string   `json:"setType,omitempty"`
	Coverage Coverage `json:"coverage"`
	// ConfiguredSLA is the SLA domain assigned to the object itself;
	// EffectiveSLA is the one in force, which may be inherited.
	ConfiguredSLA SLARef `json:"configuredSla"`
	EffectiveSLA  SLARef `json:"effectiveSla"`
	// SLAAssignment is RSC's assignment type (Derived, Direct, Unassigned).
	SLAAssignment string `json:"slaAssignment,omitempty"`
	// NewestSnapshot is the time of the object's newest snapshot, nil when
	// it has none.
	NewestSnapshot *time.Time `json:"newestSnapshot,omitempty"`
	// Relic means RSC no longer sees the source object. A relic never counts
	// as protection and is listed for admins only (KTD4).
	Relic bool `json:"relic,omitempty"`
	// MayHoldClusterScoped means the object's snapshots may contain
	// cluster-scoped resources, which makes a restore admin-only (KTD9).
	// It fails closed: true whenever RSC's answer cannot rule it out.
	MayHoldClusterScoped bool `json:"mayHoldClusterScoped"`
}

// Protecting reports whether the object counts as protection: it is not a
// relic and its effective SLA domain protects it.
func (o Object) Protecting() bool {
	return !o.Relic && o.EffectiveSLA.Protects()
}

// NamespaceScoped reports whether the object protects exactly one resolved
// namespace: a per-namespace object, or a protection set of type namespace.
// Only such objects can be the target of a non-admin action.
func (o Object) NamespaceScoped() bool {
	if o.Coverage.MultiNamespace() || len(o.Coverage.Namespaces) != 1 {
		return false
	}
	return o.Family == FamilyNamespace || strings.EqualFold(o.SetType, setTypeNamespace)
}

// SnapshotSLA returns the SLA domain id an on-demand snapshot of the object
// uses when the user names none: its effective SLA (KTD8). ok is false when
// the object has no protecting SLA, so the user must pick one.
func (o Object) SnapshotSLA() (id string, ok bool) {
	if !o.EffectiveSLA.Protects() {
		return "", false
	}
	return o.EffectiveSLA.ID, true
}

// Snapshot is one snapshot of an Object.
type Snapshot struct {
	ID       string     `json:"id"`
	Time     time.Time  `json:"time"`
	Expires  *time.Time `json:"expires,omitempty"`
	Expired  bool       `json:"expired,omitempty"`
	OnDemand bool       `json:"onDemand,omitempty"`
	// Corrupted and Quarantined are RSC's integrity flags; a restore from
	// such a snapshot should not be offered.
	Corrupted   bool `json:"corrupted,omitempty"`
	Quarantined bool `json:"quarantined,omitempty"`
}

// JobStatus is the normalized state of an RSC job, from an activity series
// or a taskchain.
type JobStatus string

const (
	JobRunning   JobStatus = "running"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
	JobPartial   JobStatus = "partial"
	JobCanceled  JobStatus = "canceled"
	// JobUnknown is any state RSC reports that the feature does not map,
	// including informational activity entries.
	JobUnknown JobStatus = "unknown"
)

// Terminal reports whether the job has finished.
func (s JobStatus) Terminal() bool {
	switch s {
	case JobSucceeded, JobFailed, JobPartial, JobCanceled:
		return true
	}
	return false
}

// Job is one RSC activity series: a snapshot, restore or other job on an
// object, as shown in job history (R12).
type Job struct {
	ID       string `json:"id"`
	ObjectID string `json:"objectId"`
	// Type is RSC's activity type as RSC spells it (for example Backup or
	// Recovery).
	Type        string     `json:"type"`
	Status      JobStatus  `json:"status"`
	StartTime   *time.Time `json:"startTime,omitempty"`
	LastUpdated *time.Time `json:"lastUpdated,omitempty"`
	// Message is RSC's latest activity message; FailureReason the reason it
	// gives for a failure. Both are tenant text, shown as is.
	Message       string `json:"message,omitempty"`
	FailureReason string `json:"failureReason,omitempty"`
}

// JobHandle identifies a job a mutation started, so its state can be
// followed (KTD7). A per-namespace mutation returns a taskchain; a
// protection-set mutation returns an async request.
type JobHandle struct {
	Family Family `json:"family"`
	// TaskchainID and JobID are set for a per-namespace mutation.
	TaskchainID string `json:"taskchainId,omitempty"`
	JobID       int64  `json:"jobId,omitempty"`
	// RequestID and RequestStatus are set for a protection-set mutation.
	RequestID     string `json:"requestId,omitempty"`
	RequestStatus string `json:"requestStatus,omitempty"`
}

// TaskchainState is a followed taskchain's normalized state.
type TaskchainState struct {
	Status JobStatus `json:"status"`
	// Error is RSC's error text for a failed taskchain, shown as is.
	Error string `json:"error,omitempty"`
}

// SLADomain is an SLA domain the SLA dialogs offer.
type SLADomain struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Frequency is the interval of the domain's most frequent snapshot
	// schedule, 0 when the domain has no schedule RSC reported (KTD5).
	Frequency time.Duration `json:"frequency"`
}

// Compliance is an SLA compliance judgment.
type Compliance string

const (
	ComplianceUnknown         Compliance = "unknown"
	ComplianceInCompliance    Compliance = "in_compliance"
	ComplianceOutOfCompliance Compliance = "out_of_compliance"
)

// complianceRank orders judgments from best to worst.
func complianceRank(c Compliance) int {
	switch c {
	case ComplianceInCompliance:
		return 0
	case ComplianceOutOfCompliance:
		return 2
	}
	return 1
}

// WorstCompliance returns the worse of two judgments: out of compliance
// beats unknown, which beats in compliance.
func WorstCompliance(a, b Compliance) Compliance {
	if complianceRank(b) > complianceRank(a) {
		return b
	}
	return a
}

// EvaluateCompliance judges one instant (KTD5): in compliance while the
// newest snapshot is younger than 1.5 times the SLA domain's most frequent
// schedule interval, out of compliance otherwise or when there is no
// snapshot, and unknown when the interval is not known. It has no memory;
// the poller owns the rule that an object must miss twice before it is
// reported out of compliance.
func EvaluateCompliance(newest *time.Time, frequency time.Duration, now time.Time) Compliance {
	if frequency <= 0 {
		return ComplianceUnknown
	}
	if newest == nil {
		return ComplianceOutOfCompliance
	}
	limit := frequency + frequency/2
	if limit < frequency { // overflow
		limit = math.MaxInt64
	}
	if now.Sub(*newest) < limit {
		return ComplianceInCompliance
	}
	return ComplianceOutOfCompliance
}

// NamespaceSummary is one namespace's protection, assembled from the objects
// of its cluster (KTD4, R11).
type NamespaceSummary struct {
	Namespace string `json:"namespace"`
	// Protected is true when a current, protecting object covers the
	// namespace.
	Protected bool `json:"protected"`
	// Compliance is the worst judgment over the protecting objects, unknown
	// when none is protecting.
	Compliance Compliance `json:"compliance"`
	// LastSnapshot is the newest snapshot time over the current objects
	// covering the namespace.
	LastSnapshot *time.Time `json:"lastSnapshot,omitempty"`
	// ObjectIDs are the current objects covering the namespace, protecting
	// or not, in the order they were given.
	ObjectIDs []string `json:"objectIds"`
	// ActionTargets are the objects an action on this namespace may target:
	// the per-namespace object when there is one, otherwise every
	// namespace-scoped protection set. With more than one the user picks
	// (R15). Never both kinds.
	ActionTargets []string `json:"actionTargets"`
}

// SummarizeNamespace assembles namespace ns's summary from its cluster's
// objects. Relics never count; callers drop any other object they class as
// earlier than the namespace (KTD3) before calling. compliance gives each
// protecting object's judgment, which the API reads from the poller's
// persisted conditions.
func SummarizeNamespace(ns string, objects []Object, compliance func(Object) Compliance) NamespaceSummary {
	sum := NamespaceSummary{
		Namespace:     ns,
		Compliance:    ComplianceUnknown,
		ObjectIDs:     []string{},
		ActionTargets: []string{},
	}
	var perNamespace, sets []string
	judged := false
	for _, o := range objects {
		if o.Relic || !o.Coverage.Covers(ns) {
			continue
		}
		sum.ObjectIDs = append(sum.ObjectIDs, o.ID)
		if o.NewestSnapshot != nil && (sum.LastSnapshot == nil || o.NewestSnapshot.After(*sum.LastSnapshot)) {
			t := *o.NewestSnapshot
			sum.LastSnapshot = &t
		}
		if o.Protecting() {
			sum.Protected = true
			c := ComplianceUnknown
			if compliance != nil {
				c = compliance(o)
			}
			if !judged {
				sum.Compliance, judged = c, true
			} else {
				sum.Compliance = WorstCompliance(sum.Compliance, c)
			}
		}
		if o.NamespaceScoped() {
			if o.Family == FamilyNamespace {
				perNamespace = append(perNamespace, o.ID)
			} else {
				sets = append(sets, o.ID)
			}
		}
	}
	if len(perNamespace) > 0 {
		sum.ActionTargets = append(sum.ActionTargets, perNamespace...)
	} else {
		sum.ActionTargets = append(sum.ActionTargets, sets...)
	}
	return sum
}

// Protection-set types the model distinguishes. RSC reports rsType as a
// free string; any other value is treated as unresolved coverage that may
// hold cluster-scoped resources.
const (
	setTypeNamespace = "namespace"
	setTypeCluster   = "cluster"
)

// clusterScopedKinds matches a cluster-scoped Kubernetes kind named in a
// protection set's definition. The match is case-sensitive on the kind's
// PascalCase name, so lowercase field names ("namespace") do not match. A
// false positive only makes a restore admin-only.
var clusterScopedKinds = regexp.MustCompile(`\b(Namespace|Node|PersistentVolume|StorageClass|ClusterRole|ClusterRoleBinding|CustomResourceDefinition|PriorityClass|IngressClass|RuntimeClass|MutatingWebhookConfiguration|ValidatingWebhookConfiguration|ValidatingAdmissionPolicy|ValidatingAdmissionPolicyBinding|APIService|CSIDriver|CSINode|VolumeSnapshotClass|ClusterIssuer|ClusterSecretStore|ClusterExternalSecret)\b`)

// setMayHoldClusterScoped applies KTD9 to a protection set: every set not of
// type namespace may hold cluster-scoped resources, and so does a namespace
// set whose definition names a cluster-scoped kind or which captures custom
// resource dependencies (custom resource definitions are cluster-scoped).
func setMayHoldClusterScoped(setType, definition, crDependencies string) bool {
	if !strings.EqualFold(strings.TrimSpace(setType), setTypeNamespace) {
		return true
	}
	if strings.TrimSpace(crDependencies) != "" {
		return true
	}
	return clusterScopedKinds.MatchString(definition)
}

// setCoverage resolves a protection set's namespaces. A namespace set covers
// its namespace. A cluster set with no namespace patterns and no label
// selector covers every namespace; with any of them RSC does not resolve the
// list, so coverage is unresolved. Any other type is unresolved.
func setCoverage(setType, namespace, include, exclude, labelSelector string) Coverage {
	switch strings.ToLower(strings.TrimSpace(setType)) {
	case setTypeNamespace:
		if ns := strings.TrimSpace(namespace); ns != "" {
			return Coverage{Namespaces: []string{ns}}
		}
	case setTypeCluster:
		if strings.TrimSpace(include) == "" && strings.TrimSpace(exclude) == "" && strings.TrimSpace(labelSelector) == "" {
			return Coverage{All: true}
		}
	}
	return Coverage{Unresolved: true}
}
