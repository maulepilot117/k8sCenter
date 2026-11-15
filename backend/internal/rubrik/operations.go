package rubrik

// This file holds every GraphQL operation the feature sends to RSC and the
// decoders that turn each answer into the model in model.go.
//
// Each operation is a constant document checked against Rubrik's published
// schema (rubrik-powershell-sdk docs/graphql/schema-public.graphql, version
// header v20261102-37). Answers are read as raw JSON and decoded by pure
// functions, so the decoders can be fuzzed without a tenant. A decoder
// rejection is ErrUnreadable with a fixed text: it never quotes the answer.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/util/validation"
)

// ErrRejected means RSC answered a mutation and said outright that it did
// not apply it (assignSla success false). Nothing was changed.
//
// A mutation reply that does not name exactly one job is ErrUnreadable
// instead: RSC may have started a job the reply cannot identify, so the
// outcome is indeterminate and the request must not be resent.
var ErrRejected = errors.New("rubrik: request rejected by rsc")

const (
	// pageSize is the page size every list operation asks for.
	pageSize = 100
	// maxPages bounds a paged list. A list still not finished after this
	// many pages (a cursor that cycles, or a tenant far larger than any
	// cluster) is an unreadable answer, never a silently short list.
	maxPages = 100
	// DefaultSnapshotLimit is how many snapshots a snapshot list returns when
	// the caller gives no limit; MaxSnapshotLimit caps any limit.
	DefaultSnapshotLimit = 200
	MaxSnapshotLimit     = 2000
)

// querier is the part of Client the operations use.
type querier interface {
	Query(ctx context.Context, req Request, out any) error
}

// Operations sends the feature's GraphQL operations through a Client.
type Operations struct {
	q querier
}

// NewOperations returns the operations over c.
func NewOperations(c *Client) *Operations {
	return &Operations{q: c}
}

// unreadable returns ErrUnreadable with a fixed text.
func unreadable(what string) error {
	return fmt.Errorf("%w: %s", ErrUnreadable, what)
}

// invalid returns ErrInvalidRequest with a fixed text naming the argument.
func invalid(what string) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, what)
}

// checkNodeCluster binds one node of a per-cluster list to the cluster the
// list asked for. The list's cluster argument is the only thing keeping
// another cluster's objects out of the answer, and callers decide visibility
// by namespace name, so a same-named namespace of another cluster must never
// pass. A node with no cluster id, or a different one, fails the whole list:
// dropping the node would hand back a silently short list instead.
func checkNodeCluster(got, want, what string) error {
	if got == "" {
		return unreadable(what + " without a cluster id")
	}
	if !strings.EqualFold(got, want) {
		return unreadable(what + " of another cluster")
	}
	return nil
}

// validUUID reports whether s is a canonical RSC UUID.
func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	_, err := uuid.Parse(s)
	return err == nil
}

// ---------------------------------------------------------------------------
// Paging

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

// collectPages runs fetch from the first page until the answer says there is
// no next page or limit items are collected (limit <= 0 means no limit). It
// refuses a page that claims a next page without a cursor, a cursor seen
// before, and a list longer than maxPages pages.
func collectPages[T any](ctx context.Context, limit int, fetch func(ctx context.Context, after string) ([]T, pageInfo, error)) ([]T, error) {
	var out []T
	seen := map[string]bool{}
	after := ""
	for page := 0; page < maxPages; page++ {
		items, info, err := fetch(ctx, after)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if limit > 0 && len(out) >= limit {
			return out[:limit], nil
		}
		if !info.HasNextPage {
			return out, nil
		}
		if info.EndCursor == "" {
			return nil, unreadable("next page has no cursor")
		}
		if seen[info.EndCursor] {
			return nil, unreadable("page cursor repeats")
		}
		seen[info.EndCursor] = true
		after = info.EndCursor
	}
	return nil, unreadable(fmt.Sprintf("list longer than %d pages", maxPages))
}

// pageVars returns the paging variables for one page.
func pageVars(vars map[string]any, after string) map[string]any {
	vars["first"] = pageSize
	if after != "" {
		vars["after"] = after
	}
	return vars
}

// query sends doc and returns the answer's data unparsed.
func (o *Operations) query(ctx context.Context, doc string, vars map[string]any) ([]byte, error) {
	var raw json.RawMessage
	if err := o.q.Query(ctx, Request{Query: doc, Variables: vars}, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// ---------------------------------------------------------------------------
// Wire shapes shared by the decoders

type wireSLA struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (w *wireSLA) ref() SLARef {
	if w == nil {
		return SLARef{}
	}
	return SLARef{ID: w.ID, Name: w.Name}
}

type wireSnapshotRef struct {
	Date *time.Time `json:"date"`
}

func (w *wireSnapshotRef) time() *time.Time {
	if w == nil || w.Date == nil {
		return nil
	}
	t := *w.Date
	return &t
}

// decodeData unmarshals an answer's data into v with a fixed error text.
func decodeData(data []byte, v any, what string) error {
	if json.Unmarshal(data, v) != nil {
		return unreadable(what + " answer does not match the operation")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Clusters

const opK8sClusters = `query RubrikK8sClusters($first: Int!, $after: String) {
  k8sClusters(first: $first, after: $after) {
    nodes { id name status }
    pageInfo { hasNextPage endCursor }
  }
}`

const opKubernetesClusters = `query RubrikKubernetesClusters($first: Int!, $after: String) {
  kubernetesClusters(first: $first, after: $after) {
    nodes { id name status }
    pageInfo { hasNextPage endCursor }
  }
}`

type wireCluster struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func decodeClusterNodes(family Family, nodes []wireCluster) ([]Cluster, error) {
	out := make([]Cluster, 0, len(nodes))
	for _, n := range nodes {
		if n.ID == "" {
			return nil, unreadable("cluster without an id")
		}
		out = append(out, Cluster{Family: family, ID: n.ID, Name: n.Name, Status: n.Status})
	}
	return out, nil
}

// decodeK8sClustersPage decodes one page of k8sClusters.
func decodeK8sClustersPage(data []byte) ([]Cluster, pageInfo, error) {
	var ans struct {
		Conn *struct {
			Nodes    []wireCluster `json:"nodes"`
			PageInfo pageInfo      `json:"pageInfo"`
		} `json:"k8sClusters"`
	}
	if err := decodeData(data, &ans, "cluster list"); err != nil {
		return nil, pageInfo{}, err
	}
	if ans.Conn == nil {
		return nil, pageInfo{}, unreadable("cluster list answer has no list")
	}
	out, err := decodeClusterNodes(FamilyNamespace, ans.Conn.Nodes)
	return out, ans.Conn.PageInfo, err
}

// decodeKubernetesClustersPage decodes one page of kubernetesClusters.
func decodeKubernetesClustersPage(data []byte) ([]Cluster, pageInfo, error) {
	var ans struct {
		Conn *struct {
			Nodes    []wireCluster `json:"nodes"`
			PageInfo pageInfo      `json:"pageInfo"`
		} `json:"kubernetesClusters"`
	}
	if err := decodeData(data, &ans, "cluster list"); err != nil {
		return nil, pageInfo{}, err
	}
	if ans.Conn == nil {
		return nil, pageInfo{}, unreadable("cluster list answer has no list")
	}
	out, err := decodeClusterNodes(FamilyProtectionSet, ans.Conn.Nodes)
	return out, ans.Conn.PageInfo, err
}

// Clusters lists every Kubernetes cluster RSC knows about, from both
// families: RSC-native clusters (per-namespace objects) first, then CDM
// clusters (protection sets).
func (o *Operations) Clusters(ctx context.Context) ([]Cluster, error) {
	native, err := collectPages(ctx, 0, func(ctx context.Context, after string) ([]Cluster, pageInfo, error) {
		data, err := o.query(ctx, opK8sClusters, pageVars(map[string]any{}, after))
		if err != nil {
			return nil, pageInfo{}, err
		}
		return decodeK8sClustersPage(data)
	})
	if err != nil {
		return nil, err
	}
	cdm, err := collectPages(ctx, 0, func(ctx context.Context, after string) ([]Cluster, pageInfo, error) {
		data, err := o.query(ctx, opKubernetesClusters, pageVars(map[string]any{}, after))
		if err != nil {
			return nil, pageInfo{}, err
		}
		return decodeKubernetesClustersPage(data)
	})
	if err != nil {
		return nil, err
	}
	return append(native, cdm...), nil
}

// ---------------------------------------------------------------------------
// Per-namespace objects

const opK8sNamespaces = `query RubrikK8sNamespaces($clusterId: UUID!, $first: Int!, $after: String) {
  k8sNamespaces(k8sClusterId: $clusterId, first: $first, after: $after) {
    nodes {
      id
      k8sClusterId
      namespaceName
      isRelic
      clusterScoped
      slaAssignment
      configuredSlaDomain { id name }
      effectiveSlaDomain { id name }
      newestSnapshot { date }
    }
    pageInfo { hasNextPage endCursor }
  }
}`

type wireNamespace struct {
	ID             string           `json:"id"`
	K8sClusterID   string           `json:"k8sClusterId"`
	NamespaceName  string           `json:"namespaceName"`
	IsRelic        bool             `json:"isRelic"`
	ClusterScoped  *bool            `json:"clusterScoped"`
	SLAAssignment  string           `json:"slaAssignment"`
	ConfiguredSLA  *wireSLA         `json:"configuredSlaDomain"`
	EffectiveSLA   *wireSLA         `json:"effectiveSlaDomain"`
	NewestSnapshot *wireSnapshotRef `json:"newestSnapshot"`
}

func (n wireNamespace) object() Object {
	cov := Coverage{Unresolved: true}
	if ns := strings.TrimSpace(n.NamespaceName); ns != "" {
		cov = Coverage{Namespaces: []string{ns}}
	}
	return Object{
		Family:        FamilyNamespace,
		ID:            n.ID,
		Name:          n.NamespaceName,
		Coverage:      cov,
		ConfiguredSLA: n.ConfiguredSLA.ref(),
		EffectiveSLA:  n.EffectiveSLA.ref(),
		SLAAssignment: n.SLAAssignment,
		// The schema documents clusterScoped as whether the namespace holds
		// cluster-scoped resources. A missing value fails closed (KTD9).
		MayHoldClusterScoped: n.ClusterScoped == nil || *n.ClusterScoped,
		NewestSnapshot:       n.NewestSnapshot.time(),
		Relic:                n.IsRelic,
	}
}

// decodeK8sNamespacesPage decodes one page of k8sNamespaces asked for
// clusterID. Every node must name that cluster (checkNodeCluster).
func decodeK8sNamespacesPage(data []byte, clusterID string) ([]Object, pageInfo, error) {
	var ans struct {
		Conn *struct {
			Nodes    []wireNamespace `json:"nodes"`
			PageInfo pageInfo        `json:"pageInfo"`
		} `json:"k8sNamespaces"`
	}
	if err := decodeData(data, &ans, "namespace list"); err != nil {
		return nil, pageInfo{}, err
	}
	if ans.Conn == nil {
		return nil, pageInfo{}, unreadable("namespace list answer has no list")
	}
	out := make([]Object, 0, len(ans.Conn.Nodes))
	for _, n := range ans.Conn.Nodes {
		if n.ID == "" {
			return nil, pageInfo{}, unreadable("namespace object without an id")
		}
		if err := checkNodeCluster(n.K8sClusterID, clusterID, "namespace object"); err != nil {
			return nil, pageInfo{}, err
		}
		out = append(out, n.object())
	}
	return out, ans.Conn.PageInfo, nil
}

// NamespaceObjects lists the per-namespace objects of an RSC-native cluster,
// relics included.
func (o *Operations) NamespaceObjects(ctx context.Context, clusterID string) ([]Object, error) {
	if !validUUID(clusterID) {
		return nil, invalid("cluster id is not a uuid")
	}
	return collectPages(ctx, 0, func(ctx context.Context, after string) ([]Object, pageInfo, error) {
		data, err := o.query(ctx, opK8sNamespaces, pageVars(map[string]any{"clusterId": clusterID}, after))
		if err != nil {
			return nil, pageInfo{}, err
		}
		return decodeK8sNamespacesPage(data, clusterID)
	})
}

// ---------------------------------------------------------------------------
// Protection sets

// The schema has no single protection-set list under a cluster other than
// kubernetesProtectionSets, whose optional cluster argument narrows it to
// one CDM KubernetesCluster.
const opProtectionSets = `query RubrikK8sProtectionSets($clusterId: UUID, $first: Int!, $after: String) {
  kubernetesProtectionSets(k8sClusterOptionalId: $clusterId, first: $first, after: $after) {
    nodes {
      id
      name
      rsType
      namespace
      namespaceIncludePatterns
      namespaceExcludePatterns
      labelSelector
      isRelic
      k8sClusterUuid
      slaAssignment
      configuredSlaDomain { id name }
      effectiveSlaDomain { id name }
      newestSnapshot { date }
    }
    pageInfo { hasNextPage endCursor }
  }
}`

type wireProtectionSet struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	RSType         string           `json:"rsType"`
	Namespace      *string          `json:"namespace"`
	Include        *string          `json:"namespaceIncludePatterns"`
	Exclude        *string          `json:"namespaceExcludePatterns"`
	LabelSelector  *string          `json:"labelSelector"`
	IsRelic        bool             `json:"isRelic"`
	K8sClusterUUID string           `json:"k8sClusterUuid"`
	SLAAssignment  string           `json:"slaAssignment"`
	ConfiguredSLA  *wireSLA         `json:"configuredSlaDomain"`
	EffectiveSLA   *wireSLA         `json:"effectiveSlaDomain"`
	NewestSnapshot *wireSnapshotRef `json:"newestSnapshot"`
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (p wireProtectionSet) object() Object {
	return Object{
		Family:         FamilyProtectionSet,
		ID:             p.ID,
		Name:           p.Name,
		SetType:        p.RSType,
		Coverage:       setCoverage(p.RSType, deref(p.Namespace), deref(p.Include), deref(p.Exclude), deref(p.LabelSelector)),
		ConfiguredSLA:  p.ConfiguredSLA.ref(),
		EffectiveSLA:   p.EffectiveSLA.ref(),
		SLAAssignment:  p.SLAAssignment,
		NewestSnapshot: p.NewestSnapshot.time(),
		Relic:          p.IsRelic,
		// KTD9, fail closed: every protection set may hold cluster-scoped
		// resources. A set not of type namespace does by its type. For a
		// namespace set nothing in RSC's answer rules it out: the schema
		// documents definition and customResourceDependencies only as free
		// strings, with no format, so no reading of them can show a set
		// free of cluster-scoped kinds. Narrow this only once a recorded
		// RSC definition fixes the format.
		MayHoldClusterScoped: true,
	}
}

// decodeProtectionSetsPage decodes one page of kubernetesProtectionSets asked
// for clusterID. The cluster argument is optional in the schema, so an RSC
// that ignored it would answer with every cluster's sets: every node must
// name the asked cluster (checkNodeCluster).
func decodeProtectionSetsPage(data []byte, clusterID string) ([]Object, pageInfo, error) {
	var ans struct {
		Conn *struct {
			Nodes    []wireProtectionSet `json:"nodes"`
			PageInfo pageInfo            `json:"pageInfo"`
		} `json:"kubernetesProtectionSets"`
	}
	if err := decodeData(data, &ans, "protection set list"); err != nil {
		return nil, pageInfo{}, err
	}
	if ans.Conn == nil {
		return nil, pageInfo{}, unreadable("protection set list answer has no list")
	}
	out := make([]Object, 0, len(ans.Conn.Nodes))
	for _, n := range ans.Conn.Nodes {
		if n.ID == "" {
			return nil, pageInfo{}, unreadable("protection set without an id")
		}
		if err := checkNodeCluster(n.K8sClusterUUID, clusterID, "protection set"); err != nil {
			return nil, pageInfo{}, err
		}
		out = append(out, n.object())
	}
	return out, ans.Conn.PageInfo, nil
}

// ProtectionSets lists the protection sets of a CDM Kubernetes cluster,
// relics included.
func (o *Operations) ProtectionSets(ctx context.Context, clusterID string) ([]Object, error) {
	if !validUUID(clusterID) {
		return nil, invalid("cluster id is not a uuid")
	}
	return collectPages(ctx, 0, func(ctx context.Context, after string) ([]Object, pageInfo, error) {
		data, err := o.query(ctx, opProtectionSets, pageVars(map[string]any{"clusterId": clusterID}, after))
		if err != nil {
			return nil, pageInfo{}, err
		}
		return decodeProtectionSetsPage(data, clusterID)
	})
}

// ---------------------------------------------------------------------------
// Snapshots

const opNamespaceSnapshots = `query RubrikK8sNamespaceSnapshots($id: UUID!, $first: Int!, $after: String) {
  k8sNamespace(fid: $id) {
    snapshotConnection(first: $first, after: $after, sortBy: Date, sortOrder: DESC) {
      nodes { id date expirationDate isExpired isOnDemandSnapshot isCorrupted isQuarantined }
      pageInfo { hasNextPage endCursor }
    }
  }
}`

const opProtectionSetSnapshots = `query RubrikK8sProtectionSetSnapshots($id: UUID!, $first: Int!, $after: String) {
  kubernetesProtectionSet(fid: $id) {
    snapshotConnection(first: $first, after: $after, sortBy: Date, sortOrder: DESC) {
      nodes { id date expirationDate isExpired isOnDemandSnapshot isCorrupted isQuarantined }
      pageInfo { hasNextPage endCursor }
    }
  }
}`

type wireSnapshot struct {
	ID          string     `json:"id"`
	Date        *time.Time `json:"date"`
	Expiration  *time.Time `json:"expirationDate"`
	IsExpired   bool       `json:"isExpired"`
	OnDemand    bool       `json:"isOnDemandSnapshot"`
	Corrupted   bool       `json:"isCorrupted"`
	Quarantined bool       `json:"isQuarantined"`
}

type wireSnapshotConn struct {
	Nodes    []wireSnapshot `json:"nodes"`
	PageInfo pageInfo       `json:"pageInfo"`
}

// decodeSnapshotConn decodes a snapshot connection. A null connection (the
// schema allows it) is an empty list.
func decodeSnapshotConn(conn *wireSnapshotConn) ([]Snapshot, pageInfo, error) {
	if conn == nil {
		return nil, pageInfo{}, nil
	}
	out := make([]Snapshot, 0, len(conn.Nodes))
	for _, n := range conn.Nodes {
		if n.ID == "" || n.Date == nil {
			return nil, pageInfo{}, unreadable("snapshot without an id or time")
		}
		s := Snapshot{ID: n.ID, Time: *n.Date, Expired: n.IsExpired, OnDemand: n.OnDemand, Corrupted: n.Corrupted, Quarantined: n.Quarantined}
		if n.Expiration != nil {
			t := *n.Expiration
			s.Expires = &t
		}
		out = append(out, s)
	}
	return out, conn.PageInfo, nil
}

// decodeNamespaceSnapshotsPage decodes one page of a per-namespace object's
// snapshots.
func decodeNamespaceSnapshotsPage(data []byte) ([]Snapshot, pageInfo, error) {
	var ans struct {
		Object *struct {
			Conn *wireSnapshotConn `json:"snapshotConnection"`
		} `json:"k8sNamespace"`
	}
	if err := decodeData(data, &ans, "snapshot list"); err != nil {
		return nil, pageInfo{}, err
	}
	if ans.Object == nil {
		return nil, pageInfo{}, unreadable("snapshot list answer has no object")
	}
	return decodeSnapshotConn(ans.Object.Conn)
}

// decodeProtectionSetSnapshotsPage decodes one page of a protection set's
// snapshots.
func decodeProtectionSetSnapshotsPage(data []byte) ([]Snapshot, pageInfo, error) {
	var ans struct {
		Object *struct {
			Conn *wireSnapshotConn `json:"snapshotConnection"`
		} `json:"kubernetesProtectionSet"`
	}
	if err := decodeData(data, &ans, "snapshot list"); err != nil {
		return nil, pageInfo{}, err
	}
	if ans.Object == nil {
		return nil, pageInfo{}, unreadable("snapshot list answer has no object")
	}
	return decodeSnapshotConn(ans.Object.Conn)
}

// snapshotLimit applies DefaultSnapshotLimit and MaxSnapshotLimit.
func snapshotLimit(limit int) int {
	if limit <= 0 {
		return DefaultSnapshotLimit
	}
	return min(limit, MaxSnapshotLimit)
}

// Snapshots lists an object's snapshots, newest first, at most limit of
// them (DefaultSnapshotLimit when limit is 0, never more than
// MaxSnapshotLimit).
func (o *Operations) Snapshots(ctx context.Context, family Family, objectID string, limit int) ([]Snapshot, error) {
	if !validUUID(objectID) {
		return nil, invalid("object id is not a uuid")
	}
	doc, decode := opNamespaceSnapshots, decodeNamespaceSnapshotsPage
	switch family {
	case FamilyNamespace:
	case FamilyProtectionSet:
		doc, decode = opProtectionSetSnapshots, decodeProtectionSetSnapshotsPage
	default:
		return nil, invalid("unknown object family")
	}
	return collectPages(ctx, snapshotLimit(limit), func(ctx context.Context, after string) ([]Snapshot, pageInfo, error) {
		data, err := o.query(ctx, doc, pageVars(map[string]any{"id": objectID}, after))
		if err != nil {
			return nil, pageInfo{}, err
		}
		return decode(data)
	})
}

// ---------------------------------------------------------------------------
// Job history (activity series)

const opActivitySeries = `query RubrikActivitySeries($filters: ActivitySeriesFilter, $first: Int!, $after: String) {
  activitySeriesConnection(filters: $filters, first: $first, after: $after, sortBy: START_TIME, sortOrder: DESC) {
    nodes {
      activitySeriesId
      objectId
      lastActivityType
      lastActivityStatus
      lastActivityMessage
      failureReason
      causeErrorMessage
      startTime
      lastUpdated
    }
    pageInfo { hasNextPage endCursor }
  }
}`

// ActivityFilter narrows a job history query. ObjectIDs is required.
type ActivityFilter struct {
	ObjectIDs []string
	// Statuses are RSC EventStatus values (for example FAILURE, SUCCESS,
	// RUNNING); empty means every status.
	Statuses []string
	// StartedAfter, when set, keeps only jobs started after it.
	StartedAfter *time.Time
	// Limit caps the number of jobs; 0 means DefaultSnapshotLimit.
	Limit int
}

// eventStatuses are the EventStatus values the schema accepts in a filter.
var eventStatuses = map[string]bool{
	"CANCELED": true, "CANCELING": true, "FAILURE": true, "INFO": true,
	"PARTIAL_SUCCESS": true, "QUEUED": true, "RUNNING": true, "SUCCESS": true,
	"TASK_FAILURE": true, "TASK_SUCCESS": true, "UNKNOWN_EVENT_STATUS": true, "WARNING": true,
}

type wireActivity struct {
	ID            string     `json:"activitySeriesId"`
	ObjectID      string     `json:"objectId"`
	Type          string     `json:"lastActivityType"`
	Status        string     `json:"lastActivityStatus"`
	Message       *string    `json:"lastActivityMessage"`
	FailureReason *string    `json:"failureReason"`
	CauseMessage  *string    `json:"causeErrorMessage"`
	StartTime     *time.Time `json:"startTime"`
	LastUpdated   *time.Time `json:"lastUpdated"`
}

// activityStatus maps RSC's ActivityStatusEnum to JobStatus.
func activityStatus(s string) JobStatus {
	switch s {
	case "Success", "TaskSuccess":
		return JobSucceeded
	case "Failure", "TaskFailure":
		return JobFailed
	case "PARTIAL_SUCCESS":
		return JobPartial
	case "Running", "Queued", "Canceling":
		return JobRunning
	case "Canceled":
		return JobCanceled
	}
	return JobUnknown
}

// decodeActivityPage decodes one page of activitySeriesConnection.
func decodeActivityPage(data []byte) ([]Job, pageInfo, error) {
	var ans struct {
		Conn *struct {
			Nodes    []wireActivity `json:"nodes"`
			PageInfo pageInfo       `json:"pageInfo"`
		} `json:"activitySeriesConnection"`
	}
	if err := decodeData(data, &ans, "job history"); err != nil {
		return nil, pageInfo{}, err
	}
	if ans.Conn == nil {
		return nil, pageInfo{}, unreadable("job history answer has no list")
	}
	out := make([]Job, 0, len(ans.Conn.Nodes))
	for _, n := range ans.Conn.Nodes {
		if n.ID == "" {
			return nil, pageInfo{}, unreadable("job without an id")
		}
		j := Job{
			ID:          n.ID,
			ObjectID:    n.ObjectID,
			Type:        n.Type,
			Status:      activityStatus(n.Status),
			StartTime:   n.StartTime,
			LastUpdated: n.LastUpdated,
			Message:     deref(n.Message),
		}
		if j.Status == JobFailed || j.Status == JobPartial {
			j.FailureReason = deref(n.FailureReason)
			if j.FailureReason == "" {
				j.FailureReason = deref(n.CauseMessage)
			}
		}
		out = append(out, j)
	}
	return out, ans.Conn.PageInfo, nil
}

// Jobs lists the job history of the filter's objects, newest first.
func (o *Operations) Jobs(ctx context.Context, f ActivityFilter) ([]Job, error) {
	if len(f.ObjectIDs) == 0 {
		return nil, invalid("no object id")
	}
	for _, id := range f.ObjectIDs {
		if !validUUID(id) {
			return nil, invalid("object id is not a uuid")
		}
	}
	filters := map[string]any{"objectFid": f.ObjectIDs}
	if len(f.Statuses) > 0 {
		for _, s := range f.Statuses {
			if !eventStatuses[s] {
				return nil, invalid("unknown activity status")
			}
		}
		filters["lastActivityStatus"] = f.Statuses
	}
	if f.StartedAfter != nil {
		filters["startTimeGt"] = f.StartedAfter.UTC().Format(time.RFC3339)
	}
	return collectPages(ctx, snapshotLimit(f.Limit), func(ctx context.Context, after string) ([]Job, pageInfo, error) {
		data, err := o.query(ctx, opActivitySeries, pageVars(map[string]any{"filters": filters}, after))
		if err != nil {
			return nil, pageInfo{}, err
		}
		return decodeActivityPage(data)
	})
}

// ---------------------------------------------------------------------------
// Taskchain (per-namespace job state)

const opTaskchain = `query RubrikTaskchain($id: String!) {
  taskchain(taskchainId: $id) { taskchainUuid state error }
}`

// taskchainStatus maps RSC's TaskchainState to JobStatus.
func taskchainStatus(s string) JobStatus {
	switch s {
	case "SUCCEEDED":
		return JobSucceeded
	case "FAILED":
		return JobFailed
	case "CANCELED":
		return JobCanceled
	case "READY", "RUNNING", "CANCELING", "UNDOING":
		return JobRunning
	}
	return JobUnknown
}

// decodeTaskchain decodes a taskchain answer.
func decodeTaskchain(data []byte) (TaskchainState, error) {
	var ans struct {
		Taskchain *struct {
			State string `json:"state"`
			Error string `json:"error"`
		} `json:"taskchain"`
	}
	if err := decodeData(data, &ans, "taskchain"); err != nil {
		return TaskchainState{}, err
	}
	if ans.Taskchain == nil {
		return TaskchainState{}, unreadable("taskchain answer has no taskchain")
	}
	st := TaskchainState{Status: taskchainStatus(ans.Taskchain.State)}
	if st.Status == JobFailed {
		st.Error = ans.Taskchain.Error
	}
	return st, nil
}

// Taskchain reads the state of the taskchain a per-namespace mutation
// started. A protection-set mutation's async request has no matching query;
// follow it through Jobs (KTD7).
func (o *Operations) Taskchain(ctx context.Context, taskchainID string) (TaskchainState, error) {
	if strings.TrimSpace(taskchainID) == "" || len(taskchainID) > 256 {
		return TaskchainState{}, invalid("taskchain id")
	}
	data, err := o.query(ctx, opTaskchain, map[string]any{"id": taskchainID})
	if err != nil {
		return TaskchainState{}, err
	}
	return decodeTaskchain(data)
}

// ---------------------------------------------------------------------------
// SLA domains

const scheduleFields = `snapshotSchedule {
        minute { basicSchedule { frequency } }
        hourly { basicSchedule { frequency } }
        daily { basicSchedule { frequency } }
        weekly { basicSchedule { frequency } }
        monthly { basicSchedule { frequency } }
        quarterly { basicSchedule { frequency } }
        yearly { basicSchedule { frequency } }
      }`

// opSLADomains lists the domains for both families: K8S_OBJECT_TYPE is the
// schema's "CDM Native K8s object" (protection sets) and KUPR_OBJECT_TYPE its
// "Kubernetes object" (per-namespace objects, hierarchy type KuprNamespace).
const opSLADomains = `query RubrikSlaDomains($first: Int!, $after: String) {
  slaDomains(first: $first, after: $after, filter: [{field: OBJECT_TYPE, objectTypeList: [K8S_OBJECT_TYPE, KUPR_OBJECT_TYPE]}]) {
    nodes {
      id
      name
      ... on GlobalSlaReply {
      ` + scheduleFields + `
      }
      ... on ClusterSlaDomain {
      ` + scheduleFields + `
      }
    }
    pageInfo { hasNextPage endCursor }
  }
}`

type wireBasic struct {
	Basic *struct {
		Frequency int64 `json:"frequency"`
	} `json:"basicSchedule"`
}

type wireSchedule struct {
	Minute    *wireBasic `json:"minute"`
	Hourly    *wireBasic `json:"hourly"`
	Daily     *wireBasic `json:"daily"`
	Weekly    *wireBasic `json:"weekly"`
	Monthly   *wireBasic `json:"monthly"`
	Quarterly *wireBasic `json:"quarterly"`
	Yearly    *wireBasic `json:"yearly"`
}

// Schedule units. Calendar units use their longest length, so a judgment
// against them errs toward "in compliance" rather than a false alarm.
const (
	day     = 24 * time.Hour
	month   = 31 * day
	quarter = 92 * day
	year    = 366 * day
)

// scheduleInterval multiplies a schedule's frequency by its unit, saturating
// instead of overflowing. A missing or non-positive frequency gives 0.
func scheduleInterval(b *wireBasic, unit time.Duration) time.Duration {
	if b == nil || b.Basic == nil || b.Basic.Frequency <= 0 {
		return 0
	}
	if b.Basic.Frequency > int64(math.MaxInt64/unit) {
		return math.MaxInt64
	}
	return time.Duration(b.Basic.Frequency) * unit
}

// shortestInterval returns the most frequent schedule's interval, 0 when the
// domain has no schedule.
func (s *wireSchedule) shortestInterval() time.Duration {
	if s == nil {
		return 0
	}
	var best time.Duration
	for _, d := range []time.Duration{
		scheduleInterval(s.Minute, time.Minute),
		scheduleInterval(s.Hourly, time.Hour),
		scheduleInterval(s.Daily, day),
		scheduleInterval(s.Weekly, 7*day),
		scheduleInterval(s.Monthly, month),
		scheduleInterval(s.Quarterly, quarter),
		scheduleInterval(s.Yearly, year),
	} {
		if d > 0 && (best == 0 || d < best) {
			best = d
		}
	}
	return best
}

// decodeSLADomainsPage decodes one page of slaDomains.
func decodeSLADomainsPage(data []byte) ([]SLADomain, pageInfo, error) {
	var ans struct {
		Conn *struct {
			Nodes []struct {
				ID       string        `json:"id"`
				Name     string        `json:"name"`
				Schedule *wireSchedule `json:"snapshotSchedule"`
			} `json:"nodes"`
			PageInfo pageInfo `json:"pageInfo"`
		} `json:"slaDomains"`
	}
	if err := decodeData(data, &ans, "sla domain list"); err != nil {
		return nil, pageInfo{}, err
	}
	if ans.Conn == nil {
		return nil, pageInfo{}, unreadable("sla domain list answer has no list")
	}
	out := make([]SLADomain, 0, len(ans.Conn.Nodes))
	for _, n := range ans.Conn.Nodes {
		if n.ID == "" {
			return nil, pageInfo{}, unreadable("sla domain without an id")
		}
		out = append(out, SLADomain{ID: n.ID, Name: n.Name, Frequency: n.Schedule.shortestInterval()})
	}
	return out, ans.Conn.PageInfo, nil
}

// SLADomains lists the SLA domains that apply to Kubernetes objects, with
// each domain's most frequent schedule interval (KTD5).
func (o *Operations) SLADomains(ctx context.Context) ([]SLADomain, error) {
	return collectPages(ctx, 0, func(ctx context.Context, after string) ([]SLADomain, pageInfo, error) {
		data, err := o.query(ctx, opSLADomains, pageVars(map[string]any{}, after))
		if err != nil {
			return nil, pageInfo{}, err
		}
		return decodeSLADomainsPage(data)
	})
}

// ---------------------------------------------------------------------------
// Mutations

const opCreateNamespaceSnapshot = `mutation RubrikCreateK8sNamespaceSnapshots($input: CreateK8sNamespaceSnapshotsInput!) {
  createK8sNamespaceSnapshots(input: $input) { jobId taskchainId }
}`

const opCreateProtectionSetSnapshot = `mutation RubrikCreateK8sProtectionSetSnapshot($input: CreateK8sProtectionSetSnapshotInput!) {
  createK8sProtectionSetSnapshot(input: $input) { id status }
}`

const opAssignSLA = `mutation RubrikAssignSla($input: AssignSlaInput!) {
  assignSla(input: $input) { success }
}`

const opRestoreNamespace = `mutation RubrikRestoreK8sNamespace($input: RestoreK8sNamespaceInput!) {
  restoreK8sNamespace(input: $input) { jobId taskchainId }
}`

const opRestoreProtectionSetSnapshot = `mutation RubrikRestoreK8sProtectionSetSnapshot($input: CreateK8sRestoreJobInput!) {
  restoreK8sProtectionSetSnapshot(input: $input) { id status }
}`

type wireJobReply struct {
	JobID       int64  `json:"jobId"`
	TaskchainID string `json:"taskchainId"`
}

// handle returns the reply's job handle. A reply without a taskchain may
// still stand for a started job, so it is unreadable, never rejected.
func (r wireJobReply) handle() (JobHandle, error) {
	if r.TaskchainID == "" {
		return JobHandle{}, unreadable("reply has no taskchain")
	}
	return JobHandle{Family: FamilyNamespace, TaskchainID: r.TaskchainID, JobID: r.JobID}, nil
}

type wireAsyncReply struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// handle returns the reply's job handle. A reply without a request id may
// still stand for a started job, so it is unreadable, never rejected.
func (r wireAsyncReply) handle() (JobHandle, error) {
	if r.ID == "" {
		return JobHandle{}, unreadable("reply has no request id")
	}
	return JobHandle{Family: FamilyProtectionSet, RequestID: r.ID, RequestStatus: r.Status}, nil
}

// decodeNamespaceSnapshotReply decodes createK8sNamespaceSnapshots, which
// answers a list; one namespace was asked for, so exactly one job must come
// back. Any other count leaves unknown what RSC started, so it is
// unreadable.
func decodeNamespaceSnapshotReply(data []byte) (JobHandle, error) {
	var ans struct {
		Replies *[]wireJobReply `json:"createK8sNamespaceSnapshots"`
	}
	if err := decodeData(data, &ans, "snapshot request"); err != nil {
		return JobHandle{}, err
	}
	if ans.Replies == nil {
		return JobHandle{}, unreadable("snapshot request answer has no reply")
	}
	if len(*ans.Replies) != 1 {
		return JobHandle{}, unreadable(fmt.Sprintf("expected one job, got %d", len(*ans.Replies)))
	}
	return (*ans.Replies)[0].handle()
}

// decodeAsyncReply decodes a protection-set mutation's AsyncRequestStatus
// under field.
func decodeAsyncReply(data []byte, field string) (JobHandle, error) {
	// Only field is read, so another field in the answer cannot fail it.
	var ans map[string]json.RawMessage
	if err := decodeData(data, &ans, "request"); err != nil {
		return JobHandle{}, err
	}
	var r *wireAsyncReply
	if err := decodeData(ans[field], &r, "request"); err != nil {
		return JobHandle{}, err
	}
	if r == nil {
		return JobHandle{}, unreadable("request answer has no reply")
	}
	return r.handle()
}

// decodeRestoreNamespaceReply decodes restoreK8sNamespace.
func decodeRestoreNamespaceReply(data []byte) (JobHandle, error) {
	var ans struct {
		Reply *wireJobReply `json:"restoreK8sNamespace"`
	}
	if err := decodeData(data, &ans, "restore request"); err != nil {
		return JobHandle{}, err
	}
	if ans.Reply == nil {
		return JobHandle{}, unreadable("restore request answer has no reply")
	}
	return ans.Reply.handle()
}

// decodeAssignSLAReply decodes assignSla.
func decodeAssignSLAReply(data []byte) error {
	var ans struct {
		Result *struct {
			Success *bool `json:"success"`
		} `json:"assignSla"`
	}
	if err := decodeData(data, &ans, "sla assignment"); err != nil {
		return err
	}
	if ans.Result == nil || ans.Result.Success == nil {
		return unreadable("sla assignment answer has no result")
	}
	if !*ans.Result.Success {
		return fmt.Errorf("%w: sla assignment not applied", ErrRejected)
	}
	return nil
}

// CreateSnapshot starts an on-demand snapshot of an object under SLA domain
// slaID. slaID is required (KTD8): callers pass the object's effective SLA
// (Object.SnapshotSLA) or the one the user picked.
func (o *Operations) CreateSnapshot(ctx context.Context, family Family, objectID, slaID string) (JobHandle, error) {
	if !validUUID(objectID) {
		return JobHandle{}, invalid("object id is not a uuid")
	}
	if !(SLARef{ID: slaID}).Protects() {
		return JobHandle{}, invalid("an on-demand snapshot needs an sla domain")
	}
	switch family {
	case FamilyNamespace:
		data, err := o.query(ctx, opCreateNamespaceSnapshot, map[string]any{"input": map[string]any{
			"snapshotInput": []any{map[string]any{"namespaceId": objectID, "onDemandSnapshotSlaId": slaID}},
		}})
		if err != nil {
			return JobHandle{}, err
		}
		return decodeNamespaceSnapshotReply(data)
	case FamilyProtectionSet:
		data, err := o.query(ctx, opCreateProtectionSetSnapshot, map[string]any{"input": map[string]any{
			"id":     objectID,
			"config": map[string]any{"slaId": slaID},
		}})
		if err != nil {
			return JobHandle{}, err
		}
		return decodeAsyncReply(data, "createK8sProtectionSetSnapshot")
	}
	return JobHandle{}, invalid("unknown object family")
}

// AssignSLA assigns SLA domain slaID to objects, or removes protection when
// slaID is empty ("do not protect"). Existing snapshots are always retained
// under their current SLA: the request pins existingSnapshotRetention to
// RETAIN_SNAPSHOTS and never sets shouldApplyToExistingSnapshots (KTD8).
func (o *Operations) AssignSLA(ctx context.Context, objectIDs []string, slaID string) error {
	if len(objectIDs) == 0 {
		return invalid("no object id")
	}
	for _, id := range objectIDs {
		if !validUUID(id) {
			return invalid("object id is not a uuid")
		}
	}
	input := map[string]any{
		"objectIds":                 objectIDs,
		"existingSnapshotRetention": "RETAIN_SNAPSHOTS",
	}
	if slaID == "" {
		input["slaDomainAssignType"] = "doNotProtect"
	} else {
		if !validUUID(slaID) {
			return invalid("sla domain id is not a uuid")
		}
		input["slaDomainAssignType"] = "protectWithSlaId"
		input["slaOptionalId"] = slaID
	}
	data, err := o.query(ctx, opAssignSLA, map[string]any{"input": input})
	if err != nil {
		return err
	}
	return decodeAssignSLAReply(data)
}

// RestoreNamespace restores a per-namespace object's snapshot into namespace
// targetNamespace of RSC-native cluster targetClusterID (KTD9). The access
// checks of R17 are the caller's.
func (o *Operations) RestoreNamespace(ctx context.Context, snapshotID, targetClusterID, targetNamespace string) (JobHandle, error) {
	if !validUUID(snapshotID) {
		return JobHandle{}, invalid("snapshot id is not a uuid")
	}
	if !validUUID(targetClusterID) {
		return JobHandle{}, invalid("cluster id is not a uuid")
	}
	if len(validation.IsDNS1123Label(targetNamespace)) > 0 {
		return JobHandle{}, invalid("target namespace is not a namespace name")
	}
	data, err := o.query(ctx, opRestoreNamespace, map[string]any{"input": map[string]any{
		"snapshotUuid":        snapshotID,
		"targetClusterUuid":   targetClusterID,
		"targetNamespaceName": targetNamespace,
	}})
	if err != nil {
		return JobHandle{}, err
	}
	return decodeRestoreNamespaceReply(data)
}

// RestoreProtectionSetSnapshot restores a protection set's snapshot in
// place, into the set's own namespaces on its own cluster (KTD9). RSC
// requires the target namespaces to exist.
func (o *Operations) RestoreProtectionSetSnapshot(ctx context.Context, snapshotID string) (JobHandle, error) {
	if !validUUID(snapshotID) {
		return JobHandle{}, invalid("snapshot id is not a uuid")
	}
	data, err := o.query(ctx, opRestoreProtectionSetSnapshot, map[string]any{"input": map[string]any{
		"id":        snapshotID,
		"jobConfig": map[string]any{},
	}})
	if err != nil {
		return JobHandle{}, err
	}
	return decodeAsyncReply(data, "restoreK8sProtectionSetSnapshot")
}
