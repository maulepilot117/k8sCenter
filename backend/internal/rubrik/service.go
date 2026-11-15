package rubrik

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/singleflight"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/store"
)

const (
	// CacheTTL is how long one RSC answer serves reads before the next read
	// fetches again (KTD15). A failed fetch is not retried sooner either,
	// so an outage costs RSC one call per key per TTL, not one per request.
	CacheTTL = 30 * time.Second
	// PollInterval is the poller's tick (KTD10). Data older than twice the
	// interval is reported stale.
	PollInterval = 5 * time.Minute
	staleAfter   = 2 * PollInterval
	// fetchTimeout bounds one cached fetch. It is detached from the request
	// that started it, so a caller giving up does not fail the others
	// waiting on the same fetch; a list may take up to maxPages pages.
	fetchTimeout = 2 * time.Minute
	// historyEntryAge is how long a snapshot or job-history entry stays in
	// the cache unread. Object lists stay for as long as their mapping and
	// connection revision do, because they are the last good data shown
	// during an outage (R13).
	historyEntryAge = time.Hour
)

// Errors a read reports before any data exists. Each matches exactly one of
// them, or one of the client's classes, with errors.Is.
var (
	// ErrNotMapped means the cluster has no Rubrik mapping.
	ErrNotMapped = errors.New("rubrik: cluster not mapped")
	// ErrMappingInvalid means the mapping no longer matches the cluster's
	// registration (the cluster was deleted or registered again), or was
	// saved for another connection. Reads show "not set up" and writes
	// refuse (R2, R22).
	ErrMappingInvalid = errors.New("rubrik: cluster mapping is invalid")
	// ErrTargetMissing means RSC lists no Kubernetes cluster with the mapped
	// id in either family. It is RSC's answer, not a failure, so no older
	// data is served in its place.
	ErrTargetMissing = errors.New("rubrik: mapped rsc cluster not found")
	// ErrStoreUnavailable means the mapping, connection revision or cluster
	// registry could not be read from PostgreSQL.
	ErrStoreUnavailable = errors.New("rubrik: store unavailable")
	// ErrObjectNotFound means the mapped cluster's objects include none with
	// the requested id. Snapshots and jobs are only ever fetched for an
	// object of the mapped cluster.
	ErrObjectNotFound = errors.New("rubrik: object not found")
)

// RSCReader is the read half of Operations the service caches.
type RSCReader interface {
	Clusters(ctx context.Context) ([]Cluster, error)
	NamespaceObjects(ctx context.Context, clusterID string) ([]Object, error)
	ProtectionSets(ctx context.Context, clusterID string) ([]Object, error)
	Snapshots(ctx context.Context, family Family, objectID string, limit int) ([]Snapshot, error)
	Jobs(ctx context.Context, f ActivityFilter) ([]Job, error)
	SLADomains(ctx context.Context) ([]SLADomain, error)
}

// MappingStore is the part of store.RubrikStore the service reads.
type MappingStore interface {
	ConnectionRevision(ctx context.Context, id string) (int64, error)
	GetMapping(ctx context.Context, clusterID string) (store.RubrikClusterMapping, error)
	ListComplianceStates(ctx context.Context, clusterID string) ([]store.RubrikComplianceState, error)
}

// ClusterRecords reads a cluster registration; store.ClusterStore
// implements it. A missing cluster is an error matching pgx.ErrNoRows.
type ClusterRecords interface {
	Get(ctx context.Context, id string) (*store.ClusterRecord, error)
}

// Target is a k8sCenter cluster whose mapping was valid when Resolve read
// it. Only Resolve makes a valid Target; the zero value refuses every write.
type Target struct {
	// ClusterID is the normalized k8sCenter cluster id.
	ClusterID string
	Mapping   store.RubrikClusterMapping
	// Revision is the connection revision the mapping was read under.
	Revision int64
	valid    bool
}

// Valid reports whether Resolve vouched for the mapping.
func (t Target) Valid() bool { return t.valid }

// Freshness says how current cached data is (R13).
type Freshness struct {
	// FetchedAt is when the data were fetched from RSC.
	FetchedAt time.Time `json:"fetchedAt"`
	// Stale is true when the latest fetch failed, so the data are the last
	// good answer, or when they are older than twice the poll interval.
	Stale bool `json:"stale"`
	// Err is the latest fetch's error when it failed. The caller logs it
	// and answers with a fixed message for its class; it may name an
	// address or carry tenant text.
	Err error `json:"-"`
}

// ClusterView is a mapped cluster's objects as RSC last reported them.
type ClusterView struct {
	// Cluster is the mapped RSC cluster, with the family its id belongs to.
	// The family is read from RSC on every fetch rather than stored with
	// the mapping: the two families list their clusters separately.
	Cluster Cluster
	// Objects are the cluster's objects of that family, relics included.
	// Callers filter them through a Viewer.
	Objects []Object
	Freshness
}

// Object returns the object with id, matched case-insensitively.
func (v ClusterView) Object(id string) (Object, bool) {
	for _, o := range v.Objects {
		if strings.EqualFold(o.ID, id) {
			return o, true
		}
	}
	return Object{}, false
}

// Service resolves mappings and serves RSC data per mapped cluster through
// a cache shared by the API and the poller (KTD15). Concurrent reads of an
// expired entry make one RSC call. It is safe for concurrent use. Build it
// with NewService.
type Service struct {
	rsc          RSCReader
	store        MappingStore
	clusters     ClusterRecords
	connectionID string
	logger       *slog.Logger
	// onRevisionChange runs when the stored connection changes (another
	// replica may have saved it). The client is passed here so its token
	// is dropped along with the cached data.
	onRevisionChange func()
	now              func() time.Time

	flights singleflight.Group

	mu sync.Mutex
	// revision is the last connection revision seen; 0 before the first
	// read and while no connection is stored.
	revision int64
	entries  map[string]*cacheEntry
}

// NewService returns a service over the RSC operations, the Rubrik store
// and the cluster registry. connectionID is store.RubrikDefaultConnectionID
// in production. onRevisionChange may be nil; production passes the
// client's Flush.
func NewService(rsc RSCReader, st MappingStore, clusters ClusterRecords, connectionID string, logger *slog.Logger, onRevisionChange func()) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		rsc:              rsc,
		store:            st,
		clusters:         clusters,
		connectionID:     connectionID,
		logger:           logger,
		onRevisionChange: onRevisionChange,
		entries:          map[string]*cacheEntry{},
	}
}

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// Resolve reads clusterID's mapping and checks it. It reports ErrNotConfigured
// when no connection is stored, ErrNotMapped when the cluster has no mapping,
// ErrMappingInvalid when the mapping was saved for another registration of
// the cluster or another connection, and ErrStoreUnavailable when PostgreSQL
// could not answer. A changed connection revision drops every cached answer
// first, whichever replica saved the connection.
func (s *Service) Resolve(ctx context.Context, clusterID string) (Target, error) {
	id := k8s.NormalizedClusterID(clusterID)
	rev, err := s.store.ConnectionRevision(ctx, s.connectionID)
	switch {
	case errors.Is(err, store.ErrRubrikConnectionNotFound):
		s.observeRevision(0)
		return Target{}, ErrNotConfigured
	case err != nil:
		return Target{}, fmt.Errorf("%w: reading connection revision: %w", ErrStoreUnavailable, err)
	}
	s.observeRevision(rev)

	m, err := s.store.GetMapping(ctx, id)
	switch {
	case errors.Is(err, store.ErrRubrikMappingNotFound):
		return Target{}, ErrNotMapped
	case err != nil:
		return Target{}, fmt.Errorf("%w: reading mapping: %w", ErrStoreUnavailable, err)
	}
	if m.ConnectionID != s.connectionID {
		return Target{}, fmt.Errorf("%w: saved for another connection", ErrMappingInvalid)
	}
	if err := s.checkGeneration(ctx, id, m.ClusterGeneration); err != nil {
		return Target{}, err
	}
	return Target{ClusterID: id, Mapping: m, Revision: rev, valid: true}, nil
}

// checkGeneration compares a mapping's stored registration generation with
// the cluster's current one: the literal "local" for the local cluster, the
// registration's creation instant otherwise, compared as an instant.
func (s *Service) checkGeneration(ctx context.Context, clusterID, stored string) error {
	if k8s.IsLocalClusterID(clusterID) {
		if stored != k8s.LocalGeneration {
			return fmt.Errorf("%w: generation changed", ErrMappingInvalid)
		}
		return nil
	}
	saved, err := time.Parse(time.RFC3339Nano, stored)
	if err != nil {
		return fmt.Errorf("%w: unreadable generation", ErrMappingInvalid)
	}
	rec, err := s.clusters.Get(ctx, clusterID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%w: cluster no longer registered", ErrMappingInvalid)
	case err != nil:
		return fmt.Errorf("%w: reading cluster registration: %w", ErrStoreUnavailable, err)
	case rec == nil || !rec.CreatedAt.Equal(saved):
		return fmt.Errorf("%w: generation changed", ErrMappingInvalid)
	}
	return nil
}

// observeRevision records the connection revision a read saw. A change drops
// every cached answer and calls onRevisionChange, so neither data nor a
// token from the previous connection is used again. The first revision a
// process sees changes nothing: there is nothing older to drop.
func (s *Service) observeRevision(rev int64) {
	s.mu.Lock()
	prev := s.revision
	if prev == rev {
		s.mu.Unlock()
		return
	}
	s.revision = rev
	clear(s.entries)
	s.mu.Unlock()
	if prev != 0 && s.onRevisionChange != nil {
		s.onRevisionChange()
	}
	if prev != 0 {
		s.logger.Info("rubrik connection changed; dropped cached data", "revision", rev)
	}
}

// Objects returns the mapped cluster's objects. After a failed fetch it
// returns the last good answer marked stale, never an empty list in its
// place (R13, AE6); with no earlier answer it returns the error.
func (s *Service) Objects(ctx context.Context, t Target) (ClusterView, error) {
	if !t.valid {
		return ClusterView{}, ErrMappingInvalid
	}
	rscID := t.Mapping.RSCClusterID
	key := fmt.Sprintf("objects|%d|%s|%s", t.Revision, t.ClusterID, rscID)
	v, f, err := s.cached(ctx, key, t.Revision, true, func(ctx context.Context) (any, error) {
		return s.fetchObjects(ctx, rscID)
	})
	if err != nil {
		return ClusterView{}, err
	}
	view := v.(ClusterView)
	view.Freshness = f
	return view, nil
}

// fetchObjects finds which family the mapped RSC cluster id belongs to and
// lists that family's objects under it.
func (s *Service) fetchObjects(ctx context.Context, rscID string) (ClusterView, error) {
	clusters, err := s.rsc.Clusters(ctx)
	if err != nil {
		return ClusterView{}, err
	}
	var found []Cluster
	for _, c := range clusters {
		if strings.EqualFold(c.ID, rscID) {
			found = append(found, c)
		}
	}
	switch {
	case len(found) == 0:
		return ClusterView{}, ErrTargetMissing
	case len(found) > 1:
		// The families' ids are separate; one id in both cannot be bound
		// to a family, so it binds to none.
		return ClusterView{}, fmt.Errorf("%w: id listed in more than one family", ErrTargetMissing)
	}
	c := found[0]
	var objects []Object
	switch c.Family {
	case FamilyNamespace:
		objects, err = s.rsc.NamespaceObjects(ctx, c.ID)
	case FamilyProtectionSet:
		objects, err = s.rsc.ProtectionSets(ctx, c.ID)
	default:
		return ClusterView{}, fmt.Errorf("%w: unknown family", ErrTargetMissing)
	}
	if err != nil {
		return ClusterView{}, err
	}
	for _, o := range objects {
		if o.Family != c.Family {
			return ClusterView{}, unreadable("object of another family")
		}
	}
	return ClusterView{Cluster: c, Objects: objects}, nil
}

// object resolves objectID among the mapped cluster's objects, so history
// is only ever fetched for an object of the selected cluster.
func (s *Service) object(ctx context.Context, t Target, objectID string) (Object, error) {
	view, err := s.Objects(ctx, t)
	if err != nil {
		return Object{}, err
	}
	o, ok := view.Object(objectID)
	if !ok {
		return Object{}, ErrObjectNotFound
	}
	return o, nil
}

// Snapshots returns the snapshots of one object of the mapped cluster, at
// most limit of them (DefaultSnapshotLimit when limit is 0). The object is
// returned too, so the caller filters the snapshots through a Viewer.
func (s *Service) Snapshots(ctx context.Context, t Target, objectID string, limit int) (Object, []Snapshot, Freshness, error) {
	o, err := s.object(ctx, t, objectID)
	if err != nil {
		return Object{}, nil, Freshness{}, err
	}
	limit = snapshotLimit(limit)
	key := fmt.Sprintf("snapshots|%d|%s|%s|%s|%s|%d", t.Revision, t.ClusterID, t.Mapping.RSCClusterID, o.Family, o.ID, limit)
	v, f, err := s.cached(ctx, key, t.Revision, false, func(ctx context.Context) (any, error) {
		return s.rsc.Snapshots(ctx, o.Family, o.ID, limit)
	})
	if err != nil {
		return Object{}, nil, Freshness{}, err
	}
	return o, v.([]Snapshot), f, nil
}

// Jobs returns the job history of one object of the mapped cluster, at most
// limit entries (DefaultSnapshotLimit when limit is 0). Jobs RSC reports for
// any other object are dropped.
func (s *Service) Jobs(ctx context.Context, t Target, objectID string, limit int) (Object, []Job, Freshness, error) {
	o, err := s.object(ctx, t, objectID)
	if err != nil {
		return Object{}, nil, Freshness{}, err
	}
	limit = snapshotLimit(limit)
	key := fmt.Sprintf("jobs|%d|%s|%s|%s|%d", t.Revision, t.ClusterID, t.Mapping.RSCClusterID, o.ID, limit)
	v, f, err := s.cached(ctx, key, t.Revision, false, func(ctx context.Context) (any, error) {
		jobs, err := s.rsc.Jobs(ctx, ActivityFilter{ObjectIDs: []string{o.ID}, Limit: limit})
		if err != nil {
			return nil, err
		}
		own := make([]Job, 0, len(jobs))
		for _, j := range jobs {
			if strings.EqualFold(j.ObjectID, o.ID) {
				own = append(own, j)
			}
		}
		return own, nil
	})
	if err != nil {
		return Object{}, nil, Freshness{}, err
	}
	return o, v.([]Job), f, nil
}

// SLADomains returns the SLA domains the service account can see, which the
// SLA dialogs offer (R15) and compliance is judged against (KTD5).
func (s *Service) SLADomains(ctx context.Context, t Target) ([]SLADomain, Freshness, error) {
	if !t.valid {
		return nil, Freshness{}, ErrMappingInvalid
	}
	key := fmt.Sprintf("sla|%d", t.Revision)
	v, f, err := s.cached(ctx, key, t.Revision, true, func(ctx context.Context) (any, error) {
		return s.rsc.SLADomains(ctx)
	})
	if err != nil {
		return nil, Freshness{}, err
	}
	return v.([]SLADomain), f, nil
}

// Compliance returns each object's compliance as the poller last persisted
// it (KTD5). An object the poller has not judged yet is unknown; the API
// never shows an instantaneous judgment, so pages and alerts agree. "late"
// is the poller's first miss, still in compliance until a second one.
func (s *Service) Compliance(ctx context.Context, t Target) (func(Object) Compliance, error) {
	if !t.valid {
		return nil, ErrMappingInvalid
	}
	rows, err := s.store.ListComplianceStates(ctx, t.ClusterID)
	if err != nil {
		return nil, fmt.Errorf("%w: reading compliance: %w", ErrStoreUnavailable, err)
	}
	byObject := make(map[string]Compliance, len(rows))
	for _, r := range rows {
		byObject[strings.ToLower(r.RSCObjectID)] = persistedCompliance(r.Status)
	}
	return func(o Object) Compliance {
		if c, ok := byObject[strings.ToLower(o.ID)]; ok {
			return c
		}
		return ComplianceUnknown
	}, nil
}

func persistedCompliance(status string) Compliance {
	switch status {
	case store.RubrikComplianceIn, store.RubrikComplianceLate:
		return ComplianceInCompliance
	case store.RubrikComplianceOut:
		return ComplianceOutOfCompliance
	}
	return ComplianceUnknown
}

// ---------------------------------------------------------------------------
// Cache

// cacheEntry is the latest outcome for one key: a value (possibly an older
// good one, after a failed fetch) and the latest fetch's error.
type cacheEntry struct {
	value     any
	hasValue  bool
	fetchedAt time.Time // when value was fetched
	checkedAt time.Time // when the latest fetch finished
	err       error     // the latest fetch's error; nil after a success
	keep      bool      // never aged out (object lists, SLA domains)
}

type cacheResult struct {
	value any
	fresh Freshness
	err   error
}

func (e *cacheEntry) result(now time.Time) cacheResult {
	if !e.hasValue {
		return cacheResult{err: e.err}
	}
	return cacheResult{
		value: e.value,
		fresh: Freshness{
			FetchedAt: e.fetchedAt,
			Stale:     e.err != nil || now.Sub(e.fetchedAt) > staleAfter,
			Err:       e.err,
		},
	}
}

// cached returns key's entry while it is younger than CacheTTL, and
// otherwise fetches it once for every concurrent caller. rev is the
// connection revision the key was built under; an answer that arrives after
// the revision changed is returned to its callers but not stored.
func (s *Service) cached(ctx context.Context, key string, rev int64, keep bool, fetch func(context.Context) (any, error)) (any, Freshness, error) {
	s.mu.Lock()
	now := s.clock()
	if e := s.entries[key]; e != nil && now.Sub(e.checkedAt) < CacheTTL {
		r := e.result(now)
		s.mu.Unlock()
		return r.value, r.fresh, r.err
	}
	s.mu.Unlock()

	ch := s.flights.DoChan(key, func() (any, error) {
		return s.refresh(ctx, key, rev, keep, fetch), nil
	})
	select {
	case <-ctx.Done():
		return nil, Freshness{}, fmt.Errorf("%w: %w", ErrUnreachable, ctx.Err())
	case r := <-ch:
		res := r.Val.(cacheResult)
		return res.value, res.fresh, res.err
	}
}

// refresh runs one fetch and records its outcome. It runs on the goroutine
// singleflight starts, outside chi's recovery, so a panic is recovered and
// reported as ErrUnreachable; singleflight would otherwise re-raise it where
// nothing can catch it. A failed fetch keeps the older good value, except
// for ErrTargetMissing, which is RSC's answer rather than a failure.
func (s *Service) refresh(ctx context.Context, key string, rev int64, keep bool, fetch func(context.Context) (any, error)) cacheResult {
	res := cacheResult{err: fmt.Errorf("%w: rubrik fetch panicked", ErrUnreachable)}
	recoverutil.Safe(s.logger, "rubrik fetch", func() {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fetchTimeout)
		defer cancel()
		v, err := fetch(fctx)

		s.mu.Lock()
		defer s.mu.Unlock()
		now := s.clock()
		e := &cacheEntry{checkedAt: now, keep: keep}
		if err == nil {
			e.value, e.hasValue, e.fetchedAt = v, true, now
		} else {
			e.err = err
			if old := s.entries[key]; old != nil && old.hasValue && !errors.Is(err, ErrTargetMissing) {
				e.value, e.hasValue, e.fetchedAt = old.value, true, old.fetchedAt
			}
			s.logger.Warn("rubrik fetch failed", "key", cacheKeyKind(key), "error", err)
		}
		if s.revision == rev {
			s.entries[key] = e
			s.prune(now)
		}
		res = e.result(now)
	})
	return res
}

// prune drops history entries nobody has refreshed for historyEntryAge.
// Called with s.mu held.
func (s *Service) prune(now time.Time) {
	for k, e := range s.entries {
		if !e.keep && now.Sub(e.checkedAt) > historyEntryAge {
			delete(s.entries, k)
		}
	}
}

// cacheKeyKind is the first segment of a cache key, for logs.
func cacheKeyKind(key string) string {
	kind, _, _ := strings.Cut(key, "|")
	return kind
}

// ---------------------------------------------------------------------------
// Connection source

// ConnectionStore is the part of store.RubrikStore the client's connection
// source reads.
type ConnectionStore interface {
	GetConnection(ctx context.Context, id string) (store.RubrikConnection, store.RubrikCredentials, error)
	ConnectionRevision(ctx context.Context, id string) (int64, error)
}

// StoreConnection reads the client's connection from the Rubrik store. A
// connection without a usable secret (none stored, or one entered for
// another tenant origin) counts as not configured.
type StoreConnection struct {
	Store ConnectionStore
	ID    string
}

// Connection implements ConnectionSource.
func (c StoreConnection) Connection(ctx context.Context) (Connection, error) {
	conn, creds, err := c.Store.GetConnection(ctx, c.ID)
	if errors.Is(err, store.ErrRubrikConnectionNotFound) {
		return Connection{}, ErrNotConfigured
	}
	if err != nil {
		return Connection{}, err
	}
	if !conn.HasSecret || creds.ClientSecret == "" {
		return Connection{}, fmt.Errorf("%w: no usable client secret", ErrNotConfigured)
	}
	return Connection{
		TenantURL:    conn.TenantURL,
		ClientID:     conn.ClientID,
		ClientSecret: creds.ClientSecret,
		Revision:     conn.Revision,
	}, nil
}

// ConnectionRevision implements ConnectionSource.
func (c StoreConnection) ConnectionRevision(ctx context.Context) (int64, error) {
	rev, err := c.Store.ConnectionRevision(ctx, c.ID)
	if errors.Is(err, store.ErrRubrikConnectionNotFound) {
		return 0, ErrNotConfigured
	}
	return rev, err
}
