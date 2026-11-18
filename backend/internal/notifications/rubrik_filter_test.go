package notifications

// rubrik_filter_test.go — the feed's filter for Rubrik rows (KTD11).
//
// Rubrik rows do not follow the namespace scope the other sources use. An
// admin sees every one. Anyone else sees a Rubrik row only on the local
// cluster, in a namespace the Rubrik visibility provider allows, and only
// when the row is no older than that namespace. The hermetic tests pin how
// the handler builds that scope and how the store turns it into SQL. The
// PostgreSQL-backed tests run the predicate against real rows for list,
// unread count, mark-read and mark-all-read, behind the
// KUBECENTER_TEST_DATABASE_URL gate (see store_dedup_test.go).

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
)

// fakeRubrikVisibility answers AllowedNamespaces from a table and records
// what it was asked.
type fakeRubrikVisibility struct {
	allowed map[string]time.Time
	admin   bool
	err     error

	calls      int
	cluster    string
	candidates []string
}

func (f *fakeRubrikVisibility) AllowedNamespaces(_ context.Context, _ *auth.User, clusterID string, candidates []string) (map[string]time.Time, bool, error) {
	f.calls++
	f.cluster = clusterID
	f.candidates = candidates
	if f.err != nil {
		return nil, false, f.err
	}
	return f.allowed, f.admin, nil
}

var (
	rubrikAdmin  = &auth.User{Username: "root", KubernetesUsername: "root", Roles: []string{"admin"}}
	rubrikReader = &auth.User{Username: "alice", KubernetesUsername: "alice", Roles: []string{"user"}}
)

func rubrikHandler(p RubrikVisibility) *Handler {
	return &Handler{Rubrik: p, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// candidatesOf returns a candidate source that answers names, and counts
// how often it was asked.
func candidatesOf(names []string, err error, calls *int) func(context.Context) ([]string, error) {
	return func(context.Context) ([]string, error) {
		*calls++
		return names, err
	}
}

func TestRubrikScope_AdminSeesEveryRowWithoutAsking(t *testing.T) {
	p := &fakeRubrikVisibility{}
	var calls int
	got := rubrikHandler(p).rubrikScopeFrom(t.Context(), rubrikAdmin, candidatesOf([]string{"team-a"}, nil, &calls))
	if !got.All {
		t.Fatalf("admin scope = %+v, want every Rubrik row", got)
	}
	if calls != 0 || p.calls != 0 {
		t.Errorf("admin scope asked for candidates %d times and the provider %d times, want 0", calls, p.calls)
	}
}

func TestRubrikScope_AllowedNamespacesFromTheProvider(t *testing.T) {
	createdA := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	createdB := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	p := &fakeRubrikVisibility{allowed: map[string]time.Time{"team-b": createdB, "team-a": createdA}}
	var calls int
	got := rubrikHandler(p).rubrikScopeFrom(t.Context(), rubrikReader,
		candidatesOf([]string{"team-a", "team-b", "team-c"}, nil, &calls))

	want := RubrikScope{Namespaces: []RubrikNamespace{{"team-a", createdA}, {"team-b", createdB}}}
	if got.All || !slices.Equal(got.Namespaces, want.Namespaces) {
		t.Fatalf("scope = %+v, want %+v", got, want)
	}
	// Non-admin Rubrik access exists only on the local cluster (R10).
	if p.cluster != k8s.LocalClusterID {
		t.Errorf("provider asked about cluster %q, want %q", p.cluster, k8s.LocalClusterID)
	}
	if !slices.Equal(p.candidates, []string{"team-a", "team-b", "team-c"}) {
		t.Errorf("provider asked about %v, want the feed's candidates", p.candidates)
	}
}

// Every path that cannot answer hides Rubrik rows: the zero scope. None of
// them is an error, so the rest of the feed is still served.
func TestRubrikScope_FailsClosed(t *testing.T) {
	var nilAccess *fakeRubrikVisibility
	for name, c := range map[string]struct {
		provider   RubrikVisibility
		candidates []string
		listErr    error
	}{
		"no provider": {provider: nil, candidates: []string{"team-a"}},
		// An interface holding a nil pointer is not nil; calling it panics.
		"nil pointer provider": {provider: nilAccess, candidates: []string{"team-a"}},
		"candidates failed":    {provider: &fakeRubrikVisibility{allowed: map[string]time.Time{"team-a": time.Now()}}, listErr: errors.New("db down")},
		"provider failed":      {provider: &fakeRubrikVisibility{err: errors.New("sar failed")}, candidates: []string{"team-a"}},
		// The handler's own admin check already said no.
		"provider claims admin": {provider: &fakeRubrikVisibility{admin: true}, candidates: []string{"team-a"}},
		"nothing allowed":       {provider: &fakeRubrikVisibility{allowed: map[string]time.Time{}}, candidates: []string{"team-a"}},
	} {
		t.Run(name, func(t *testing.T) {
			var calls int
			got := rubrikHandler(c.provider).rubrikScopeFrom(t.Context(), rubrikReader, candidatesOf(c.candidates, c.listErr, &calls))
			if got.All || len(got.Namespaces) != 0 {
				t.Fatalf("scope = %+v, want none", got)
			}
		})
	}
}

// With no Rubrik row to place, the provider is not asked.
func TestRubrikScope_NoCandidatesAsksNothing(t *testing.T) {
	p := &fakeRubrikVisibility{allowed: map[string]time.Time{"team-a": time.Now()}}
	var calls int
	got := rubrikHandler(p).rubrikScopeFrom(t.Context(), rubrikReader, candidatesOf(nil, nil, &calls))
	if got.All || len(got.Namespaces) != 0 || p.calls != 0 {
		t.Fatalf("scope = %+v after %d provider calls, want none and no call", got, p.calls)
	}
}

func TestRubrikClause(t *testing.T) {
	if clause, args, ok := rubrikClause(RubrikScope{All: true}, 2); ok || clause != "" || args != nil {
		t.Fatalf("all: got %q %v ok=%v, want no predicate", clause, args, ok)
	}

	created := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	clause, args, ok := rubrikClause(RubrikScope{Namespaces: []RubrikNamespace{
		{"team-a", created},
		{"", created},           // no name: names nothing
		{"team-b", time.Time{}}, // no creation time: cannot place older rows
	}}, 5)
	if !ok {
		t.Fatal("a non-admin scope added no predicate")
	}
	for _, want := range []string{"n.resource_ns <> ''", "n.cluster_id = $5", "unnest($6::text[], $7::timestamptz[])", "n.created_at >= v.since"} {
		if !strings.Contains(clause, want) {
			t.Errorf("clause %q lacks %q", clause, want)
		}
	}
	if len(args) != 3 || args[0] != k8s.LocalClusterID {
		t.Fatalf("args = %v, want the local cluster id, names and times", args)
	}
	if names := args[1].([]string); !slices.Equal(names, []string{"team-a"}) {
		t.Errorf("names = %v, want [team-a]", names)
	}
	if since := args[2].([]time.Time); len(since) != 1 || !since[0].Equal(created) {
		t.Errorf("since = %v, want [%v]", since, created)
	}

	// The zero scope still adds the predicate, over empty (not NULL) arrays.
	_, args, ok = rubrikClause(RubrikScope{}, 2)
	if !ok || args[1].([]string) == nil || args[2].([]time.Time) == nil {
		t.Fatalf("zero scope: ok=%v args=%v, want a predicate over empty arrays", ok, args)
	}
}

func TestFeedClause(t *testing.T) {
	if clause, args, ok := feedClause(FeedScope{AllNamespaces: true, Rubrik: RubrikScope{All: true}}, 2); ok || clause != "" || args != nil {
		t.Fatalf("admin: got %q %v ok=%v, want no predicate", clause, args, ok)
	}

	// Non-Rubrik rows keep the namespace clause at the first index, and
	// Rubrik rows get their own clause after it.
	clause, args, ok := feedClause(FeedScope{Namespaces: []string{"team-a"}}, 2)
	if !ok {
		t.Fatal("non-admin scope added no predicate")
	}
	nsClause, _, _ := namespaceClause(false, []string{"team-a"}, 2)
	rubrik, _, _ := rubrikClause(RubrikScope{}, 3)
	want := "((n.source <> 'rubrik' AND " + nsClause + ") OR (n.source = 'rubrik' AND " + rubrik + "))"
	if clause != want {
		t.Errorf("clause = %q\nwant %q", clause, want)
	}
	if len(args) != 4 || !slices.Equal(args[0].([]string), []string{"team-a"}) {
		t.Errorf("args = %v, want the namespaces then the three Rubrik values", args)
	}

	// An admin's namespace scope with no Rubrik scope still hides Rubrik
	// rows: the two are decided separately.
	clause, args, ok = feedClause(FeedScope{AllNamespaces: true}, 2)
	if !ok || !strings.HasPrefix(clause, "((n.source <> 'rubrik' AND TRUE) OR (n.source = 'rubrik' AND (n.resource_ns") || len(args) != 3 {
		t.Errorf("all namespaces, no Rubrik scope: got %q %v ok=%v", clause, args, ok)
	}
}

// ---------------------------------------------------------------------------
// PostgreSQL-backed.
// ---------------------------------------------------------------------------

// rubrikRows inserts one Rubrik row of each kind the filter tells apart,
// in namespaces unique to this run, plus a diagnostic row in the first one.
type rubrikRows struct {
	nsA, nsB, nsRemote string
	// local is in nsA with an empty cluster id, local2 in nsB with "local".
	local, local2, remote, noNamespace, diagnostic string
}

func insertRubrikRows(t *testing.T, s *Store) rubrikRows {
	t.Helper()
	r := rubrikRows{nsA: uniqueName(t), nsB: uniqueName(t), nsRemote: uniqueName(t)}
	insert := func(source Source, ns, cluster string) string {
		n := baseNotification(t)
		n.Source, n.ResourceNS, n.ClusterID = source, ns, cluster
		return mustInsert(t, s, n)
	}
	r.local = insert(SourceRubrik, r.nsA, "")
	r.local2 = insert(SourceRubrik, r.nsB, k8s.LocalClusterID)
	r.remote = insert(SourceRubrik, r.nsRemote, "remote-1")
	r.noNamespace = insert(SourceRubrik, "", "")
	r.diagnostic = insert(SourceDiagnostic, r.nsA, "")
	return r
}

var everyRow = FeedScope{AllNamespaces: true, Rubrik: RubrikScope{All: true}}

// listedIDs returns the ids of rows user sees under scope, among the
// Rubrik and diagnostic rows created since. Listing one source at a time
// keeps other packages' rows in the shared database out of the page.
func listedIDs(t *testing.T, s *Store, user string, scope FeedScope, since time.Time) map[string]bool {
	t.Helper()
	ids := map[string]bool{}
	for _, source := range []Source{SourceRubrik, SourceDiagnostic} {
		got, _, err := s.ListNotifications(t.Context(), ListOpts{
			UserID: user, Namespaces: scope.Namespaces, AllNamespaces: scope.AllNamespaces, Rubrik: scope.Rubrik,
			Source: source, Since: since, Limit: 200,
		})
		if err != nil {
			t.Fatalf("ListNotifications: %v", err)
		}
		for _, n := range got {
			ids[n.ID] = true
		}
	}
	return ids
}

// readIDs returns which of ids user has read.
func readIDs(t *testing.T, s *Store, user string, ids ...string) map[string]bool {
	t.Helper()
	rows, err := s.pool.Query(t.Context(),
		"SELECT notification_id::text FROM nc_reads WHERE user_id = $1 AND notification_id = ANY($2::uuid[])", user, ids)
	if err != nil {
		t.Fatalf("reading nc_reads: %v", err)
	}
	defer rows.Close()
	read := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scanning nc_reads: %v", err)
		}
		read[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading nc_reads: %v", err)
	}
	return read
}

// all returns every row id r holds.
func (r rubrikRows) all() []string {
	return []string{r.local, r.local2, r.remote, r.noNamespace, r.diagnostic}
}

// createdAt returns the stored creation time of row id.
func createdAt(t *testing.T, s *Store, id string) time.Time {
	t.Helper()
	var at time.Time
	if err := s.pool.QueryRow(t.Context(), "SELECT created_at FROM nc_notifications WHERE id = $1", id).Scan(&at); err != nil {
		t.Fatalf("reading created_at: %v", err)
	}
	return at
}

func TestFeed_RubrikRows(t *testing.T) {
	s := testNotifStore(t)
	since := time.Now().Add(-time.Minute)
	r := insertRubrikRows(t, s)
	at := createdAt(t, s, r.local)
	past := at.Add(-time.Hour)
	both := RubrikScope{Namespaces: []RubrikNamespace{{r.nsA, past}, {r.nsB, past}}}

	for _, tc := range []struct {
		name  string
		scope FeedScope
		want  []string
	}{
		{name: "admin", scope: everyRow,
			want: []string{r.local, r.local2, r.remote, r.noNamespace, r.diagnostic}},
		// AE7: namespace access without the Rubrik grant shows no Rubrik row,
		// and the diagnostic row in the same namespace is listed as before.
		{name: "namespace access, no grant", scope: FeedScope{Namespaces: []string{r.nsA, r.nsRemote}},
			want: []string{r.diagnostic}},
		{name: "grant on both namespaces", scope: FeedScope{Namespaces: []string{}, Rubrik: both},
			want: []string{r.local, r.local2}},
		// A remote cluster's row and a row with no namespace are admin-only,
		// even when the namespace name is allowed.
		{name: "grant names the remote row's namespace", scope: FeedScope{Rubrik: RubrikScope{Namespaces: []RubrikNamespace{{r.nsRemote, past}}}},
			want: nil},
		// A row created exactly when the namespace was is the namespace's.
		{name: "namespace created with the row", scope: FeedScope{Rubrik: RubrikScope{Namespaces: []RubrikNamespace{{r.nsA, at}}}},
			want: []string{r.local}},
		// The row predates the namespace that exists now, so an earlier
		// namespace of the same name raised it (R6).
		{name: "namespace created after the row", scope: FeedScope{Rubrik: RubrikScope{Namespaces: []RubrikNamespace{{r.nsA, at.Add(time.Microsecond)}}}},
			want: nil},
		// An admin's namespace scope does not reach Rubrik rows on its own.
		{name: "all namespaces, no Rubrik scope", scope: FeedScope{AllNamespaces: true},
			want: []string{r.diagnostic}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ids := listedIDs(t, s, uniqueName(t), tc.scope, since)
			for _, id := range []string{r.local, r.local2, r.remote, r.noNamespace, r.diagnostic} {
				if ids[id] != slices.Contains(tc.want, id) {
					t.Errorf("row %s listed=%v, want %v", id, ids[id], slices.Contains(tc.want, id))
				}
			}
		})
	}
}

// The unread count follows the list: granting nsA and nsB adds exactly the
// two local Rubrik rows. Rows outside this test move every count equally,
// so compare differences.
func TestFeed_RubrikUnreadCountMatchesTheList(t *testing.T) {
	s := testNotifStore(t)
	r := insertRubrikRows(t, s)
	past := createdAt(t, s, r.local).Add(-time.Hour)
	reader := uniqueName(t)
	count := func(scope FeedScope) int {
		t.Helper()
		c, err := s.UnreadCount(t.Context(), reader, scope)
		if err != nil {
			t.Fatalf("UnreadCount: %v", err)
		}
		return c
	}

	noGrant := count(FeedScope{Namespaces: []string{r.nsA, r.nsRemote}})
	none := count(FeedScope{})
	if noGrant-none != 1 {
		t.Errorf("namespace access counted %d rows beyond the empty scope, want 1 (the diagnostic row only)", noGrant-none)
	}
	granted := count(FeedScope{Rubrik: RubrikScope{Namespaces: []RubrikNamespace{{r.nsA, past}, {r.nsB, past}, {r.nsRemote, past}}}})
	if granted-none != 2 {
		t.Errorf("the grant counted %d rows, want 2 (the remote row is admin-only)", granted-none)
	}
	if admin := count(everyRow); admin-granted < 3 {
		t.Errorf("admin counted %d rows beyond the grant, want at least the remote, namespace-less and diagnostic rows", admin-granted)
	}
}

func TestFeed_RubrikMarkAllReadLeavesHiddenRowsUnread(t *testing.T) {
	s := testNotifStore(t)
	r := insertRubrikRows(t, s)
	past := createdAt(t, s, r.local).Add(-time.Hour)
	reader := uniqueName(t)

	if err := s.MarkAllRead(t.Context(), reader, RubrikScope{Namespaces: []RubrikNamespace{{r.nsA, past}}}); err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	read := readIDs(t, s, reader, r.all()...)
	for id, want := range map[string]bool{
		r.local: true, r.diagnostic: true, // visible to the reader
		r.local2: false, r.remote: false, r.noNamespace: false, // hidden
	} {
		if read[id] != want {
			t.Errorf("row %s read=%v, want %v", id, read[id], want)
		}
	}
}

func TestFeed_RubrikMarkRead(t *testing.T) {
	s := testNotifStore(t)
	r := insertRubrikRows(t, s)
	past := createdAt(t, s, r.local).Add(-time.Hour)
	scopeA := RubrikScope{Namespaces: []RubrikNamespace{{r.nsA, past}}}
	reader := uniqueName(t)

	// A hidden row is not marked, and the call still succeeds, so the
	// answer does not tell the caller the row exists.
	for _, id := range []string{r.local2, r.remote, r.noNamespace} {
		if err := s.MarkRead(t.Context(), reader, id, scopeA); err != nil {
			t.Fatalf("MarkRead(hidden %s): %v", id, err)
		}
	}
	for _, id := range []string{r.local, r.diagnostic} {
		if err := s.MarkRead(t.Context(), reader, id, scopeA); err != nil {
			t.Fatalf("MarkRead(visible %s): %v", id, err)
		}
	}
	read := readIDs(t, s, reader, r.all()...)
	for id, want := range map[string]bool{r.local: true, r.diagnostic: true, r.local2: false, r.remote: false, r.noNamespace: false} {
		if read[id] != want {
			t.Errorf("row %s read=%v, want %v", id, read[id], want)
		}
	}

	// An admin marks any Rubrik row.
	if err := s.MarkRead(t.Context(), reader, r.remote, RubrikScope{All: true}); err != nil {
		t.Fatalf("MarkRead(admin): %v", err)
	}
	if !readIDs(t, s, reader, r.remote)[r.remote] {
		t.Error("an admin's mark-read left the remote row unread")
	}

	// An id that names no row still fails, as it did before the filter.
	if err := s.MarkRead(t.Context(), reader, "00000000-0000-4000-8000-000000000000", scopeA); err == nil {
		t.Error("MarkRead of an unknown id succeeded, want the foreign-key error")
	}
}

func TestRubrikFeedNamespaces(t *testing.T) {
	s := testNotifStore(t)
	r := insertRubrikRows(t, s)
	names, err := s.RubrikFeedNamespaces(t.Context())
	if err != nil {
		t.Fatalf("RubrikFeedNamespaces: %v", err)
	}
	if !slices.Contains(names, r.nsA) || !slices.Contains(names, r.nsB) {
		t.Errorf("candidates lack the local Rubrik rows' namespaces %s, %s", r.nsA, r.nsB)
	}
	// A remote row and a row with no namespace are admin-only, so they name
	// no candidate.
	if slices.Contains(names, r.nsRemote) || slices.Contains(names, "") {
		t.Errorf("candidates %v include a remote or empty namespace", names)
	}
}

// Each feed endpoint hands the store the Rubrik scope it computed for the
// caller. The store tests take a scope directly, so without this a handler
// that dropped the scope, or passed an admin's, would fail no test.
func TestFeedHandlers_ApplyTheCallersRubrikScope(t *testing.T) {
	s := testNotifStore(t)
	since := time.Now().Add(-time.Minute)
	r := insertRubrikRows(t, s)
	past := createdAt(t, s, r.local).Add(-time.Hour)

	// A non-admin with no namespace access and the Rubrik grant on nsA.
	h := scopeHandler(t, nil, namespaceLister(t))
	h.Service = &NotificationService{store: s}
	granted := &fakeRubrikVisibility{allowed: map[string]time.Time{r.nsA: past}}
	reader := uniqueName(t)
	user := &auth.User{Username: reader, KubernetesUsername: reader, Roles: []string{"user"}}

	serve := func(handle http.HandlerFunc, method, target, id string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, nil)
		ctx := auth.ContextWithUser(req.Context(), user)
		if id != "" {
			rc := chi.NewRouteContext()
			rc.URLParams.Add("id", id)
			ctx = context.WithValue(ctx, chi.RouteCtxKey, rc)
		}
		rec := httptest.NewRecorder()
		handle(rec, req.WithContext(ctx))
		return rec
	}

	// List.
	h.Rubrik = granted
	q := url.Values{"source": {string(SourceRubrik)}, "since": {since.UTC().Format(time.RFC3339)}, "limit": {"200"}}
	rec := serve(h.HandleList, http.MethodGet, "/notifications?"+q.Encode(), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status %d, want 200", rec.Code)
	}
	var list struct{ Data []Notification }
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decoding list: %v", err)
	}
	listed := map[string]bool{}
	for _, n := range list.Data {
		listed[n.ID] = true
	}
	for id, want := range map[string]bool{r.local: true, r.local2: false, r.remote: false, r.noNamespace: false} {
		if listed[id] != want {
			t.Errorf("list: row %s listed=%v, want %v", id, listed[id], want)
		}
	}

	// Unread count: the grant adds exactly nsA's row to what the same user
	// counts with no provider. Rows outside this test move both counts.
	unread := func() int {
		t.Helper()
		rec := serve(h.HandleUnreadCount, http.MethodGet, "/notifications/unread-count", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("unread count: status %d, want 200", rec.Code)
		}
		var body struct{ Data struct{ Count int } }
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decoding unread count: %v", err)
		}
		return body.Data.Count
	}
	h.Rubrik = nil
	without := unread()
	h.Rubrik = granted
	if with := unread(); with-without != 1 {
		t.Errorf("unread count with the grant is %d more than without, want 1", with-without)
	}

	// Mark-read: a hidden row is not marked, a visible one is.
	for _, id := range []string{r.local2, r.local} {
		if rec := serve(h.HandleMarkRead, http.MethodPost, "/notifications/"+id+"/read", id); rec.Code != http.StatusNoContent {
			t.Fatalf("mark-read %s: status %d, want 204", id, rec.Code)
		}
	}
	if read := readIDs(t, s, reader, r.local, r.local2); !read[r.local] || read[r.local2] {
		t.Errorf("mark-read: read=%v, want only %s", read, r.local)
	}

	// Mark-all-read, by a second user with the same grant.
	reader2 := uniqueName(t)
	user = &auth.User{Username: reader2, KubernetesUsername: reader2, Roles: []string{"user"}}
	if rec := serve(h.HandleMarkAllRead, http.MethodPost, "/notifications/read-all", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("mark-all-read: status %d, want 204", rec.Code)
	}
	read := readIDs(t, s, reader2, r.all()...)
	for id, want := range map[string]bool{r.local: true, r.diagnostic: true, r.local2: false, r.remote: false, r.noNamespace: false} {
		if read[id] != want {
			t.Errorf("mark-all-read: row %s read=%v, want %v", id, read[id], want)
		}
	}
}
