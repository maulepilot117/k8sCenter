package store

// eso_history_test.go — coverage for ESOHistoryStore and migration 000020
// (Release B, U13).
//
// Two layers:
//
//   - hermetic tests (cursor codec, limit clamp) always run;
//   - PostgreSQL-backed tests obtain their pool from testDB(t) and skip unless
//     KUBECENTER_TEST_DATABASE_URL is set. Every row they write carries a
//     cluster id and uid from testOwnerID(t), so residue from a failed run can
//     never collide with another test's rows (see the isolation contract in
//     testdb_test.go).
//
// To run the gated layer locally:
//
//	make dev-db
//	KUBECENTER_TEST_DATABASE_URL='postgresql://k8scenter:k8scenter@127.0.0.1:5432/<scratch db>?sslmode=disable' \
//	    go test ./internal/store/ -run ESOHistory
//
// Point it at a throwaway database, never the one the dev backend uses.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Hermetic tests — always on, no database required.
// ---------------------------------------------------------------------------

func TestEncodeDecodeESOHistoryCursor_RoundTrip(t *testing.T) {
	tests := []ESOHistoryCursor{
		{AttemptAt: time.Unix(0, 0).UTC(), ID: 1},
		{AttemptAt: time.Date(2026, 9, 22, 13, 4, 5, 123456000, time.UTC), ID: 42},
		{AttemptAt: time.UnixMicro(maxESOHistoryCursorMicros).UTC(), ID: 1<<63 - 1},
	}
	for _, want := range tests {
		enc := EncodeESOHistoryCursor(want)
		if strings.ContainsAny(enc, "=+/") {
			t.Errorf("cursor %q is not unpadded base64url", enc)
		}
		got, err := DecodeESOHistoryCursor(enc)
		if err != nil {
			t.Fatalf("DecodeESOHistoryCursor(%q) error: %v", enc, err)
		}
		if !got.AttemptAt.Equal(want.AttemptAt) || got.ID != want.ID {
			t.Errorf("round trip = %+v; want %+v", got, want)
		}
	}
}

// Sub-microsecond precision is not representable in TIMESTAMPTZ, so the codec
// truncates it. Stored values never carry it; this pins the behaviour for a
// caller that builds a cursor from a Go time.
func TestEncodeESOHistoryCursor_TruncatesToMicroseconds(t *testing.T) {
	at := time.Date(2026, 9, 22, 0, 0, 0, 123456789, time.UTC)
	got, err := DecodeESOHistoryCursor(EncodeESOHistoryCursor(ESOHistoryCursor{AttemptAt: at, ID: 7}))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if want := at.Truncate(time.Microsecond); !got.AttemptAt.Equal(want) {
		t.Errorf("AttemptAt = %v; want %v", got.AttemptAt, want)
	}
}

func TestDecodeESOHistoryCursor_Rejects(t *testing.T) {
	enc := func(raw string) string { return base64.RawURLEncoding.EncodeToString([]byte(raw)) }
	tests := []struct {
		name   string
		cursor string
	}{
		{"empty", ""},
		{"not base64", "abc"},
		{"base64 alphabet violation", "!!!!"},
		{"padded std base64", base64.StdEncoding.EncodeToString([]byte("1:12"))},
		{"no colon", enc("12345")},
		{"two colons", enc("1:2:3")},
		{"non-numeric micros", enc("x:1")},
		{"non-numeric id", enc("1:x")},
		{"empty micros", enc(":1")},
		{"empty id", enc("1:")},
		{"negative micros", enc("-1:1")},
		{"zero id", enc("1:0")},
		{"negative id", enc("1:-5")},
		{"micros past 2100", enc("4102444800000001:1")},
		{"year 3000 micros", enc("32503680000000000:1")},
		{"id overflows int64", enc("1:9223372036854775808")},
		{"invalid utf-8", enc("1:1\xff")},
		{"4 KiB blob", enc(strings.Repeat("1", 4096))},
		{"just over the byte cap", enc(strings.Repeat("0", maxESOHistoryCursorBytes-2) + ":1" + "1")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeESOHistoryCursor(tc.cursor)
			if !errors.Is(err, ErrInvalidESOHistoryCursor) {
				t.Fatalf("DecodeESOHistoryCursor(%q) = (%+v, %v); want ErrInvalidESOHistoryCursor", tc.cursor, got, err)
			}
			if got != (ESOHistoryCursor{}) {
				t.Errorf("rejected cursor returned a non-zero value %+v", got)
			}
		})
	}
}

// The byte cap must not reject a legitimate cursor: the widest one the
// encoder can produce has to fit.
func TestDecodeESOHistoryCursor_WidestValidCursorFits(t *testing.T) {
	widest := EncodeESOHistoryCursor(ESOHistoryCursor{
		AttemptAt: time.UnixMicro(maxESOHistoryCursorMicros),
		ID:        1<<63 - 1,
	})
	if _, err := DecodeESOHistoryCursor(widest); err != nil {
		t.Fatalf("widest valid cursor %q rejected: %v", widest, err)
	}
}

func TestClampESOHistoryLimit(t *testing.T) {
	tests := []struct{ in, want int }{
		{-1, defaultESOHistoryLimit},
		{0, defaultESOHistoryLimit},
		{1, 1},
		{50, 50},
		{maxESOHistoryLimit, maxESOHistoryLimit},
		{maxESOHistoryLimit + 1, maxESOHistoryLimit},
		{1 << 30, maxESOHistoryLimit},
	}
	for _, tc := range tests {
		if got := clampESOHistoryLimit(tc.in); got != tc.want {
			t.Errorf("clampESOHistoryLimit(%d) = %d; want %d", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// historyBase is an arbitrary fixed instant, microsecond-aligned so values
// survive the TIMESTAMPTZ round trip unchanged.
var historyBase = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func historyEntry(clusterID, uid string, at time.Time) ESOSyncHistoryEntry {
	return ESOSyncHistoryEntry{
		ClusterID:       clusterID,
		UID:             uid,
		Namespace:       "apps",
		Name:            "db-credentials",
		AttemptAt:       at,
		Outcome:         "success",
		DiffKeysAdded:   []string{},
		DiffKeysRemoved: []string{},
		DiffKeysChanged: []string{},
	}
}

func mustInsertHistory(t *testing.T, s *ESOHistoryStore, e ESOSyncHistoryEntry) {
	t.Helper()
	if err := s.Insert(t.Context(), e); err != nil {
		t.Fatalf("Insert(%s/%s @ %s): %v", e.ClusterID, e.UID, e.AttemptAt, err)
	}
}

// collectAllPages walks every page at the given size and fails the test on a
// page-size violation or a runaway walk.
func collectAllPages(t *testing.T, s *ESOHistoryStore, clusterID, uid string, limit int) []ESOSyncHistoryEntry {
	t.Helper()
	var (
		all   []ESOSyncHistoryEntry
		after *ESOHistoryCursor
	)
	for range 1000 {
		page, err := s.QueryPage(t.Context(), clusterID, uid, after, limit)
		if err != nil {
			t.Fatalf("QueryPage: %v", err)
		}
		if len(page.Entries) > limit {
			t.Fatalf("page has %d entries; limit %d", len(page.Entries), limit)
		}
		all = append(all, page.Entries...)
		if page.NextCursor == "" {
			return all
		}
		c, err := DecodeESOHistoryCursor(page.NextCursor)
		if err != nil {
			t.Fatalf("server produced an undecodable cursor %q: %v", page.NextCursor, err)
		}
		after = &c
	}
	t.Fatal("pagination did not terminate after 1000 pages")
	return nil
}

// ---------------------------------------------------------------------------
// PostgreSQL-backed tests
// ---------------------------------------------------------------------------

// The same UID in two clusters is two objects. Neither reader may mix them.
func TestESOHistory_QueryPage_ClusterIsolation(t *testing.T) {
	s := NewESOHistoryStore(testDB(t))
	clusterA, clusterB := testOwnerID(t), testOwnerID(t)
	uid := testOwnerID(t)

	for i := range 3 {
		mustInsertHistory(t, s, historyEntry(clusterA, uid, historyBase.Add(time.Duration(i)*time.Minute)))
	}
	mustInsertHistory(t, s, historyEntry(clusterB, uid, historyBase.Add(time.Hour)))

	pageA, err := s.QueryPage(t.Context(), clusterA, uid, nil, 50)
	if err != nil {
		t.Fatalf("QueryPage(A): %v", err)
	}
	if len(pageA.Entries) != 3 {
		t.Fatalf("cluster A returned %d entries; want 3", len(pageA.Entries))
	}
	for _, e := range pageA.Entries {
		if e.ClusterID != clusterA {
			t.Errorf("cluster A page contains a row from %q", e.ClusterID)
		}
	}

	latestA, err := s.LatestByClusterUID(t.Context(), clusterA, uid)
	if err != nil {
		t.Fatalf("LatestByClusterUID(A): %v", err)
	}
	// Cluster B's row is the newest overall; a cluster-blind reader returns it.
	if latestA == nil || latestA.ClusterID != clusterA || !latestA.AttemptAt.Equal(historyBase.Add(2*time.Minute)) {
		t.Errorf("LatestByClusterUID(A) = %+v; want cluster A's row at +2m", latestA)
	}

	pageB, err := s.QueryPage(t.Context(), clusterB, uid, nil, 50)
	if err != nil {
		t.Fatalf("QueryPage(B): %v", err)
	}
	if len(pageB.Entries) != 1 || pageB.Entries[0].ClusterID != clusterB {
		t.Errorf("cluster B page = %+v; want its single row", pageB.Entries)
	}
}

// A deleted and recreated ExternalSecret keeps its name and gets a new UID.
// The new object must not inherit the old one's evidence.
func TestESOHistory_QueryPage_SameNameDistinctUID(t *testing.T) {
	s := NewESOHistoryStore(testDB(t))
	cluster := testOwnerID(t)
	oldUID, newUID := testOwnerID(t), testOwnerID(t)

	mustInsertHistory(t, s, historyEntry(cluster, oldUID, historyBase))
	mustInsertHistory(t, s, historyEntry(cluster, oldUID, historyBase.Add(time.Minute)))
	mustInsertHistory(t, s, historyEntry(cluster, newUID, historyBase.Add(time.Hour)))

	page, err := s.QueryPage(t.Context(), cluster, newUID, nil, 50)
	if err != nil {
		t.Fatalf("QueryPage: %v", err)
	}
	if len(page.Entries) != 1 || page.Entries[0].UID != newUID {
		t.Fatalf("recreated object's history = %+v; want only its own row", page.Entries)
	}
}

// Newest first, strictly, across pages, with the cursor only on full pages.
func TestESOHistory_QueryPage_OrderAndCursor(t *testing.T) {
	s := NewESOHistoryStore(testDB(t))
	cluster, uid := testOwnerID(t), testOwnerID(t)

	const n = 5
	for i := range n {
		mustInsertHistory(t, s, historyEntry(cluster, uid, historyBase.Add(time.Duration(i)*time.Second)))
	}

	first, err := s.QueryPage(t.Context(), cluster, uid, nil, 2)
	if err != nil {
		t.Fatalf("QueryPage: %v", err)
	}
	if len(first.Entries) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %d entries, cursor %q; want 2 and a cursor", len(first.Entries), first.NextCursor)
	}
	if !first.Entries[0].AttemptAt.Equal(historyBase.Add(4 * time.Second)) {
		t.Errorf("first entry at %v; want the newest (+4s)", first.Entries[0].AttemptAt)
	}

	all := collectAllPages(t, s, cluster, uid, 2)
	if len(all) != n {
		t.Fatalf("walked %d entries; want %d", len(all), n)
	}
	for i := 1; i < len(all); i++ {
		if !all[i].AttemptAt.Before(all[i-1].AttemptAt) {
			t.Errorf("entry %d at %v is not older than entry %d at %v", i, all[i].AttemptAt, i-1, all[i-1].AttemptAt)
		}
	}

	// A short page carries no cursor.
	short, err := s.QueryPage(t.Context(), cluster, uid, nil, n+1)
	if err != nil {
		t.Fatalf("QueryPage: %v", err)
	}
	if len(short.Entries) != n || short.NextCursor != "" {
		t.Errorf("short page = %d entries, cursor %q; want %d and no cursor", len(short.Entries), short.NextCursor, n)
	}
}

// The keyset compares the (attempt_at, id) tuple, not attempt_at alone, so a
// cursor sitting on a stored row's timestamp includes or excludes that row by
// id rather than by timestamp.
func TestESOHistory_QueryPage_EqualTimestampsDeterministic(t *testing.T) {
	s := NewESOHistoryStore(testDB(t))
	cluster := testOwnerID(t)

	// The dedup index forbids two rows with one (cluster, uid, attempt_at), so
	// a real history cannot hold the three equal-timestamp rows the plan
	// sketched. What the id tiebreaker still governs is a cursor whose
	// timestamp equals a stored row's: the tuple comparison, not the
	// timestamp alone, decides whether that row is on the next page.
	uid := testOwnerID(t)
	mustInsertHistory(t, s, historyEntry(cluster, uid, historyBase))
	mustInsertHistory(t, s, historyEntry(cluster, uid, historyBase.Add(-time.Second)))

	page, err := s.QueryPage(t.Context(), cluster, uid, nil, 50)
	if err != nil {
		t.Fatalf("QueryPage: %v", err)
	}
	if len(page.Entries) != 2 {
		t.Fatalf("got %d entries; want 2", len(page.Entries))
	}
	newest := page.Entries[0]

	// Same timestamp, id one above the stored row: the stored row sorts after
	// the cursor and must be included.
	after := &ESOHistoryCursor{AttemptAt: newest.AttemptAt, ID: newest.ID + 1}
	got, err := s.QueryPage(t.Context(), cluster, uid, after, 50)
	if err != nil {
		t.Fatalf("QueryPage(after id+1): %v", err)
	}
	if len(got.Entries) != 2 || got.Entries[0].ID != newest.ID {
		t.Errorf("after (t, id+1) = %d entries starting at id %d; want both rows starting at %d",
			len(got.Entries), firstID(got.Entries), newest.ID)
	}

	// Same timestamp, same id: the row itself is excluded, the older one kept.
	after = &ESOHistoryCursor{AttemptAt: newest.AttemptAt, ID: newest.ID}
	got, err = s.QueryPage(t.Context(), cluster, uid, after, 50)
	if err != nil {
		t.Fatalf("QueryPage(after id): %v", err)
	}
	if len(got.Entries) != 1 || got.Entries[0].ID == newest.ID {
		t.Errorf("after (t, id) = %+v; want only the older row", got.Entries)
	}
}

func firstID(es []ESOSyncHistoryEntry) int64 {
	if len(es) == 0 {
		return 0
	}
	return es[0].ID
}

// A cursor is unauthenticated by design, so a forged one must be harmless: a
// well-formed cursor built from another cluster's row still returns only the
// requested cluster's rows.
func TestESOHistory_QueryPage_ForgedCursorStaysInScope(t *testing.T) {
	s := NewESOHistoryStore(testDB(t))
	clusterA, clusterB := testOwnerID(t), testOwnerID(t)
	uid := testOwnerID(t)

	mustInsertHistory(t, s, historyEntry(clusterA, uid, historyBase))
	for i := range 3 {
		mustInsertHistory(t, s, historyEntry(clusterB, uid, historyBase.Add(-time.Duration(i+1)*time.Minute)))
	}

	pageA, err := s.QueryPage(t.Context(), clusterA, uid, nil, 50)
	if err != nil || len(pageA.Entries) != 1 {
		t.Fatalf("seed read = %+v, %v", pageA.Entries, err)
	}
	forged := &ESOHistoryCursor{AttemptAt: pageA.Entries[0].AttemptAt.Add(time.Hour), ID: 1<<62 - 1}

	got, err := s.QueryPage(t.Context(), clusterA, uid, forged, 50)
	if err != nil {
		t.Fatalf("QueryPage(forged): %v", err)
	}
	for _, e := range got.Entries {
		if e.ClusterID != clusterA {
			t.Fatalf("forged cursor surfaced a row from cluster %q", e.ClusterID)
		}
	}
	if len(got.Entries) != 1 {
		t.Errorf("forged far-future cursor returned %d rows; want cluster A's single row", len(got.Entries))
	}
}

// Before 000020 the dedup key was (uid, attempt_at), so the second cluster's
// attempt was swallowed by ON CONFLICT DO NOTHING.
func TestESOHistory_Insert_DedupIsClusterScoped(t *testing.T) {
	pool := testDB(t)
	s := NewESOHistoryStore(pool)
	clusterA, clusterB := testOwnerID(t), testOwnerID(t)
	uid := testOwnerID(t)

	mustInsertHistory(t, s, historyEntry(clusterA, uid, historyBase))
	mustInsertHistory(t, s, historyEntry(clusterB, uid, historyBase))
	// And the same cluster re-observing the same attempt is still absorbed.
	mustInsertHistory(t, s, historyEntry(clusterA, uid, historyBase))

	var perCluster []int
	rows, err := pool.Query(t.Context(),
		`SELECT count(*) FROM eso_sync_history
		  WHERE uid = $1 AND cluster_id IN ($2, $3)
		  GROUP BY cluster_id ORDER BY cluster_id`, uid, clusterA, clusterB)
	if err != nil {
		t.Fatalf("counting rows: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		perCluster = append(perCluster, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(perCluster) != 2 || perCluster[0] != 1 || perCluster[1] != 1 {
		t.Fatalf("rows per cluster = %v; want [1 1]", perCluster)
	}
}

func TestESOHistory_LatestByClusterUID_NoRowsIsNilNil(t *testing.T) {
	s := NewESOHistoryStore(testDB(t))
	got, err := s.LatestByClusterUID(t.Context(), testOwnerID(t), testOwnerID(t))
	if err != nil || got != nil {
		t.Fatalf("LatestByClusterUID on no rows = (%+v, %v); want (nil, nil)", got, err)
	}
}

// Empty is (empty page, nil) — the page is non-nil so it serialises as [].
func TestESOHistory_QueryPage_EmptyIsNotAnError(t *testing.T) {
	s := NewESOHistoryStore(testDB(t))
	page, err := s.QueryPage(t.Context(), testOwnerID(t), testOwnerID(t), nil, 50)
	if err != nil {
		t.Fatalf("QueryPage on no rows: %v", err)
	}
	if page.Entries == nil || len(page.Entries) != 0 || page.NextCursor != "" {
		t.Errorf("empty page = %+v; want non-nil empty entries and no cursor", page)
	}
}

// A fault must never read as "no history yet".
func TestESOHistory_DBFaultIsNotEmpty(t *testing.T) {
	pool := testDB(t)
	s := NewESOHistoryStore(pool)
	pool.Close()

	page, err := s.QueryPage(context.Background(), testOwnerID(t), testOwnerID(t), nil, 50)
	if err == nil {
		t.Fatal("QueryPage on a closed pool returned no error")
	}
	if len(page.Entries) != 0 || page.NextCursor != "" {
		t.Errorf("faulted QueryPage returned data %+v alongside its error", page)
	}

	latest, err := s.LatestByClusterUID(context.Background(), testOwnerID(t), testOwnerID(t))
	if err == nil || latest != nil {
		t.Errorf("LatestByClusterUID on a closed pool = (%+v, %v); want (nil, error)", latest, err)
	}
}

func TestESOHistory_QueryPage_ContextCancelled(t *testing.T) {
	s := NewESOHistoryStore(testDB(t))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	page, err := s.QueryPage(ctx, testOwnerID(t), testOwnerID(t), nil, 50)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("QueryPage with a cancelled context = %v; want context.Canceled", err)
	}
	if len(page.Entries) != 0 {
		t.Errorf("cancelled QueryPage returned %d entries", len(page.Entries))
	}
}

// Both page queries must seek the keyset index under a GENERIC plan, the kind
// PostgreSQL settles on after pgx has run a cached prepared statement five
// times. Result-based tests cannot see this: a query that degrades to scanning
// from the newest row down to the cursor returns the same rows, just in
// O(depth) time. Before this test existed QueryPage used one text with
// "$3 IS NULL OR (attempt_at, id) < (...)", whose generic plan moved the row
// comparison into a post-scan Filter.
func TestESOHistory_PageSQL_KeysetUnderGenericPlan(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()

	var serverVersion int
	if err := pool.QueryRow(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&serverVersion); err != nil {
		t.Fatalf("reading server version: %v", err)
	}
	if serverVersion < 160000 {
		t.Skipf("EXPLAIN (GENERIC_PLAN) needs PostgreSQL 16+; server is %d", serverVersion)
	}

	tests := []struct {
		name string
		sql  string
		// wantKeysetCond is true when the plan must push the cursor's row
		// comparison into the index condition.
		wantKeysetCond bool
	}{
		{"first page", esoHistoryFirstPageSQL, false},
		{"next page", esoHistoryNextPageSQL, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()

			// The shared test table is small, so a sequential scan is the
			// cheapest plan and would hide the question. Disabling it asks
			// what the planner does when it must use an index, which is the
			// production situation for an object with a long history.
			if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
				t.Fatalf("disabling seqscan: %v", err)
			}
			// Simple-query protocol: GENERIC_PLAN explains a statement with
			// unbound $n placeholders, which pgx's extended protocol refuses
			// to send without arguments.
			results, err := tx.Conn().PgConn().Exec(ctx, `EXPLAIN (GENERIC_PLAN, FORMAT JSON) `+tc.sql).ReadAll()
			if err != nil {
				t.Fatalf("EXPLAIN: %v", err)
			}
			if len(results) != 1 || len(results[0].Rows) != 1 || len(results[0].Rows[0]) != 1 {
				t.Fatalf("EXPLAIN returned an unexpected shape: %+v", results)
			}
			raw := results[0].Rows[0][0]
			var plans []struct {
				Plan map[string]any `json:"Plan"`
			}
			if err := json.Unmarshal(raw, &plans); err != nil || len(plans) != 1 {
				t.Fatalf("decoding plan %s: %v", raw, err)
			}

			var keysetScan map[string]any
			var walk func(node map[string]any)
			walk = func(node map[string]any) {
				if filter, _ := node["Filter"].(string); strings.Contains(filter, "attempt_at") {
					t.Errorf("plan filters on attempt_at after the scan (%q); the cursor is not an index condition:\n%s", filter, raw)
				}
				if name, _ := node["Index Name"].(string); name == "idx_eso_sync_history_keyset" {
					keysetScan = node
				}
				children, _ := node["Plans"].([]any)
				for _, c := range children {
					if child, ok := c.(map[string]any); ok {
						walk(child)
					}
				}
			}
			walk(plans[0].Plan)

			if keysetScan == nil {
				t.Fatalf("generic plan does not use idx_eso_sync_history_keyset:\n%s", raw)
			}
			cond, _ := keysetScan["Index Cond"].(string)
			if !strings.Contains(cond, "cluster_id") || !strings.Contains(cond, "uid") {
				t.Errorf("keyset Index Cond = %q; want it to pin cluster_id and uid", cond)
			}
			if tc.wantKeysetCond && !strings.Contains(cond, "attempt_at") {
				t.Errorf("keyset Index Cond = %q; want the (attempt_at, id) row comparison in it", cond)
			}
		})
	}
}

// TestMigration_ScopeESOHistory_RoundTrip steps a throwaway database across
// 000020 in both directions. It cannot use the shared harness database: it
// rolls a migration back and forces a dirty version, which would pull the
// schema out from under every other test in the binary.
func TestMigration_ScopeESOHistory_RoundTrip(t *testing.T) {
	m, pool := migrationScratchDB(t)
	ctx := t.Context()
	s := NewESOHistoryStore(pool)

	if err := m.Migrate(19); err != nil {
		t.Fatalf("migrating to 000019: %v", err)
	}

	insertRaw := func(clusterID, uid string, at time.Time) error {
		_, err := pool.Exec(ctx,
			`INSERT INTO eso_sync_history (cluster_id, uid, namespace, name, attempt_at, outcome)
			 VALUES ($1, $2, 'apps', 'db', $3, 'success')`, clusterID, uid, at)
		return err
	}
	survivor := testOwnerID(t)
	if err := insertRaw("local", survivor, historyBase); err != nil {
		t.Fatalf("seeding at 000019: %v", err)
	}

	// 1. At 000019 the key is cluster-blind: the same attempt in a second
	//    cluster is refused. Without this the test would pass against a schema
	//    that had never been narrow.
	shared := testOwnerID(t)
	if err := insertRaw("cluster-a", shared, historyBase); err != nil {
		t.Fatalf("seeding cluster-a at 000019: %v", err)
	}
	if err := insertRaw("cluster-b", shared, historyBase); err == nil {
		t.Fatal("inserted the same (uid, attempt_at) for a second cluster at 000019; the old key was already cluster-scoped")
	}

	// 2. Up. The new key admits the second cluster, and Insert's conflict
	//    target now matches an index (a mismatch is SQLSTATE 42P10).
	if err := m.Migrate(20); err != nil {
		t.Fatalf("migrating 000019 -> 000020: %v", err)
	}
	for _, idx := range []string{"idx_eso_sync_history_dedup_cluster", "idx_eso_sync_history_keyset"} {
		if !indexExists(t, pool, idx) {
			t.Errorf("index %s missing after 000020", idx)
		}
	}
	for _, idx := range []string{"idx_eso_sync_history_dedup", "idx_eso_sync_history_uid_attempt"} {
		if indexExists(t, pool, idx) {
			t.Errorf("index %s still present after 000020", idx)
		}
	}
	if !indexExists(t, pool, "idx_eso_sync_history_attempt_at") || !indexExists(t, pool, "idx_eso_sync_history_cluster_failures") {
		t.Error("000020 dropped an index it does not own")
	}
	if err := s.Insert(ctx, historyEntry("cluster-b", shared, historyBase)); err != nil {
		t.Fatalf("Insert of the second cluster's attempt after 000020: %v", err)
	}

	// 3. The rollback must refuse while two clusters share (uid, attempt_at).
	if err := m.Migrate(19); err == nil {
		t.Fatal("rolled back 000020 with a cross-cluster duplicate present; the down migration must refuse rather than drop one cluster's row")
	}
	// The refused down leaves the version dirty but the schema intact (one
	// implicit transaction). Check the schema before forcing, or forcing
	// would hide a half-applied rollback.
	if !indexExists(t, pool, "idx_eso_sync_history_dedup_cluster") || indexExists(t, pool, "idx_eso_sync_history_dedup") {
		t.Fatal("the refused rollback was not atomic; forcing the version would hide a half-applied schema")
	}
	if err := m.Force(20); err != nil {
		t.Fatalf("clearing the dirty version after the expected failure: %v", err)
	}

	// 4. Resolve the duplicate as NOTES.txt tells the operator to, and the
	//    rollback goes through.
	if _, err := pool.Exec(ctx,
		`DELETE FROM eso_sync_history WHERE uid = $1 AND cluster_id = 'cluster-b'`, shared); err != nil {
		t.Fatalf("resolving the duplicate: %v", err)
	}
	if err := m.Migrate(19); err != nil {
		t.Fatalf("rolling back 000020 with no duplicates: %v", err)
	}
	if !indexExists(t, pool, "idx_eso_sync_history_dedup") || !indexExists(t, pool, "idx_eso_sync_history_uid_attempt") {
		t.Error("rollback did not restore the 000019 indexes")
	}

	// 5. Re-apply. The unrelated row was never in scope.
	if err := m.Migrate(20); err != nil {
		t.Fatalf("re-applying 000020: %v", err)
	}
	page, err := s.QueryPage(ctx, "local", survivor, nil, 50)
	if err != nil {
		t.Fatalf("reading the survivor: %v", err)
	}
	if len(page.Entries) != 1 {
		t.Errorf("survivor rows after down+up = %d; want 1", len(page.Entries))
	}
}
